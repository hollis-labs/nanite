// This file (store_adapter.go) implements gosched.Store: the persistence
// seam go-scheduler's Engine polls every tick, converting agent_schedules
// rows into the library's neutral gosched.Schedule type and back.
//
// See docs/engineering/architecture/12-scheduling.md ("The Store adapter")
// for the design, and apps/hadron/internal/scheduler/adapter.go for the
// directly-transferable storeAdapter template this file follows.
//
// Package-location and embedding-vs-wrapper calls (TASKS/scheduling/
// 02-store-adapter.md's Context asked both to be made and documented
// explicitly):
//
//   - Package: internal/scheduler, the same package runner_adapter.go
//     (TASKS/scheduling/03) already lives in -- confirmed via grep before
//     writing this file that no internal/scheduler-shaped concept already
//     existed under a different name (internal/agent/reflexes'
//     recurrence.go is a fire-cooldown/debounce guard, not a next-
//     occurrence calculator; see docs/engineering/GLOSSARY.md's Reflexes
//     entry).
//
//   - Embedding vs. separate wrapper: this file does NOT follow Hadron's
//     `type storeAdapter struct { *persistence.Store }` embedding shortcut,
//     even though it would work mechanically (ClaimAndUpdateScheduleRun/
//     SetScheduleNextRun/DisableSchedule could be added directly onto
//     *store.Store with gosched-matching signatures, using only time.Time/
//     string/bool/error -- no gosched import needed on the store package's
//     side -- and then promoted). Deliberately not done, for two reasons:
//     (1) internal/store/agent_schedules.go already has a consistent,
//     Nanite-flavored naming convention for this table (InsertAgentSchedule,
//     GetAgentSchedule, ListAgentSchedules, UpdateAgentScheduleStatus,
//     BumpAgentScheduleFireCount) -- bare gosched-interface-shaped names
//     (ClaimAndUpdateScheduleRun, SetScheduleNextRun, DisableSchedule) with
//     no AgentSchedule-qualifying prefix would break that convention and
//     make it ambiguous whether a given *store.Store method is a Nanite-
//     native concept or a go-scheduler-interface promotion target. (2) this
//     package's sibling file, runner_adapter.go (03, already merged),
//     explicitly documents and follows a narrow-dependency-interface
//     convention specifically to avoid exposing a wide concrete service
//     type's entire method surface through a scheduler-package wrapper;
//     embedding *store.Store (a large, general-purpose type used across the
//     whole app for sessions/agents/reflexes/etc., not a type designed
//     around scheduling the way Hadron's persistence.Store apparently was)
//     would silently promote that type's entire API through StoreAdapter,
//     the opposite of that convention. Instead: three small, Nanite-
//     convention-named low-level DB methods were added to
//     internal/store/agent_schedules.go (ListDueAgentSchedules,
//     ClaimAgentScheduleRun, SetAgentScheduleNextRun), and DisableSchedule
//     reuses the existing UpdateAgentScheduleStatus (see this file's
//     DisableSchedule doc comment for the status mapping). StoreAdapter
//     holds *store.Store as a plain named field (not narrowed to an
//     interface, unlike runner_adapter.go's dependencies) because the CAS
//     claim's correctness argument is specifically tied to *store.Store's
//     own sqlitekit.OpenSingle single-writer-connection configuration, not
//     to "anything satisfying a narrow Store-shaped interface" -- narrowing
//     it here would blur that argument. TASKS/scheduling/02-store-
//     adapter.md's own required regression test (a real, not
//     in-memory-only, *store.Store) reflects the same reasoning.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	return out, nil
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
// Neither this method nor any other part of this adapter calls
// gosched.NextRun/hand-rolls cron-next-occurrence math: libs/go-
// scheduler/engine.go's tick() already owns computing a cron schedule's
// next next_run (via its own NextRun call) and a one-time schedule's
// disable-after-fire placeholder, then hands the result to
// ClaimAndUpdateScheduleRun -- this adapter only ever persists whatever
// next-run value the engine computed, exactly matching Hadron's own
// adapter.go (which also contains zero cron-math). This is the strongest
// form of TASKS/scheduling/02-store-adapter.md step 5's "don't hand-roll a
// second cron-math implementation": there is no cron-math in this package
// to duplicate go-scheduler's own NextRun in the first place. See this
// file's regression test (TestStoreAdapter_NextRunRoundTrip_OneShotAndCron)
// for the round-trip proof, which calls gosched.NextRun directly to
// simulate exactly what the real engine does.
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
// Today's real data shape (confirmed via managed_durable_configs.go's
// syncManagedDurableAgentConfig, which creates exactly one instance and
// then exactly one profile-scoped schedule per managed YAML config) is
// 1:1: one profile_id maps to one instance. Nothing in the schema enforces
// that going forward, though -- agent_schedules is scoped to a profile,
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

// ClaimAndUpdateScheduleRun is the compare-and-set claim go-scheduler's
// duplicate-dispatch safety rests on -- see internal/store/
// agent_schedules.go's ClaimAgentScheduleRun doc comment for the full
// safety argument (single-writer connection pool, no additional locking
// needed).
func (a *StoreAdapter) ClaimAndUpdateScheduleRun(ctx context.Context, id string, expectedNext, lastRun, nextRun time.Time) (bool, error) {
	claimed, err := a.Store.ClaimAgentScheduleRun(ctx, id, expectedNext, lastRun, nextRun)
	if err != nil {
		return false, fmt.Errorf("scheduler: claim agent_schedules run %s: %w", id, err)
	}
	return claimed, nil
}

// SetScheduleNextRun resets a schedule's next_run unconditionally -- the
// engine's own rollback path after a failed Runner.Enqueue
// (libs/go-scheduler/engine.go's tick()).
func (a *StoreAdapter) SetScheduleNextRun(ctx context.Context, id string, nextRun time.Time) error {
	if err := a.Store.SetAgentScheduleNextRun(ctx, id, nextRun); err != nil {
		return fmt.Errorf("scheduler: set agent_schedules next_run %s: %w", id, err)
	}
	return nil
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
// ClaimAgentScheduleRun, SetAgentScheduleNextRun, backfillScheduleNextRun)
// only ever writes time.RFC3339 or NULL.
func parseScheduleTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, v)
}
