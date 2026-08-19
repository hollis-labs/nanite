# Fix: `dispatch_to_agent` reflexes are spuriously "fired" by the generic per-turn reflex pass

**Phase:** 4
**Status:** not-started
**Depends on:** `TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md`, `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md` (both already landed on `main` — this task fixes a real, confirmed gap the fresh Phase 4 Reviewer found in their combined result)
**Touches:** `internal/agent/reflexes/engine.go` (`Engine.EvaluateState`), possibly `internal/agent/reflexes/executor.go` (comment accuracy only — no behavior change expected there), `docs/engineering/architecture/03-steering.md` (stale doc line, see item 2 below)

## Context

Found by the fresh, independent Phase 4 Reviewer (no shared context with the workers who implemented tasks 02/03), reviewing the merged `87f54625..HEAD` diff, 2026-08-19. Full finding text is preserved in the Reviewer's report; this task file distills it into an actionable brief.

### The bug, traced end to end

`internal/service/chat_reflexes.go`'s `evaluateAndInjectReflexes` (unmodified by Phase 4, called unconditionally every turn at `internal/service/chat_generate.go:523`) calls `internal/agent/reflexes/engine.go`'s `Engine.EvaluateState`, which:

1. Lists reflexes via `Store.ListAgentReflexesForAgent(ctx, agentID, agentClass)` (`internal/store/agent_reflexes.go:218`) — **no `action_kind` filter**.
2. Iterates every active reflex regardless of kind, evaluates its trigger, and — if the trigger fires and it hasn't recently fired (15-minute debounce) — calls `e.Executor.Apply(ctx, r, state)`.
3. `Executor.Apply`'s `dispatch_to_agent` case (`internal/agent/reflexes/executor.go:106-123`) is an **explicit, documented no-op** — it returns `(applied, nil)` with zero error, by design, because a real dispatch needs a stream channel + `ToolService` that only `internal/service/chat_reflex_dispatch.go`'s dedicated `attemptReflexDispatch`/`matchDispatchToAgentReflex` call sites have (calling `Apply()` directly, bypassing this generic pass's debounce, per that file's own design note 2 — a routing decision needs fresh-every-turn evaluation, not a once-per-cooldown nudge).
4. Because `Apply` returns no error, `EvaluateState`'s loop (`engine.go:119-144`) treats this as a real, successful fire: it appends to `out.Actions`, calls `Store.BumpAgentReflexFired` (inflating `fired_count`/`last_fired_at`), and — if `e.Plugins != nil` — calls `EmitReflexFired`/`EmitReflexActionStaged`. Back in `evaluateAndInjectReflexes`, this also produces a redundant `event_log` row (`event_type="reflex_action"`, category `"reflex"`) alongside the real, richer `dispatch_to_agent` event `chat_reflex_dispatch.go`'s dedicated pass writes.

**This happens on the SAME turn as, and before, the real dispatch decision** — `evaluateAndInjectReflexes` runs at `chat_generate.go:523`; `attemptReflexDispatch` runs later at `chat_generate.go:670`. So for any turn matching a `dispatch_to_agent` reflex's trigger, both passes evaluate it: the generic pass no-ops but pollutes telemetry and fires plugin hooks; the dedicated pass performs the real dispatch (or falls back to chat-direct).

### Confirmed concretely reachable, not theoretical

`StateCollector.Collect` (`internal/agent/reflexes/state.go:22-92`) never sets `State.ScopeTier`/`State.ExecutionPattern` — so the reflexes carrying a `scope_tier`/`execution_pattern` guard (`dispatch_to_agent_open_subagent`, `dispatch_to_agent_background_long_task`, `dispatch_to_agent_planner_mention`, `dispatch_to_agent_planner_large_task`) can never fire through the generic pass; harmless there by accident, not by design. But **`dispatch_to_agent_researcher_mention`** and **`dispatch_to_agent_reviewer_mention`** (task 03's migrated seeds, `internal/agent/reflexes/seeds.go`) carry no tier/pattern guard — just a bare `user_regex_window` phrase predicate. `StateCollector.recentMessagesByRole` reads real, already-persisted user messages (`chat.go:663`'s `CreateMessage(userMsg)` runs before `generateResponse` is ever called), so **any message containing "review"/"research"/"investigate"/"audit"/"check this"/etc. — extremely common phrasing — spuriously fires the generic pass** on top of the real dedicated-pass dispatch.

### Why this matters (not a routing bug, but a real signal-integrity defect)

1. `fired_count`/`last_fired_at` telemetry is inflated/inaccurate for these two reflexes — contaminated by no-op firings from an unrelated pass, not reflecting real dispatch counts.
2. A redundant, semantically-thin `event_log` row is written alongside the real one — audit-trail noise (not fatal; an operator querying `event_type='dispatch_to_agent'` specifically still sees the correct signal).
3. **The real one**: `EmitReflexFired`/`EmitReflexActionStaged` — the plugin extension seam `docs/engineering/standards/patterns.md` names as the reusable interception mechanism — fire for a `dispatch_to_agent` reflex with **no corresponding dispatch having occurred**. A plugin subscribed to these hooks gets a false "this routing reflex fired" signal. This is exactly the silently-misleading-signal pattern `docs/engineering/standards/code-quality.md` flags as a real defect class, not a style nit.

This does **not** break the actual dispatch decision — the dedicated path doesn't consult `recentlyFired` and is unaffected. This is a telemetry/plugin-signal-integrity defect, not a "the router silently fails" defect.

## What to do

1. Exclude `action_kind = 'dispatch_to_agent'` reflex rows from `Engine.EvaluateState`'s generic per-turn evaluation loop entirely — this action kind is architecturally meant to be evaluated exclusively through the dedicated `attemptReflexDispatch`/`matchDispatchToAgentReflex` call sites, never through the generic pass. The cleanest shape (a judgment call, document what you pick and why): skip the row in the loop (a `continue` immediately after the `action_kind` is known to be `dispatch_to_agent`, before trigger evaluation, `fired_count` bump, or plugin hook emission) rather than filtering at the `Store.ListAgentReflexesForAgent` query level — that query is shared by the dedicated dispatch paths too, which DO need `dispatch_to_agent` rows returned; don't break those.
2. Confirm the fix doesn't regress the 5 other action kinds' generic-pass behavior (`inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule`) — they must continue to fire, debounce, bump `fired_count`, and emit plugin hooks exactly as before.
3. Verify the real dedicated dispatch paths (`chat_reflex_dispatch.go`'s `attemptReflexDispatch`, `self_tools_dispatch.go`'s `matchDispatchToAgentReflex`) are unaffected — they call `Store.ListAgentReflexesForAgent` and `Executor.Apply` directly, not through `EvaluateState`, so this fix should not touch their behavior at all. Confirm this by reading the call chain, not just assuming.
4. Add real test coverage for the exact scenario the Reviewer found no coverage for: a session with a seeded `dispatch_to_agent` reflex whose trigger fires, run through `Engine.EvaluateState` (or `evaluateAndInjectReflexes`) directly — confirm `fired_count` does NOT bump, no `event_log` row is written by this pass, and (if a fake `PluginHooks`/`Plugins` is wired in the test) `EmitReflexFired`/`EmitReflexActionStaged` are NOT called for that reflex.
5. Correct the stale line in `docs/engineering/architecture/03-steering.md`'s "Two correctness gaps carried into implementation" section — it still reads *"Tool concurrency-safety classification is currently pure name-heuristic (suffix/substring matching), not derived from declared tool metadata"*, which `TASKS/phase-4/07-tool-concurrency-safety-classification.md` already fully closed (replaced the heuristic entirely with `known_tools.concurrency_safe` declared metadata, fail-closed default). Update or remove this line so the doc doesn't claim a gap that's already closed.

## Done means

- `dispatch_to_agent` reflexes are provably invisible to the generic per-turn reflex pass (`Engine.EvaluateState`/`evaluateAndInjectReflexes`) — a new test proves no `fired_count` bump, no `event_log` write, and no plugin-hook emission from that pass for a firing `dispatch_to_agent` reflex.
- The 5 other action kinds' generic-pass behavior is unchanged — existing tests for `inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule` still pass without modification (or with only mechanical updates, not behavior changes).
- The dedicated dispatch paths (`attemptReflexDispatch`, `matchDispatchToAgentReflex`) are confirmed unaffected.
- `docs/engineering/architecture/03-steering.md`'s stale concurrency-safety-gap line is corrected.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Verified in a real session: trigger a message matching `dispatch_to_agent_researcher_mention` or `dispatch_to_agent_reviewer_mention`'s phrase list, confirm via `event_log`/reflex telemetry that only the real `dispatch_to_agent` event/fired_count bump occurs, not a duplicate/spurious one from the generic pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
