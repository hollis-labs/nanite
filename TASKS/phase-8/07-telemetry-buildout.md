# Telemetry buildout — close the gaps found by the 2026-08-19 coverage audit

**Phase:** 8 (Test, Review, Verify)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/service/chat_loop_terminated.go`, `internal/inspector/service.go`, `internal/subagent/service.go`, `internal/store/agent_runtime.go` (+ migration), `internal/store/migrations/001_schema.sql`'s `execution_metrics` table (+ migration for a new column).

## Context

Prompted by a separate architecture-review agent's suggestion to confirm what telemetry exists on turns/tool-calls/retries/reflex-fires before stress-testing concurrency — "otherwise the stress test tells you it fell over without telling you why." A dedicated read-only audit (`research-auditor`, 2026-08-19) covered agent launching, turns/chat-loop, tool calls, reflexes, steering/recovery, and subagent spawn/dispatch. Full findings below are from that audit; file:line references were spot-checked, not independently re-verified line-by-line, except where noted.

## What the audit found (summary, by area)

1. **Agent launching/boot** — good. `agent_runtime` table (`internal/store/migrations/050_agent_runtime.sql:29-45`) persists id/mode/state/pid/parent_session_id/failure_reason/timestamps, indexed. Gap: no boot duration/latency column, no time-series count of concurrent `launching` states (only a current snapshot).
2. **Turns/chat loop** — mixed. `execution_metrics` persists duration/tokens/tool_iterations/tool_calls/`stop_reason` per turn, but `stop_reason` is the raw provider value, not the internal `TerminationCode` (hardCeiling/runaway/etc.). **The internal termination reason is explicitly not persisted** — `internal/service/chat_loop_terminated.go:35-39` says so directly: "We intentionally do not persist the envelope... chat-loop-terminated is a stream-only signal." A stress test cannot query how many turns hit the runaway circuit-breaker after the fact, only grep logs.
3. **Tool calls** — split. `event_log` rows (`tool_call`/`tool_error`/`tool_truncated`) are persisted and per-session-queryable but carry no structured latency. The richer structured record (name/args/result/is_error/latency_ms) goes to `internal/inspector` — an **in-memory ring buffer**, 50 entries per session, explicitly documented as acceptable dev-mode data loss, lost on restart.
4. **Reflexes** — split fidelity. `dispatch_to_agent` matches get a real, queryable log (`playbook_match_log` / `Store.LogReflexMatch`). The other five action kinds only get generic `event_log` rows (`reflex_action`/`reflex_eval_error`). `fired_count`/`last_fired_at` on the `agent_reflexes` row are cumulative across all sessions, not per-session — and no condition-evaluation trace (why a trigger did/didn't fire) is persisted for the generic pass.
5. **Steering/recovery** — best-instrumented area already. `nanite_recovery_breadcrumbs` and `durable_agent_events` are both real, indexed, queryable tables with good coverage. No action needed here.
6. **Subagent spawn/dispatch** — partial. Recursion-depth rejection is logged (`event_log` type `subagent_recursion_blocked`, `internal/subagent/service.go:687-701`). **Fan-out cap hits (`ErrSpawnFanoutCapReached`) have zero telemetry** — `acquireSpawnSlot` returns the error silently, no slog/LogEvent/breadcrumb anywhere in the call path. (The audit also surfaced that a claimed second cap, `MaxConcurrentLongLived`/`ErrLongLivedCapReached`, doesn't exist in code at all despite Torque task `CW-20260512-0055` marking it done — flagged separately in Torque, not part of this task's scope.)

## What to do

Prioritized by the audit's own ranking (most valuable for the upcoming concurrency stress-test and reflex work first):

1. **Persist chat-loop termination codes.** `chat_loop_terminated.go`'s envelope is stream-only by design; add a persisted record (new `event_log` type, or a new column on `execution_metrics`) capturing the internal `TerminationCode` (hardCeiling/runaway/end_turn/etc.), not just the provider's raw `stop_reason`. This is the single highest-value gap — without it, a stress test can't count runaway-breaker trips after the fact.
2. **Log subagent fan-out cap hits.** `acquireSpawnSlot`'s `ErrSpawnFanoutCapReached` path currently has zero telemetry. Add a `LogEvent` call analogous to the existing `subagent_recursion_blocked` one at `service.go:687-701` — this is the direct saturation signal a concurrency stress test needs.
3. **Persist tool-call latency**, at least at the `event_log` level (the `inspector` ring buffer's richer per-call record is fine to keep as a dev-mode/live-debugging tool, but it shouldn't be the *only* place latency lives — it's lost on every restart).
4. **Add a duration/latency column to `agent_runtime`** so boot-time-under-load is queryable, not just current lifecycle state.
5. **Reconcile `execution_metrics.stop_reason` with the internal `TerminationCode`** (folds into item 1 if done together) so provider-level and harness-level termination reasons aren't conflated in the one durable per-turn table.

Items 4 and 5 are lower-value than 1-3 per the audit's own ranking — land 1-3 first if this gets split across multiple passes.

## Out of scope

- Reflexes' per-action-kind telemetry depth (items 4/5 above) — real gap, but `TASKS/phase-4/10-reflex-architecture-review.md` is the better place to decide reflex telemetry shape, since it's tied up with the precedence/priority-tier design question there. Don't duplicate that decision here.
- Steering/recovery (`nanite_recovery_breadcrumbs`, `durable_agent_events`) — already well-instrumented, no action needed.
- Fixing `CW-20260512-0055`'s stale "done" status (the `MaxConcurrentLongLived` cap that doesn't exist) — that's a Torque bookkeeping question, flagged there directly, not this task's job.

## Done means

- All five prioritized items above are implemented, or explicitly deferred with a filed follow-up and stated reason.
- A stress test run after this lands can answer, from persisted data alone (no log-grepping): how many turns hit each termination path, how many subagent spawns hit the fan-out cap, and per-tool-call latency for at least the `event_log`-visible window.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
