// TASKS/scheduling/07-wire-add-schedule-reflex.md: wires
// reflexEngine.Executor.Schedule (internal/agent/reflexes/executor.go's
// ScheduleHook) so a firing action_kind='add_schedule' reflex genuinely
// inserts an agent_schedules row -- closing CW-20260819-0006's loop per
// docs/engineering/architecture/12-scheduling.md's "Producers" item 3. The
// hook itself is wired in container.go, right alongside
// reflexEngine.Executor.Halt; this file holds the (deliberately pure,
// DB-free, easily-testable) action_spec -> store.AgentSchedule translation.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// reflexScheduleSource is the agent_schedules.created_by value stamped on
// every row this hook inserts -- lets an operator tell a reflex-authored
// schedule apart from a boot-time YAML-synced one
// (managedDurableConfigSource, managed_durable_configs.go) or an
// operator-authored one (InsertAgentSchedule's own "operator" default) at a
// glance. No CHECK constraint governs created_by (migration
// 127_schedule_runs_and_retry_policy.sql leaves it free-text), so this is
// just a convention, not a schema requirement.
const reflexScheduleSource = "reflex"

// NewReflexScheduleHook builds the reflexes.ScheduleHook assigned to
// reflexEngine.Executor.Schedule in container.go (mirroring the existing
// Executor.Halt assignment right next to it). Closing over *store.Store
// here -- rather than threading it through buildReflexAgentSchedule below
// -- keeps the translation logic itself a pure function with no DB
// dependency, so a regression test can exercise it directly without a real
// store. Exported (rather than kept container.go-local) so a same-module
// end-to-end verification (constructing the real reflexes.Executor +
// go-scheduler Engine together, outside package service, without an
// internal/service <-> internal/scheduler import cycle) can wire the exact
// same hook production uses.
func NewReflexScheduleHook(st *store.Store) reflexes.ScheduleHook {
	return func(ctx context.Context, agentID string, spec map[string]interface{}) error {
		row, err := buildReflexAgentSchedule(agentID, spec, time.Now())
		if err != nil {
			return fmt.Errorf("reflex add_schedule: %w", err)
		}
		if err := st.InsertAgentSchedule(ctx, row); err != nil {
			return fmt.Errorf("reflex add_schedule: %w", err)
		}
		return nil
	}
}

// buildReflexAgentSchedule translates a fired add_schedule reflex's
// already-JSON-unmarshaled action_spec (internal/agent/reflexes/
// executor.go's Apply unmarshals reflex.ActionSpec before ever calling the
// Schedule hook) into a store.AgentSchedule ready for InsertAgentSchedule.
//
// action_spec field names -- this task's own design call (07's brief:
// "decide and document the exact spec field names," since no reflex
// anywhere declared action_kind='add_schedule' before this task, so there
// was no existing shape to match against):
//
//	name           string  required. -> Row.Name (human label).
//	schedule_kind  string  required. "cron" | "one_shot"
//	               (store.ScheduleKindCron / ScheduleKindOneShot -- the two
//	               live values per migration 127's narrowed CHECK).
//	schedule_spec  string  required when schedule_kind="cron" (a standard
//	               5-field cron expression, e.g. "0 9 * * *"). Optional/
//	               ignored for "one_shot" -- matches
//	               backfillScheduleNextRun's own confirmed finding
//	               (agent_schedules.go) that a one_shot row has no
//	               independent target-time encoding today.
//	body           string  required. -> Row.Body, the agent-facing prompt/
//	               instruction text. For job_type=durable_agent_wake (the
//	               default), internal/scheduler's StoreAdapter forwards this
//	               straight through as WakePayload.Prompt.
//	job_type       string  optional, default "durable_agent_wake"
//	               (store.ScheduleJobType* constants). Must be one of the
//	               four seeded job types if given explicitly.
//	job_payload    object|string  optional. Only meaningful for job types
//	               other than durable_agent_wake -- internal/scheduler's
//	               RunnerAdapter decodes this directly
//	               (TASKS/scheduling/02-store-adapter.md's confirmed
//	               "pass-through-as-is, not parse-and-repack" contract) into
//	               that job type's own *Payload struct
//	               (internal/scheduler/runner_adapter.go), so the reflex
//	               author is responsible for matching that job type's field
//	               names (AgentWorkflowRunPayload / CommandRunPayload /
//	               ReflexDispatchPayload). Given as a nested JSON object it
//	               is re-marshaled to a string; given as an already-encoded
//	               JSON string it is validated and used as-is. Omitted ->
//	               InsertAgentSchedule's own "{}" default.
//	max_retries    number  optional. Omitted (or <= 0) -> InsertAgentSchedule's
//	               own column default (3) applies -- left unset here rather
//	               than hard-coding 3 a second time.
//	on_fail        string  optional, one of retry|disable|notify if given.
//	               Omitted -> InsertAgentSchedule's own "retry" default.
//	priority       number  optional, default 0.
//	session_id     string  optional. Empty (the Go zero value) means
//	               "applies to all sessions of this agent," per
//	               AgentSchedule.SessionID's own doc comment.
//	expires_at     string  optional, RFC3339.
//
// next_run is computed here via the exported store.ComputeAgentScheduleNextRun
// helper (01/05) -- deliberately NOT a second hand-rolled cron-parse, per
// this task's own explicit instruction and TASKS/ESCALATIONS.md's
// 2026-08-20 note flagging that exact duplication already happening once
// in task 05's review. An inserted row therefore always has a real,
// non-NULL next_run and is immediately due-eligible to
// ListDueAgentSchedules on the very next engine tick, rather than sitting
// invisible until the next process restart's backfillScheduleNextRun pass.
//
// store.ValidateAgentSchedule owns the shared schedule rules for this hook,
// the HTTP producer, the self-tool producer, and direct store callers. It
// rejects action_spec shapes that would otherwise fail DB constraints or
// produce a semantically broken row before next_run is computed. This hook
// retains only translation of the reflex action_spec into the domain row.
//
// The shared validator uses cron.ParseStandard for a non-empty cron spec.
// This function used to rely on
// store.ComputeAgentScheduleNextRun's documented "fall back to due now"
// behavior for a malformed expression instead, but that fallback is only
// safe for the *next_run computation*, not for the row's ongoing life:
// go-scheduler's own tick() (libs/go-scheduler/engine.go) re-parses
// CronExpr on every tick and silently skips a row that fails to parse
// (bumping WorkerErrors, never reaching the CAS claim) -- so a bad cron
// expression that fell through to the "due now" fallback here would
// produce a schema-valid row that never fires again, with no signal beyond
// a coarse aggregate error counter. See TASKS/scheduling/
// 07-wire-add-schedule-reflex.md's Review notes and TASKS/ESCALATIONS.md's
// 2026-08-20 "Scheduling Phase 2 review: task 07's malformed-cron gap"
// entry for the full finding this fixes.
func buildReflexAgentSchedule(agentID string, spec map[string]interface{}, now time.Time) (store.AgentSchedule, error) {
	jobPayload, err := specJobPayload(spec)
	if err != nil {
		return store.AgentSchedule{}, err
	}

	row := store.AgentSchedule{
		ID:           "reflex-schedule-" + uuid.New().String(),
		AgentID:      agentID,
		SessionID:    specString(spec, "session_id"),
		Name:         specString(spec, "name"),
		ScheduleKind: specString(spec, "schedule_kind"),
		ScheduleSpec: specString(spec, "schedule_spec"),
		Body:         specString(spec, "body"),
		Priority:     specInt64(spec, "priority"),
		ExpiresAt:    specString(spec, "expires_at"),
		CreatedBy:    reflexScheduleSource,
		MaxRetries:   specInt64(spec, "max_retries"),
		OnFail:       specString(spec, "on_fail"),
		JobType:      specString(spec, "job_type"),
		JobPayload:   jobPayload,
	}
	if err := store.ValidateAgentSchedule(row); err != nil {
		return store.AgentSchedule{}, fmt.Errorf("action_spec: %w", err)
	}
	nextRun := store.ComputeAgentScheduleNextRun(row.ScheduleKind, row.ScheduleSpec, now)
	if nextRun.IsZero() {
		// Defensive only -- the shared validator already rejects every kind
		// ComputeAgentScheduleNextRun doesn't recognize, so this branch should
		// be unreachable in practice.
		return store.AgentSchedule{}, fmt.Errorf(
			"action_spec: could not compute next_run for schedule_kind=%q", row.ScheduleKind)
	}
	row.NextRun = nextRun.Format(time.RFC3339)
	return row, nil
}

// specString reads a string field out of an unmarshaled action_spec map,
// returning "" for a missing key or a non-string value (e.g. a reflex
// author accidentally writing a number where a string was expected) rather
// than panicking on a failed type assertion.
func specString(spec map[string]interface{}, key string) string {
	if v, ok := spec[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// specInt64 reads an integer field out of an unmarshaled action_spec map.
// encoding/json decodes every JSON number into a Go float64 by default (the
// map[string]interface{} shape Apply hands the hook), so float64 is the
// realistic case; the other branches are defensive for callers that
// construct the map directly (e.g. this file's own regression tests).
func specInt64(spec map[string]interface{}, key string) int64 {
	v, ok := spec[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

// specJobPayload extracts action_spec.job_payload as a JSON string ready
// for AgentSchedule.JobPayload -- see buildReflexAgentSchedule's doc
// comment for the wire contract this mirrors. Accepts either a nested JSON
// object (re-marshaled to a string) or an already-encoded JSON string
// (validated later by ValidateAgentSchedule, used as-is here). Omitted
// entirely (or explicit JSON null) returns "", which InsertAgentSchedule
// defaults to "{}".
func specJobPayload(spec map[string]interface{}) (string, error) {
	v, ok := spec["job_payload"]
	if !ok || v == nil {
		return "", nil
	}
	switch p := v.(type) {
	case string:
		return p, nil // ValidateAgentSchedule owns JSON validity.
	default:
		encoded, err := json.Marshal(p)
		if err != nil {
			return "", fmt.Errorf("action_spec.job_payload: %w", err)
		}
		return string(encoded), nil
	}
}
