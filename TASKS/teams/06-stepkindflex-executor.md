# Flex-step executor — pause/exit-trigger/resume, plus the two stress tests the design doc says need real answers

**Phase:** 2 — Runtime engine (`TASKS/teams`)
**Status:** not-started
**Depends on:** `03` (`StepKindFlex` schema/const must exist)
**Touches:** `internal/service/workflow_engine.go` (`BuiltinWorkflowEngine`'s step-dispatch logic — add real flex-step handling), `internal/agent/reflexes/evaluator.go` (reused read-only — confirm/export `EvaluateTrigger` if not already exported for cross-package use).

## Context

`docs/engineering/architecture/15-teams.md`'s Decision 2 section is explicit that a flex step reuses existing pause/resume plumbing, not new engine machinery: *"A flex step reuses that identical pause/external-resolve/`Resume` shape — it returns the waiting status when it starts, and something external (the exit trigger firing) is what calls `Resume`, not a step executor returning synchronously. The only new thing is *what happens during the wait* (active agents doing real work, not an idle pause) and *what resolves it* (an exit-trigger condition, not an operator approval)."*

**Confirmed directly against the real code** (this planning session's research): `RunStatusWaiting = "waiting_on_gate"` (`internal/agentworkflow/types.go:44` — note the literal string is `"waiting_on_gate"`, not generic "waiting"). `BuiltinWorkflowEngine.Resume(ctx context.Context, runID string, wf agentworkflow.WorkflowDefinition, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error)` (`internal/service/workflow_engine.go:98`) — the design doc's claimed signature matches exactly. `Resume` is **not part of the `WorkflowEngine` interface** — it's a `BuiltinWorkflowEngine`-specific method (its own doc comment: "external engines own their own resumption semantics"), called by concrete-type-aware callers only. The real, confirmed live caller today is `TaskManager.resumeWorkflowRun` (`internal/service/a2a_task_manager.go:702-703`, invoked from `:689` after an operator resolves an A2A gate task) — exactly matching the design doc's claim.

**Resume's actual reload logic** (`workflow_engine.go:98-144`): reloads the run row and persisted steps, buckets each by status (`waiting_on_gate` steps tracked separately; anything caught mid-`running` at crash time is **re-run, not resumed in place** — documented at-least-once semantics, `workflow_engine.go:89-95`). This matters directly for this task: whatever a flex step does on (re-)entry must be safe to run twice (idempotent with respect to `team_run_members`, task `02`'s table — re-entering an already-active flex step must not double-count or re-spawn members that already resolved).

**Two stress tests the design doc explicitly refuses to leave unanswered** — quoting its own "Validating this design" section: *"a flex step's exit trigger fires while other required members are still actively working (phase-closure race)... a message routes to a slot whose durable member is unavailable or whose fresh member fails to instantiate (routing-target resolution failure). The last two are likely the messiest runtime edges of the whole design... and deserve concrete answers before implementation starts, not just a passing mention."* This task owns the first (phase-closure race); task `09` owns the second (routing-target resolution failure).

## What to do

1. Add flex-step handling to whatever function actually dispatches per-`StepKind` execution in `workflow_engine.go` (confirm the exact function name — `execute(...)` or similar, per `03`'s own note about this switch). On first entry to a flex step:
   - Mark the step (and run) into a waiting state. **Do not silently reuse the literal `"waiting_on_gate"` status for a flex step without deciding this on purpose** — a flex step is not a gate, and any code branching on that exact string today (e.g. anything assuming "waiting_on_gate ⇒ needs an operator approval") could misbehave. Recommend introducing a distinct status (e.g. `"waiting_on_flex"`); document your call either way, and confirm every real caller of `Resume`/anything inspecting `RunStatusWaiting` handles whichever status you choose correctly.
   - Persist the flex step's `active_slots` (from its `Config`) durably enough that a process restart can re-derive who's active — cross-reference against `team_run_members` (task `02`) rather than inventing a second source of truth; document exactly how.

2. **Exit-trigger evaluation**: call `internal/agent/reflexes/evaluator.go`'s `EvaluateTrigger` (export it if not already exported for cross-package use) against the flex step's `exit_trigger` spec (reusing the reflex trigger-spec JSON verbatim, per the design doc's explicit instruction — do not build a second condition language) and current session state. Decide and document the evaluation cadence — recommend triggering re-evaluation on a relevant real event (e.g. a `message_send` or self-tool call from an active-slot member) rather than a blind interval poll, and explain why in the Work Log.

3. **Phase-closure race — concrete answer required, not deferred.** When the exit trigger fires, other `active_slots` members may still be mid-turn. Pick one, implement it, and cover it with a regression test using a fake/deterministic exit trigger plus a simulated in-flight second member:
   - (a) fire immediately — in-flight work from other members is abandoned/orphaned;
   - (b) fire immediately, but other active members keep running independently in their own sessions; their eventual output (if any) is routed as an ordinary message to whatever phase is active by the time they finish, rather than discarded;
   - (c) exit-trigger firing sets a "closing" flag; the phase truly ends only once no member is mid-turn, with a documented timeout/escape hatch so one stuck member can't block the run forever.
   Document your choice and reasoning explicitly in the Work Log — this is real design work the planning session deliberately left to the implementing worker, per the design doc's own instruction that this needs "concrete answers before implementation," not a default assumed silently.

4. **Exit-trigger authority default** (the design doc leaves this open: *"whether it's always `orchestrator`-only, `any(active_slots)`, a specific slot, or varies per flex step — the separation (trigger truth vs. permission to produce it) is decided; the default policy is not"*). Default recommendation: **any member of `active_slots` may satisfy a `self_tool`-kind exit trigger**, unless the Team definition's routing/authority config (task `04`) names a specific slot for that trigger — matches the flex step's own "fluid, self-organizing" framing. Document this as a provisional default explicitly revisitable once `04`'s verb set grows to cover it directly, and cover it with a test.

## Done means

- `go build`/`vet`/`test` clean.
- A test `WorkflowDefinition` containing a flex step correctly enters a waiting state, and correctly resumes via `Resume` once its exit trigger fires — driven through the real `BuiltinWorkflowEngine`, not a mock.
- The phase-closure-race default is implemented (not just documented) and covered by a named regression test simulating the race directly.
- The exit-trigger-authority default is implemented and covered by a test (an unauthorized slot attempting to satisfy the exit trigger is rejected or ignored, per your documented policy).
- A restart-mid-flex-step scenario (simulating the engine's real at-least-once re-run semantics) does not double-resolve or corrupt `team_run_members` state — covered by a test.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
