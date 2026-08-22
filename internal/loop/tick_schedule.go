package loop

// TASKS/loops/12-loop-run-tick-scheduled-trigger.md, item 5: "resolve where
// the actual agent_schedules row [with job_type=loop_run_tick] comes from
// ... if task 08/10 didn't already build this ... add the minimal insertion
// call yourself."
//
// Confirmed directly against the real, already-landed code before writing
// this file, not assumed: task 08's LoopEngine (engine.go, reviewed) never
// calls store.InsertAgentSchedule anywhere -- grepped the whole package.
// Task 10 (TASKS/loops/10-loop-launcher-and-api.md) is still not-started at
// the time this task was implemented (running concurrently in a separate,
// isolated worktree this task cannot see) -- its own "What to do" only
// names Launch/Cancel/ResolveEscalation, with no mention of an automatic
// agent_schedules producer either. So neither party builds this; this file
// is the minimal creator TASKS/loops/12's own item 5 requires rather than
// leaving loop_run_tick with no real producer anywhere in the codebase.
//
// Call site: evaluateDecideAndAct (engine.go), specifically when Decide
// (decide.go) returns DecisionWait -- not DecisionEscalate. This is the one
// real, unambiguous moment 21-loops.md's trigger-surface bullet describes
// verbatim: "a WAIT-status loop polling an external condition." ESCALATE is
// the other half of the same loop_runs.status bucket
// (LoopRunStatusWaitingOnEscalation -- see evaluateDecideAndAct's own doc
// comment on why WAIT/ESCALATE collapse into one column value) but means
// something structurally different: "needs a human," resolved via task 10's
// future ResolveEscalation endpoint or a direct Resume call, not an
// automatic re-poll. Scheduling a tick against an ESCALATE would be
// actively wrong (it would silently resume a LoopRun an operator hasn't
// actually looked at yet).
//
// Deliberately NOT gated on a "durable preset" marker, even though
// 21-loops.md's own trigger-surface bullet names two cases ("a
// 'durable'-preset loop OR a WAIT-status loop polling an external
// condition"): no preset registry or continuation-policy/budget field
// distinguishing "this WAIT is a durable-preset poll" from "this WAIT is
// some other kind of external wait" exists yet -- TASKS/loops/
// 13-loop-presets.md, the task that would define "durable," is still
// not-started as of this implementation, confirmed directly. Rather than
// invent preset infrastructure this task was never scoped to build, every
// DecisionWait is treated as the polling case. This is the conservative
// direction: a spurious extra tick against a WAIT that was never really
// meant to be auto-polled is a cheap no-op (RunnerAdapter.
// isResumableLoopRunStatus / enqueueLoopRunTick, internal/scheduler/
// runner_adapter.go, this same task); the opposite failure -- a
// durable-preset WAIT that never gets ticked because no marker matched --
// is exactly the "no real creator" gap item 5 was written to close. Real
// follow-up candidate for task 13: once a genuine polling-cadence field
// exists on ContinuationPolicy or Budget, narrow this gate and let the
// preset supply its own interval instead of defaultLoopRunTickPollInterval
// below.
//
// Deterministic schedule ID (loopRunTickScheduleID, not a fresh ID per
// call): store.InsertAgentSchedule is INSERT OR REPLACE keyed on id, so a
// LoopRun that cycles through WAIT more than once (an operator or reflex
// resumes it, the external condition still isn't met, Decide returns
// DecisionWait again) replaces its own single outstanding tick schedule
// rather than accumulating one row per WAIT episode.
//
// Known limitation, documented rather than solved here: this inserts
// exactly one one_shot tick per WAIT decision. If that tick fires, finds
// the LoopRun still not resumable for some external reason, and Resume
// itself doesn't re-enter a WAIT (e.g. it errors, or the condition truly
// never resolves), nothing re-schedules a second tick -- there is no
// self-rescheduling loop here. This matches the actual scope of this task's
// enqueueLoopRunTick (a single "check now" dispatch, not a recurring
// poller) and is left as a real follow-up candidate for task 13's preset
// work, which is the party best positioned to own a genuine recurring
// polling-cadence contract.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// defaultLoopRunTickPollInterval is a placeholder polling cadence -- no
// preset/continuation-policy/budget field carries a real one yet (see this
// file's own package doc comment). Five minutes is a deliberately coarse
// default: this is one dedicated re-check tick per waiting LoopRun, not a
// shared poller sweeping every due schedule in one pass, so there is no
// throughput reason to poll tighter than this by default.
const defaultLoopRunTickPollInterval = 5 * time.Minute

// loopRunTickScheduleID derives a deterministic agent_schedules.id for
// loopRunID's outstanding loop_run_tick -- see this file's own package doc
// comment for why determinism (vs. a fresh ID per call) matters here.
func loopRunTickScheduleID(loopRunID string) string {
	return "loop_run_tick:" + loopRunID
}

// scheduleLoopRunTick inserts (or replaces) the one_shot agent_schedules
// row TASKS/loops/12's loop_run_tick job type dispatches against loopRunID
// after defaultLoopRunTickPollInterval. See this file's own package doc
// comment for the call site (evaluateDecideAndAct's DecisionWait branch)
// and why every DecisionWait -- not just a "durable preset" subset --
// currently qualifies.
func (e *LoopEngine) scheduleLoopRunTick(ctx context.Context, loopRunID, agentProfileID string) error {
	if agentProfileID == "" {
		return fmt.Errorf("loop: schedule loop_run_tick for %s: agent_profile_id is required", loopRunID)
	}
	payload, err := jsonMarshalLoopRunTickPayload(loopRunID)
	if err != nil {
		return fmt.Errorf("loop: schedule loop_run_tick for %s: %w", loopRunID, err)
	}
	row := store.AgentSchedule{
		ID:           loopRunTickScheduleID(loopRunID),
		AgentID:      agentProfileID,
		Name:         "loop_run_tick",
		ScheduleKind: store.ScheduleKindOneShot,
		Body:         fmt.Sprintf("loop_run_tick poll for loop_run %s", loopRunID),
		NextRun:      time.Now().UTC().Add(defaultLoopRunTickPollInterval).Format(time.RFC3339),
		JobType:      store.ScheduleJobTypeLoopRunTick,
		JobPayload:   payload,
	}
	if err := e.store.InsertAgentSchedule(ctx, row); err != nil {
		return fmt.Errorf("loop: schedule loop_run_tick for %s: %w", loopRunID, err)
	}
	return nil
}

// jsonMarshalLoopRunTickPayload builds the exact JSON shape
// internal/scheduler.LoopRunTickPayload decodes -- {"loop_run_id": "..."}.
// A tiny hand-built encode against an anonymous struct (rather than
// importing internal/scheduler's own LoopRunTickPayload type) is
// deliberate: internal/scheduler already imports internal/loop (for
// LoopResumer, this same task), so internal/loop importing
// internal/scheduler back would be a cycle. Mirrors this package's own
// precedent for the same constraint (internal/store's
// ScheduleJobTypeLoopRunTick vs. internal/scheduler's JobTypeLoopRunTick --
// see agent_schedules.go's doc comment): two independently-maintained
// copies of one small, stable JSON contract rather than a shared import
// that isn't possible in either direction without inverting an existing
// one.
func jsonMarshalLoopRunTickPayload(loopRunID string) (string, error) {
	b, err := json.Marshal(struct {
		LoopRunID string `json:"loop_run_id"`
	}{LoopRunID: loopRunID})
	if err != nil {
		return "", fmt.Errorf("encode loop_run_tick payload: %w", err)
	}
	return string(b), nil
}
