# LoopEngine core — `internal/loop.LoopEngine.Run`/`.Resume`

**Phase:** 2 — Runtime engine (`TASKS/loops`)
**Status:** not-started
**Depends on:** `03-loop-runs-schema.md`, `04-loop-run-iterations-schema.md`,
`07-loop-continuation-policy.md`
**Touches:** new `internal/loop/engine.go`, `internal/service/workflow_launch.go`
(read-only reference — calling `WorkflowLauncher.Launch`/`GetEngine`, not modifying them),
`internal/store/loop_runs.go` / `loop_run_iterations.go` (calling the CRUD tasks `03`/`04`
built).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s `LoopEngine` — quoted directly:
*"Its only executor dependency is the existing `WorkflowLauncher` (`Launch`/`Resume`) —
`LoopEngine` does not get its own `StepExecutor`. For a bounded loop..., `Run` drives
iteration-to-iteration synchronously within one call, exactly as
`BuiltinWorkflowEngine.Run` already iterates over ready DAG nodes in one call. A `WAIT` or
`ESCALATE` decision persists `loop_runs.status` and returns — resumed later... mirroring
exactly how a gate-blocked `WorkflowRun` already works."*

```go
type LoopEngine interface {
    Run(ctx context.Context, def LoopDefinition, input LoopInput) (LoopResult, error)
    Resume(ctx context.Context, loopRunID string) (LoopResult, error)
}
```

**Direct structural precedent, confirmed this session** —
`internal/service/team_compiler.go:104`'s `CompileTeam(name string, phases []store.TeamPhase,
resolvedMembers map[string][]store.TeamRunMember) (agentworkflow.WorkflowDefinition, error)`
and `internal/service/team_run_launcher.go:156`'s `TeamRunLauncher` (constructed via
`NewTeamRunLauncher(st *store.Store, registry *agentworkflow.Registry, launcher
*WorkflowLauncher, durable DurableAgentService) *TeamRunLauncher`, driving entry point
`LaunchTeamRun` at line 226) are the real, already-shipped template for wrapping
`*WorkflowLauncher` as a field and driving it — `LoopEngine` follows the identical shape one
level up: a struct wrapping `*WorkflowLauncher` (not implementing `agentworkflow.WorkflowEngine`
itself — Decision 1 is explicit `LoopRun` is not a `WorkflowRun`).

**`WorkflowLauncher` has no direct `.Resume` method — a real correction to the design doc's
bare `Launch`/`Resume` phrasing, confirmed this session.** `WorkflowLauncher.Launch`
(`internal/service/workflow_launch.go:122`) is real; resuming goes through
`WorkflowLauncher.GetEngine(name)` (line 101) to get the concrete engine, then that engine's
own `.Resume(ctx, runID, wf, exec)` — `BuiltinWorkflowEngine.Resume` at
`internal/service/workflow_engine.go:125`. The one real caller precedent for this two-step
resume today is `internal/service/a2a_task_manager.go`'s `resumeWorkflowRun` (confirmed this
session, lines 708-762): re-fetches the `WorkflowRun` and its `WorkflowDefinition`, gets the
concrete `*BuiltinWorkflowEngine` off the launcher (type-asserted, since `.Resume` isn't on
the generic `agentworkflow.WorkflowEngine` interface — only gate/flex/loop-capable engines
have it), then calls `.Resume(ctx, runID, wf, launcher.GetStepExecutor())`. `LoopEngine.Resume`
follows this exact pattern for resuming a paused iteration's `WorkflowRun`, not a bare
`launcher.Resume(...)` call that doesn't exist.

## What to do

1. **`internal/loop/types.go`** — `LoopDefinition` (the iteration `WorkflowDefinition`
   name/version or preset name, per the design doc's `loop_runs.definition_name` field),
   `LoopInput` (the launch-time params: `GoalID` or inline goal spec, `Budget` overrides),
   `LoopResult` (terminal or paused state — `Status`, `LoopRunID`, `CurrentIteration`,
   `LastDecision`).

2. **`internal/loop/engine.go`** — `LoopEngine` struct wrapping `*service.WorkflowLauncher`
   (or whatever the real import path is at dispatch time), a `*store.Store`, and a reference
   to task `07`'s `Decide`. `Run(ctx, def, input)`:
   - Resolve/upsert the `Goal` (task `01`'s `CreateGoal`/`GetGoal`) per `LoopInput`.
   - **Enforce this planning session's one-active-`LoopRun`-per-`goal_id` decision here or
     in task `10`'s launcher** — pick one owner and document it; if both `Run` and the
     launcher could plausibly be called directly, the check belongs in this package (`Run`)
     so it's enforced regardless of caller, not just the HTTP path.
   - Create the `loop_runs` row (task `03`'s `CreateLoopRun`).
   - Loop: launch the next iteration's `WorkflowRun` via `WorkflowLauncher.Launch` (setting
     task `05`'s `loop_run_id`/`loop_iteration` on the created run — resolve task `05`'s own
     Work Log for the exact mechanism it built), create the corresponding
     `loop_run_iterations` row (task `04`), wait for that `WorkflowRun` to reach a terminal
     or waiting status, compute the iteration's `Evaluation` (rollup over that run's
     `VerifyResult`s — the design doc's own "reuses `Verify` wholesale" instruction; find
     wherever `workflow_run_steps.verify_json` is queryable and aggregate it, no new
     evaluator subsystem), call task `07`'s `Decide`, persist the decision + evaluation via
     `CompleteLoopRunIteration`, update `loop_runs.no_progress_streak` and
     `current_iteration`, then act on the decision: `CONTINUE`/`RETRY` → next loop
     iteration (same or adjusted definition); `REPLAN`/`REARCHITECT` → next iteration under
     a revised definition/goal; `WAIT`/`ESCALATE` → persist `loop_runs.status` and return
     (per the design doc's own instruction, quoted above); `COMPLETE`/`FAIL` → persist
     terminal status and return.
   - `Resume(ctx, loopRunID)`: re-fetch the `LoopRun`, and — mirroring `resumeWorkflowRun`'s
     shape — either resume a still-in-flight iteration's paused `WorkflowRun` (via
     `GetEngine`+concrete-engine `.Resume`, as described in Context) if that's why it's
     waiting, or re-enter the `Run` loop's iteration cycle if the pause was a `LoopRun`-level
     `WAIT`/`ESCALATE` now being cleared externally (task `10`'s escalation-resolution
     endpoint, or task `11`'s reflex-fired resume, or task `12`'s scheduled tick — all three
     call this same `Resume`).

3. **Bound the synchronous loop** — the design doc's "for a bounded loop... `Run` drives
   iteration-to-iteration synchronously within one call" is explicitly scoped to bounded
   cases (Ralph, test/fix). Guard against a caller accidentally blocking on an
   effectively-unbounded loop by respecting `budget.MaxRuntimeSeconds`/`MaxIterations` as a
   hard ceiling inside `Run` itself (not just as one of `Decide`'s deterministic checks) —
   document your own call on whether this is redundant with `Decide`'s own budget-exhausted
   branch or a genuine second guard (e.g. a context deadline derived from
   `MaxRuntimeSeconds`, independent of iteration-boundary checks).

## Done means

- `LoopEngine.Run` drives a real, multi-iteration loop end-to-end in a regression test
  against a trivial single-`llm`-step `WorkflowDefinition` (no real LLM call needed if the
  test uses a stub `StepExecutor`, matching whatever test-double convention
  `workflow_engine_test.go`/`team_run_launcher_test.go` already use) — asserting
  `loop_run_iterations` rows are created and completed in order, `loop_runs.current_iteration`
  and `no_progress_streak` update correctly, and a `COMPLETE` decision terminates the loop.
- A `WAIT`/`ESCALATE` decision persists status and returns without blocking, and a
  subsequent `Resume` call picks the loop back up correctly (tested).
- The one-active-`LoopRun`-per-`goal_id` rule is enforced and tested (a second `Run` call
  against a `goal_id` with an already-`running` `LoopRun` is rejected).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
