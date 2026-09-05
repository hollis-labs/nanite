// StoreAdapter implements go-scheduler's durable v0.2 Store contract over
// Nanite's agent_schedules and schedule_runs tables. The application owns the
// schema and payload mapping; the library owns fire lifecycle and retry rules.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

// errNoDurableAgentInstance and errAmbiguousDurableAgentInstance are the two
// ways resolveDurableAgentInstanceID can fail to produce a single,
// unambiguous durable_agent_instances.id for a durable_agent_wake row's
// agent_id (an agent_profiles.id). Both are treated as "skip this row, log
// it, keep the tick moving" by ListDueSchedules -- see that method's doc
// comment.
var (
	errNoDurableAgentInstance        = errors.New("no durable_agent_instances row for this agent_profiles.id")
	errAmbiguousDurableAgentInstance = errors.New("multiple durable_agent_instances rows share this agent_profiles.id; cannot resolve an unambiguous wake target")
)

// StoreAdapter implements gosched.Store over Nanite's agent_schedules
// table. Logger is required, matching internal/agent/reflexes.Executor's
// own "Logger *slog.Logger // Required." convention in this codebase.
type StoreAdapter struct {
	Store  *store.Store
	Logger *slog.Logger
}

const (
	scheduleRetryInitialDelay = 30 * time.Second
	scheduleRetryMaximumDelay = 5 * time.Minute
	workflowRetryInitialDelay = time.Second
	workflowRetryMaximumDelay = 30 * time.Second
	workflowRetryMaxAttempts  = 3
)

var _ gosched.Store = (*StoreAdapter)(nil)

// ListDueSchedules loads due agent_schedules rows and converts each into
// the neutral gosched.Schedule the engine understands, packing job-type-
// specific data into the opaque Payload per
// docs/engineering/architecture/12-scheduling.md's "Runner adapter and job
// taxonomy" table and this package's own DurableAgentWakePayload/
// AgentWorkflowRunPayload/CommandRunPayload/ReflexDispatchPayload types
// (runner_adapter.go, TASKS/scheduling/03 -- the fixed contract this
// method's Payload encoding must stay wire-compatible with).
//
// A row that cannot be converted (payload encode failure, an unresolvable
// or ambiguous durable_agent_wake instance target, invalid JSON in
// job_payload) is skipped with a logged warning rather than aborting the
// whole tick -- the same "skip the unconvertible record" behavior Hadron's
// own storeAdapter.ListDueSchedules uses (adapter.go's toSchedule).
func (a *StoreAdapter) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]gosched.Schedule, error) {
	rows, err := a.Store.ListDueAgentSchedules(ctx, now, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list due agent_schedules: %w", err)
	}
	out := make([]gosched.Schedule, 0, len(rows))
	for _, row := range rows {
		sched, convErr := a.toSchedule(row)
		if convErr != nil {
			a.Logger.Warn("scheduler: skipping agent_schedules row that could not convert to gosched.Schedule",
				"schedule_id", row.ID, "agent_id", row.AgentID, "job_type", row.JobType, "error", convErr)
			continue
		}
		out = append(out, sched)
	}
	activations, err := a.Store.ListDueWorkflowActivationSchedules(ctx, now, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list due workflow activations: %w", err)
	}
	for _, row := range activations {
		sched, convErr := toWorkflowActivationSchedule(row)
		if convErr != nil {
			a.Logger.Warn("scheduler: skipping workflow activation that could not convert to gosched.Schedule",
				"schedule_id", row.ScheduleID, "activation_id", row.ActivationID, "error", convErr)
			continue
		}
		out = append(out, sched)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextRun.Equal(out[j].NextRun) {
			return out[i].NextRun.Before(out[j].NextRun)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func toWorkflowActivationSchedule(row store.WorkflowActivationSchedule) (gosched.Schedule, error) {
	nextRun, err := parseScheduleTime(row.NextRun)
	if err != nil {
		return gosched.Schedule{}, fmt.Errorf("parse activation next_run %q: %w", row.NextRun, err)
	}
	if !json.Valid([]byte(row.ActivationJSON)) {
		return gosched.Schedule{}, fmt.Errorf("activation payload is not valid JSON")
	}
	return gosched.Schedule{
		ID: row.ScheduleID, NextRun: nextRun, Enabled: row.Status == "active",
		JobType: JobTypeWorkflowActivation, Payload: []byte(row.ActivationJSON),
		Retry: gosched.RetryPolicy{
			MaxAttempts: workflowRetryMaxAttempts,
			Backoff: gosched.BackoffPolicy{
				Strategy: gosched.BackoffExponential, InitialDelay: workflowRetryInitialDelay,
				MaxDelay: workflowRetryMaximumDelay,
			},
		},
	}, nil
}

// toSchedule converts one AgentSchedule row into a neutral gosched.Schedule.
//
// CronExpr is schedule_spec for a cron-kind row and deliberately forced
// empty for a one_shot-kind row (regardless of what schedule_spec holds --
// backfillScheduleNextRun's own doc comment in internal/store/
// agent_schedules.go confirms a one_shot row has no independent target-time
// encoding in schedule_spec today), matching go-scheduler's own "empty
// CronExpr means one-time" convention (libs/go-scheduler/scheduler.go:26).
//
// Cron math stays inside go-scheduler. CreateFire receives and atomically
// persists the next-run value the engine computed.
func (a *StoreAdapter) toSchedule(row store.AgentSchedule) (gosched.Schedule, error) {
	nextRun, err := parseScheduleTime(row.NextRun)
	if err != nil {
		return gosched.Schedule{}, fmt.Errorf("parse next_run %q: %w", row.NextRun, err)
	}
	lastRun, err := parseScheduleTime(row.LastFiredAt)
	if err != nil {
		return gosched.Schedule{}, fmt.Errorf("parse last_fired_at %q: %w", row.LastFiredAt, err)
	}
	cronExpr := ""
	if row.ScheduleKind == store.ScheduleKindCron {
		cronExpr = row.ScheduleSpec
	}
	payload, err := a.buildPayload(row)
	if err != nil {
		return gosched.Schedule{}, err
	}
	return gosched.Schedule{
		ID:       row.ID,
		CronExpr: cronExpr,
		LastRun:  lastRun,
		NextRun:  nextRun,
		Enabled:  row.Status == store.ScheduleStatusActive,
		JobType:  row.JobType,
		Payload:  payload,
		Retry: gosched.RetryPolicy{
			// Nanite's max_retries has historically meant total attempts,
			// despite its name. Keep that persisted behavior at the boundary.
			MaxAttempts: int(row.MaxRetries),
			Backoff: gosched.BackoffPolicy{
				Strategy:     gosched.BackoffExponential,
				InitialDelay: scheduleRetryInitialDelay,
				MaxDelay:     scheduleRetryMaximumDelay,
			},
		},
	}, nil
}

// buildPayload marshals the neutral Payload for one agent_schedules row per
// its job_type.
//
// durable_agent_wake: migration 127 deliberately left job_payload at its
// '{}' default for every existing row of this type -- the real payload
// data lives in agent_id (resolved here to a durable_agent_instances.id,
// see resolveDurableAgentInstanceID) and body (forwarded as Prompt,
// matching durable_wake.go's own RunDue, which forwards
// item.Schedule.Body as WakePayload.Prompt today). Reason/ProjectID are
// deliberately left empty here rather than computed: DurableAgentWakeService.
// Wake already falls back to wakeReasonForInstance(inst)/
// resolveWakeScope(inst).ProjectID internally whenever the incoming request
// leaves them empty (durable_wake.go's Wake, lines around payload.Reason ==
// "" and projectID == ""), so duplicating that fallback here would just be
// a second, driftable copy of logic Wake already owns.
//
// agent_workflow_run / command_run / reflex_dispatch / loop_run_tick:
// job_payload already holds the JSON (pass-through, not parse-and-repack --
// validated via json.Valid so a corrupt row is skipped by ListDueSchedules
// rather than handed to the Runner as garbage, but never re-marshaled,
// since re-marshaling a byte-identical JSON object buys nothing and risks
// silently reordering/dropping fields RunnerAdapter's own struct doesn't
// know about). loop_run_tick (TASKS/loops/
// 12-loop-run-tick-scheduled-trigger.md) follows this same fifth-column
// convention exactly -- its LoopRunTickPayload{LoopRunID} is exactly as
// simple as the other three pass-through shapes, with no producer-specific
// resolution step (unlike durable_agent_wake's instance-id resolution
// above).
func (a *StoreAdapter) buildPayload(row store.AgentSchedule) ([]byte, error) {
	switch row.JobType {
	case JobTypeDurableAgentWake:
		instanceID, err := a.resolveDurableAgentInstanceID(row.AgentID)
		if err != nil {
			return nil, fmt.Errorf("resolve durable_agent_instances for agent_id %s: %w", row.AgentID, err)
		}
		payload := DurableAgentWakePayload{
			InstanceID: instanceID,
			Prompt:     row.Body,
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode durable_agent_wake payload: %w", err)
		}
		return b, nil
	case JobTypeAgentWorkflowRun, JobTypeCommandRun, JobTypeReflexDispatch, JobTypeLoopRunTick:
		raw := row.JobPayload
		if raw == "" {
			raw = "{}"
		}
		if !json.Valid([]byte(raw)) {
			return nil, fmt.Errorf("job_payload is not valid JSON")
		}
		return []byte(raw), nil
	default:
		return nil, fmt.Errorf("unknown job_type %q", row.JobType)
	}
}

// resolveDurableAgentInstanceID resolves an agent_schedules.agent_id (an
// agent_profiles.id -- see 071_agent_schedules.sql and durable_wake.go's
// own ListDue, which keys ListAgentSchedules(ctx, inst.ProfileID) the exact
// same way) to the one durable_agent_instances row bound to that profile,
// reusing the existing store.ListDurableAgentInstances(false) listing
// (durable_wake.go's ListDue/ListSchedules is the confirmed existing
// resolution mechanism this method mirrors, in the reverse direction: it
// walks instances outer, schedules-by-profile-id inner; this walks the
// single profile_id this schedule row already carries against every
// instance).
//
// Most deployed data currently has one instance per profile, but nothing in
// the schema enforces that -- agent_schedules is scoped to a profile,
// not an instance, so two instances of the same profile sharing one
// schedule row is a real (if currently unexercised) possibility. Rather
// than guess which instance a schedule fires against in that case, this
// resolves only the unambiguous 0-or-1-match cases and treats 2+ matches
// as a conversion failure (skipped by ListDueSchedules, logged) --
// silently picking one of several matching instances would be a worse
// failure mode than skipping a firing, since it could wake the wrong
// instance with no visible error. Revisit if/when a real multi-instance-
// per-profile durable_agent_wake schedule appears.
func (a *StoreAdapter) resolveDurableAgentInstanceID(profileID string) (string, error) {
	instances, err := a.Store.ListDurableAgentInstances(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, false)
	if err != nil {
		return "", fmt.Errorf("list durable_agent_instances: %w", err)
	}
	var matchID string
	matches := 0
	for _, inst := range instances {
		if inst.ProfileID == profileID {
			matches++
			matchID = inst.ID
		}
	}
	switch matches {
	case 0:
		return "", errNoDurableAgentInstance
	case 1:
		return matchID, nil
	default:
		return "", errAmbiguousDurableAgentInstance
	}
}

// CreateFire atomically materializes a stable durable fire and advances the
// schedule through the store's single transaction/CAS boundary.
func (a *StoreAdapter) CreateFire(ctx context.Context, creation gosched.FireCreation) (bool, error) {
	request := store.ScheduleFireCreation{
		ScheduleID:   creation.ScheduleID,
		ExpectedNext: creation.ExpectedNext,
		NextRun:      creation.NextRun,
		Fire:         fromFire(creation.Fire),
	}
	var created bool
	var err error
	if store.IsWorkflowActivationScheduleID(creation.ScheduleID) {
		created, err = a.Store.CreateWorkflowActivationFire(ctx, request)
	} else {
		created, err = a.Store.CreateScheduleFire(ctx, request)
	}
	if err != nil {
		return false, fmt.Errorf("scheduler: create fire %s: %w", creation.Fire.ID, err)
	}
	return created, nil
}

func (a *StoreAdapter) ListDueFires(ctx context.Context, now time.Time, limit int) ([]gosched.Fire, error) {
	rows, err := a.Store.ListDueScheduleFires(ctx, now, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list due fires: %w", err)
	}
	out := make([]gosched.Fire, 0, len(rows))
	for _, row := range rows {
		fire, conversionErr := toFire(row)
		if conversionErr != nil {
			return nil, fmt.Errorf("scheduler: decode fire %s: %w", row.RunID, conversionErr)
		}
		out = append(out, fire)
	}
	workflowRows, err := a.Store.ListDueWorkflowActivationFires(ctx, now, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list due workflow activation fires: %w", err)
	}
	for _, row := range workflowRows {
		fire, convErr := toFire(row)
		if convErr != nil {
			return nil, fmt.Errorf("scheduler: decode workflow activation fire %s: %w", row.RunID, convErr)
		}
		out = append(out, fire)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].NextAttemptAt, out[j].NextAttemptAt
		if left.IsZero() {
			left = out[i].ScheduledAt
		}
		if right.IsZero() {
			right = out[j].ScheduledAt
		}
		if !left.Equal(right) {
			return left.Before(right)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a *StoreAdapter) ClaimFire(ctx context.Context, claim gosched.FireClaim) (gosched.Fire, bool, error) {
	request := store.ScheduleFireClaim{
		FireID:          claim.FireID,
		ExpectedStatus:  string(claim.ExpectedStatus),
		ExpectedAttempt: int64(claim.ExpectedAttempt),
		ExpectedFiredAt: claim.ExpectedFiredAt,
		ClaimedAt:       claim.ClaimedAt,
		ClaimExpiresAt:  claim.ClaimExpiresAt,
	}
	var row store.ScheduleFire
	var won bool
	var err error
	if _, lookupErr := a.Store.GetWorkflowActivationFire(ctx, claim.FireID); lookupErr == nil {
		row, won, err = a.Store.ClaimWorkflowActivationFire(ctx, request)
	} else if !errors.Is(lookupErr, store.ErrWorkflowActivationFireNotFound) {
		return gosched.Fire{}, false, lookupErr
	} else {
		row, won, err = a.Store.ClaimScheduleFire(ctx, request)
	}
	if err != nil || !won {
		return gosched.Fire{}, won, err
	}
	fire, err := toFire(row)
	if err != nil {
		return gosched.Fire{}, false, fmt.Errorf("scheduler: decode claimed fire %s: %w", claim.FireID, err)
	}
	return fire, true, nil
}

func (a *StoreAdapter) TransitionFire(ctx context.Context, transition gosched.FireTransition) (bool, error) {
	request := store.ScheduleFireTransition{
		FireID:        transition.FireID,
		Attempt:       int64(transition.Attempt),
		From:          string(transition.From),
		ClaimedAt:     transition.ClaimedAt,
		To:            string(transition.To),
		NextAttemptAt: transition.NextAttemptAt,
		LastError:     transition.Error,
	}
	var transitioned bool
	var err error
	if _, lookupErr := a.Store.GetWorkflowActivationFire(ctx, transition.FireID); lookupErr == nil {
		transitioned, err = a.Store.TransitionWorkflowActivationFire(ctx, request)
	} else if !errors.Is(lookupErr, store.ErrWorkflowActivationFireNotFound) {
		return false, lookupErr
	} else {
		transitioned, err = a.Store.TransitionScheduleFire(ctx, request)
	}
	if err != nil {
		return false, fmt.Errorf("scheduler: transition fire %s: %w", transition.FireID, err)
	}
	return transitioned, nil
}

func fromFire(fire gosched.Fire) store.ScheduleFire {
	strategy := fire.Retry.Backoff.Strategy
	if strategy == "" {
		strategy = gosched.BackoffNone
	}
	return store.ScheduleFire{
		ID:                     fire.ID,
		ScheduleID:             fire.ScheduleID,
		RunID:                  fire.ID,
		ScheduledAt:            formatScheduleTime(fire.ScheduledAt),
		FiredAt:                formatScheduleTime(fire.FiredAt),
		ClaimExpiresAt:         formatScheduleTime(fire.ClaimExpiresAt),
		Status:                 string(fire.Status),
		AttemptCount:           int64(fire.Attempt),
		LastError:              fire.LastError,
		NextAttemptAt:          formatScheduleTime(fire.NextAttemptAt),
		RetryMaxAttempts:       int64(fire.Retry.MaxAttempts),
		RetryBackoffStrategy:   string(strategy),
		RetryInitialDelayNanos: int64(fire.Retry.Backoff.InitialDelay),
		RetryMaximumDelayNanos: int64(fire.Retry.Backoff.MaxDelay),
		JobType:                fire.JobType,
		JobPayload:             string(fire.Payload),
	}
}

func toFire(row store.ScheduleFire) (gosched.Fire, error) {
	scheduledAt, err := parseScheduleTime(row.ScheduledAt)
	if err != nil {
		return gosched.Fire{}, fmt.Errorf("parse scheduled_at: %w", err)
	}
	firedAt, err := parseScheduleTime(row.FiredAt)
	if err != nil {
		return gosched.Fire{}, fmt.Errorf("parse fired_at: %w", err)
	}
	claimExpiresAt, err := parseScheduleTime(row.ClaimExpiresAt)
	if err != nil {
		return gosched.Fire{}, fmt.Errorf("parse claim_expires_at: %w", err)
	}
	nextAttemptAt, err := parseScheduleTime(row.NextAttemptAt)
	if err != nil {
		return gosched.Fire{}, fmt.Errorf("parse next_attempt_at: %w", err)
	}
	return gosched.Fire{
		ID:             row.RunID,
		ScheduleID:     row.ScheduleID,
		ScheduledAt:    scheduledAt,
		FiredAt:        firedAt,
		ClaimExpiresAt: claimExpiresAt,
		Attempt:        int(row.AttemptCount),
		Status:         gosched.FireStatus(row.Status),
		NextAttemptAt:  nextAttemptAt,
		LastError:      row.LastError,
		Retry: gosched.RetryPolicy{
			MaxAttempts: int(row.RetryMaxAttempts),
			Backoff: gosched.BackoffPolicy{
				Strategy:     gosched.BackoffStrategy(row.RetryBackoffStrategy),
				InitialDelay: time.Duration(row.RetryInitialDelayNanos),
				MaxDelay:     time.Duration(row.RetryMaximumDelayNanos),
			},
		},
		JobType: row.JobType,
		Payload: []byte(row.JobPayload),
	}, nil
}

// DisableSchedule marks a schedule disabled. The engine's only call site
// today is immediately after a one-time (empty-CronExpr) schedule's
// successful dispatch (libs/go-scheduler/engine.go's tick()) -- the exact
// same event durable_wake.go's RunDue already handles today by setting
// status=expired for a fired ScheduleKindOneShot row. This maps
// DisableSchedule onto that same existing status value (via the existing
// UpdateAgentScheduleStatus, not a new column or a new "disabled" status)
// to preserve that established behavior/vocabulary rather than introduce a
// parallel disabled concept: 'expired' already means "done, naturally
// terminal, no longer active" in this table's status vocabulary
// (docs/engineering/architecture/12-scheduling.md's on_fail policy notes
// 'paused' is reserved for a reversible, operator-toggled-off state, which
// this is not).
func (a *StoreAdapter) DisableSchedule(ctx context.Context, id string) error {
	if store.IsWorkflowActivationScheduleID(id) {
		if err := a.Store.DisableWorkflowActivationSchedule(ctx, id, time.Now().UTC()); err != nil {
			return fmt.Errorf("scheduler: disable workflow activation schedule %s: %w", id, err)
		}
		return nil
	}
	if err := a.Store.UpdateAgentScheduleStatus(ctx, id, store.ScheduleStatusExpired); err != nil {
		return fmt.Errorf("scheduler: disable agent_schedules row %s: %w", id, err)
	}
	return nil
}

// parseScheduleTime decodes an RFC3339 nullable-in-practice timestamp
// (agent_schedules.next_run/last_fired_at, both COALESCE'd to "" by
// agentScheduleColumns when NULL). An empty string decodes to the zero
// time.Time, matching gosched.Schedule's own "zero NextRun is unscheduled/
// zero LastRun means never run" convention -- mirroring Hadron's
// adapter.go's parseNullTime, but returning an error instead of silently
// zeroing on a genuinely malformed (non-empty, unparseable) value, since an
// agent_schedules row should never have one: every writer in this codebase
// (InsertAgentSchedule via nullIfEmpty, BumpAgentScheduleFireCount,
// CreateScheduleFire, backfillScheduleNextRun)
// only ever writes RFC3339 timestamps or NULL.
func parseScheduleTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, v)
}

func formatScheduleTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
