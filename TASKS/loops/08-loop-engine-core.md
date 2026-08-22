# LoopEngine core — `internal/loop.LoopEngine.Run`/`.Resume`

**Phase:** 2 — Runtime engine (`TASKS/loops`)
**Status:** reviewed
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

Built `internal/loop/types.go` (`LoopDefinition`, `LoopGoalSpec`, `LoopInput`, `LoopResult`)
and `internal/loop/engine.go` (`LoopEngine`, `NewLoopEngine`, `Run`, `Resume`, and their
private helpers), plus `internal/loop/engine_test.go`. Confirmed directly against the real
code (not the design doc's paraphrase) before writing anything: `CompileTeam`
(`internal/service/team_compiler.go:104`), `TeamRunLauncher`/`LaunchTeamRun`
(`internal/service/team_run_launcher.go:156,226`), `WorkflowLauncher.Launch`/`GetEngine`
(`internal/service/workflow_launch.go:101,122`), `BuiltinWorkflowEngine.Resume`
(`internal/service/workflow_engine.go:125`), and `a2a_task_manager.go`'s `resumeWorkflowRun`
(lines 708-762) — `WorkflowLauncher` really has no bare `.Resume`; the two-step
`GetEngine`+concrete-engine-`.Resume(ctx, runID, wf, exec)` pattern is exactly what
`resumeBlockedIteration` follows.

**Evidence-formula duplication escalation (`TASKS/ESCALATIONS.md`, 2026-08-21) — resolved via
option (a).** Changed `Decide`'s signature (`internal/loop/decide.go`) to take a precomputed
`goalMet bool` instead of `evidence []GoalEvidence`, and deleted `evidenceSatisfiesGoal` and
its `missingFromCoverage` helper from `internal/loop` entirely — the four-clause `goal_met`
formula now has exactly one implementation, `internal/store/goal_evidence.go`'s
`EvaluateGoalEvidence`/`EvidenceSatisfiesGoal`. `LoopEngine.evaluateDecideAndAct` (engine.go)
is the one caller: it queries `store.EvidenceSatisfiesGoal(ctx, goal.ID)` itself, every
iteration, and passes the bool straight into `Decide`. Judged option (a) practical, not
"impractical for a real reason" (the escalation's own bar for falling back to option (b)
instead) — nothing about `goalMet` needs a DB read `Decide` couldn't otherwise avoid, and the
caller already holds a live `*store.Store`. Updated `internal/loop/decide_test.go` to match:
removed `TestDecide_GoalMet_PartialEvidence_DoesNotComplete` and
`TestDecide_GoalMet_NoEvidenceAtAll_DoesNotComplete` (they tested evidence-coverage nuance that
moved entirely out of `Decide`'s scope and is already covered once, in
`internal/store/goal_evidence_test.go`'s `TestEvidenceSatisfiesGoal_*` tests), added
`TestDecide_GoalNotMet_DoesNotComplete`, and updated all 20 other call sites' `nil` evidence
arg to `false`. All 24 `decide_test.go` tests pass with the new signature.

**One-active-`LoopRun`-per-`goal_id` — enforced in `Run` itself**, per the task's own
"pick one owner" instruction: queries
`store.ListLoopRuns(ctx, LoopRunFilter{GoalID, Statuses: LoopRunActiveStatuses})` before
creating any row, returning `ErrLoopRunAlreadyActive` (wrapped, `errors.Is`-checkable) if a
`running`/`waiting_on_gate`/`waiting_on_escalation` row already exists for that goal. Enforced
regardless of caller, not just an HTTP path task 10 might add later.

**`LoopEngine` is a concrete struct, not an interface** — a documented deviation from
21-loops.md's bare illustrative sketch, following `TeamRunLauncher`'s own real precedent (a
struct wrapping its collaborators: `*store.Store`, `*agentworkflow.Registry`,
`*service.WorkflowLauncher`). Nothing in this task needs a second, swappable implementation.

**Real gap found and resolved: `Resume(ctx, loopRunID)` has no parameters, but relaunching a
further iteration hard-requires `AgentProfileID`.** `WorkflowLaunchRequest.AgentProfileID` is a
required, NOT-NULL-FK-backed field, and nothing in the existing schema (no migration in this
task's scope) gives `loop_runs` a place to persist launch-time params for a bare, no-argument
`Resume` to recover later. Resolved by finishing what task 07 explicitly left open:
`loop_runs.continuation_policy_json` was documented as "task 07 defines and consumes its real
shape without this table needing to be revisited" — task 07 only ever needed
`ContinuationPolicy`'s own four fields. This task defines the column's actual, full stored
shape as `loopRunPersistentConfig` (engine.go, unexported): `ContinuationPolicy` nested
unchanged (so task 07's own type/doc-comments stay accurate) plus `AgentProfileID`/
`ProjectID`/`ParentSessionID`/`TimeoutSeconds`/`WorkflowParams`. `Run` encodes this once at
launch; `Resume` decodes it to relaunch further iterations with zero new inputs. Documented at
length in engine.go's own package doc comment. Flagged as a legitimate follow-up candidate: a
future migration could give `loop_runs` its own dedicated launch-params column instead of
overloading this one.

**Real schema gap found: `loop_runs.status` (migration 141) has only two pause buckets
(`waiting_on_gate`, `waiting_on_escalation`), one fewer than `Decide`'s own `WAIT`/`ESCALATE`
distinction.** No migration in scope to add a third. Resolved both `DecisionWait` and
`DecisionEscalate` into `LoopRunStatusWaitingOnEscalation` — no information is actually lost,
since `loop_run_iterations.decision` (persisted every iteration) already carries the exact,
undegraded `wait`/`escalate` distinction for any downstream reader; only the coarser
`loop_runs.status` column collapses the two. `LoopRunStatusWaitingOnGate` is reserved
specifically for "this iteration's own contained WorkflowRun is itself blocked mid-run"
(`RunStatusWaiting`/`RunStatusWaitingOnFlex`), a structurally different pause than a
LoopRun-level continuation-policy verdict. Documented as a real follow-up candidate (a future
migration could add a distinct `waiting` status for `WAIT`, mirroring `RunStatusWaitingOnFlex`'s
own precedent for exactly this problem) — not fixed here since it is a schema change and this
task has none in scope.

**Iteration ordering: `Launch` happens before `CreateLoopRunIteration`, not after**, so the
in-flight row always has `WorkflowRunID` populated even before a decision exists — matches
21-loops.md's own illustrative `loop_run_iterations` example (iteration 3, `decision: null`,
already carries `workflow_run_id: wr_ghi`) and is what makes `resumeBlockedIteration` able to
find and resume a still-in-flight iteration's `WorkflowRun` at all. `CreateLoopRunIteration`'s
own doc comment already explicitly allows this ("a caller that already knows all three fields
up front ... may set them directly") — no store-layer change needed.

**Evaluation rollup (`classifyIterationProgress`)** reads `WorkflowLaunchResult.StepResults`
directly (each `StepResult` already carries its own decoded `VerifyResult`, populated by the
builtin engine from `workflow_run_steps.verify_json`) — no new DB query, no new evaluator
subsystem, per the design doc's "reuses `Verify` wholesale" instruction. A step that errored or
failed its `Verify` counts as a regression data point; a cleanly-completed run with zero such
data points (including the common case of no `Verify` configured at all, e.g. Ralph's plain
one-`llm`-step preset) is classified `PROGRESS` — documented as a deliberately minimal v1
heuristic, matching 21-loops.md's own "no-progress/convergence detection... minimal v1...
deeper heuristics deferred" framing. `GOAL_MET` is never returned by this function; it is
overlaid by `evaluateDecideAndAct` once it has the caller-supplied `goalMet` bool.

**Bounding the synchronous loop (item 3) — judged a genuine second guard, not redundant with
`Decide`'s own budget-exhausted branch, for `MaxRuntimeSeconds` specifically; NOT duplicated
for `MaxIterations`.** `driveIterations` derives a `context.WithTimeout` from
`budget.MaxRuntimeSeconds` (when set) wrapping every iteration's `Launch` call and `Decide`'s
own `ctx`. Reasoning: `Decide`'s own `budgetExhausted` check only ever fires *between*
iterations (after a `WorkflowRun` returns); each individual iteration is already separately
bounded by its own `WorkflowLaunchRequest.TimeoutSeconds` (defaulting to 1800s), but a `LoopRun`
with `MaxIterations` left at 0 ("no cap") and `MaxRuntimeSeconds` set could otherwise run an
unbounded *number* of individually-bounded iterations before the history-based check ever
fires. A wall-clock context deadline closes that gap and can also cut off a single
overrunning iteration sooner than its own `TimeoutSeconds` would (Go composes two nested
context deadlines to the earlier one). `MaxIterations`, by contrast, is NOT given a second
in-`Run` check: `Decide` already compares `len(history) >= budget.MaxIterations` against the
exact same history slice this file builds and hands it — a second copy of that identical
comparison would be true redundancy, no new information. A `LoopRun` with both
`MaxIterations==0` and `MaxRuntimeSeconds==0` is treated as an explicit, intentional
"run until COMPLETE/FAIL" configuration (matching `budgetExhausted`'s own "0 means no cap"
convention) — no hidden default iteration ceiling was invented to override that, since the
task's own wording asks to guard against an *accidental* unbounded loop, not to second-guess a
deliberate one. Known, documented limitation: a context deadline expiring *while* a `Launch` or
`Decide` reasoning-fallback LLM call is already in flight surfaces as a plain propagated error,
not a gracefully persisted `ESCALATE`/`FAIL` status — handling that race fully was judged real
additional complexity beyond what "Done means" requires (the tested, common path is the
between-iterations check).

**v1 scope, documented, not escalated:** `REPLAN` relaunches the same `WorkflowDefinition` as
`CONTINUE`/`RETRY` — 21-loops.md's own "What this session did not decide" leaves REPLAN's/
REARCHITECT's concrete "different approach" mechanism unresolved, and `Decide`'s `Decision`
type carries no alternate-definition payload for `REPLAN` (only `REARCHITECT` gets a
`Revision`, and that only ever touches the Goal's own `desired_state`/`acceptance_criteria`,
never a `WorkflowDefinition`). Not invented here; a future task that defines a real "different
approach" mechanism can extend `LoopDefinition`/`Decision` together.

**Tests** (`internal/loop/engine_test.go`, all against a real `t.TempDir()`-rooted SQLite
`*store.Store` + real `BuiltinWorkflowEngine`/`WorkflowLauncher`, only the leaf
`StepExecutor` stubbed — mirrors `workflow_engine_test.go`/`team_run_launcher_test.go`'s own
convention):
- `TestLoopEngine_Run_MultiIterationLoop_CompletesOnGoalMet` — 3 real iterations against a
  trivial single-`llm`-step `WorkflowDefinition`; the 3rd iteration's own step records the
  qualifying `goal_evidence` row as a side effect. Asserts `loop_run_iterations` rows created/
  completed in order (`continue, continue, complete`), each with a real `workflow_run_id` whose
  `workflow_runs` row carries the correct `loop_run_id`/`loop_iteration` (task 05's scoping),
  `loop_runs.current_iteration == 3`, `no_progress_streak == 0`, final status `completed`.
- `TestLoopEngine_Run_BudgetExhausted_EscalatesThenResumeContinues` — `Budget{MaxIterations:2}`
  drives 2 iterations to `ESCALATE`; asserts `Run` returns `waiting_on_escalation` without
  blocking, then `Resume` (called twice, proving idempotent re-entry) launches one real further
  iteration each time by recovering `AgentProfileID`/budget/policy purely from persisted state.
- `TestLoopEngine_Run_RejectsSecondActiveLoopRunForSameGoal` — a pre-existing `running` row
  rejects a second `Run` before any `WorkflowRun` launches (`exec.calls == 0`); also confirms
  `waiting_on_escalation` counts as active and `completed` does not.
- Two bonus tests: inline `LoopGoalSpec` upsert + budget-exhausted `FAIL` path, and the
  GoalID/Goal mutual-exclusivity error paths.

**Verification:** `go build ./cmd/nanite/` — pass (read the actual command output directly,
no build errors). `go vet ./...` — pre-existing, unrelated failures only
(`internal/service/container.go:1180/1200/1260`, `stopReaper`/`stopRuntimeReaper` possible
context leak; confirmed via `git diff --stat`/`git status` that this file is untouched by this
task — zero files besides `internal/loop/*` changed). `go test ./...` — every package printed
`ok`, including `internal/loop` (2.845s); zero `FAIL` lines anywhere in the full, un-piped
output, read directly from a file the test run was redirected to (not trusted via `$?` alone,
per this batch's own standing instruction).

## Review-found bug fix (2026-08-21)

A review of this task, prior to marking it `reviewed`, found a real correctness bug in
`driveIterations`'s loop-top check (`internal/loop/engine.go`): the original code read bare
`if runCtx.Err() != nil { return e.escalateOnBoundedContextExceeded(ctx, lr, budget) }`.
`runCtx.Err() != nil` cannot distinguish (1) the genuine `budget.MaxRuntimeSeconds`-derived
deadline elapsing (the case this check exists to guard against) from (2) the caller-supplied
`ctx` itself dying for a reason unrelated to this LoopRun's own budget (an HTTP handler's
`r.Context()` on client disconnect, a reverse-proxy timeout, a graceful-shutdown cancellation).
Both collapsed into the same "budget exhausted" diagnosis. This was worst when
`budget.MaxRuntimeSeconds == 0` ("no cap", this file's own documented "run until
COMPLETE/FAIL" configuration): `runCtx := ctx` is then a literal alias, not a derived child
context, so the check fired purely off the caller's own context lifecycle, silently violating
the file's own "no hidden ceiling on an unbounded loop" promise.

**Fix:** `driveIterations`'s loop-top check now checks `ctx.Err() != nil` first (the caller's
own context) and, if so, returns a plain propagated error without calling
`e.store.UpdateLoopRunStatus` at all — consistent with the file's pre-existing "Known
limitation" doc comment, which already accepted a context deadline expiring mid-Launch/
mid-Decide as a plain error rather than a persisted status; this extends the same treatment to
the loop-top check. Only once `ctx.Err()` is confirmed nil does a non-nil `runCtx.Err()`
unambiguously mean the derived `MaxRuntimeSeconds` timeout genuinely fired, correctly still
calling `escalateOnBoundedContextExceeded`. Documented at length in `engine.go`'s own
package-level doc comment (new "Review fix" section) and inline above the check itself.

**A real nuance found while building the regression test, also documented in `engine.go`:**
with `ctx` already fully dead, `escalateOnBoundedContextExceeded`'s own `UpdateLoopRunStatus(ctx,
...)` call is handed that same dead `ctx` and (confirmed directly against this store's real
driver, `modernc.org/sqlite`: an already-canceled/expired `ctx` makes `ExecContext`/
`QueryRowContext` fail immediately) also fails — so in this specific store implementation,
`loop_runs.status` often ends up unchanged either way, pre-fix or post-fix, once `ctx` is fully
dead by the time the check runs. This does NOT make the pre-fix bug harmless: (1) that outcome
is an incidental property of one DB driver's own ctx-checking, not something the loop engine's
own control flow guarantees — not a safety net to rely on; (2) the pre-fix code's returned error
still misdiagnoses the cause (it literally reads "max_runtime_seconds exceeded" even when the
real cause was the caller's own context dying), which matters for logs/monitoring/alerting even
when the erroneous write itself happens to fail closed. The regression test below asserts on
this error-message diagnosis specifically, since it is what actually, deterministically differs
between the pre-fix and post-fix code in this exact scenario — the persisted-status assertion
alone was empirically confirmed (by temporarily reverting the fix and re-running the test) to
NOT discriminate the two, for the reason just given.

**Regression test:** `TestLoopEngine_DriveIterations_CallerContextDead_DoesNotEscalateOrFailBudget`
(`internal/loop/engine_test.go`), two subtests (`MaxRuntimeSecondsUnset_CallerContextCanceled`,
`MaxRuntimeSecondsSetLarger_CallerContextDeadlineExceeded`) covering both the aliased
(`MaxRuntimeSeconds == 0`) and derived-child (`MaxRuntimeSeconds` set larger than the parent's
own already-expired deadline) cases the task asked for. **Deviation from the task's literal test
wording, confirmed and logged, not escalated:** the task said "passes an already-canceled ...
parent ctx into `Run`" — confirmed directly that this is not actually reachable as a meaningful
regression test: `Run`'s own setup (`resolveGoal`→`GetGoal`/`CreateGoal`, `ListLoopRuns`,
`CreateLoopRun`) forwards that same `ctx` straight into real `ExecContext`/`QueryRowContext`
calls, which fail immediately given an already-dead `ctx` (confirmed via a scratch experiment
against the real `modernc.org/sqlite` driver) — so an already-dead `ctx` handed to `Run` surfaces
as a plain setup-phase error before ever reaching `driveIterations`, exercising nothing the fix
touches (the pre-fix code behaves identically in that scenario — a false-negative-prone test). A
wall-clock short-deadline alternative that lets `Run`'s setup complete and then expires was also
considered and rejected as unreliable: an iteration cycle has roughly half a dozen other
ctx-consuming DB checkpoints besides the loop-top check itself, so a real timer is not
statistically favored to land exactly on the loop-top check rather than mid-iteration — it would
not reliably catch the bug on every run (confirmed by design, not just asserted). The test
instead calls `driveIterations` directly (accessible since `engine_test.go` is `package loop`,
not `loop_test`) with the Goal/LoopRun rows constructed directly via the store first — mirroring
this same test file's own pre-existing precedent in
`TestLoopEngine_Run_RejectsSecondActiveLoopRunForSameGoal`, which already constructs a
`store.LoopRun` by hand rather than through `Run`. This is deterministic (nothing before the
loop-top check touches `ctx`, so it is the first and only thing that can observe the death) and
directly exercises the exact fixed code path, whereas a literal `Run`-level test would not.
Verified the test is a genuine regression test, not a vacuously-passing one: temporarily reverted
the fix, re-ran the test, confirmed both subtests fail with the pre-fix code's misdiagnosing
error message (`"...max_runtime_seconds exceeded, and updating loop_run status failed:
...context canceled"` / `...context deadline exceeded`); restored the fix, re-ran, confirmed both
pass.

**Verification (all read directly from un-piped, file-redirected output, exit codes checked
immediately, per this batch's standing instruction):** `go build ./cmd/nanite/` — exit 0, no
output. `go vet ./...` — same four pre-existing, unrelated failures as before this fix
(`internal/service/container.go:1180/1200/1260`, `stopReaper`/`stopRuntimeReaper` possible
context leak; `git status`/`git diff --stat` confirm only `internal/loop/engine.go` and
`internal/loop/engine_test.go` changed by this fix). `go test ./...` — exit 0, every package
`ok` or `[no test files]`, zero `FAIL` lines anywhere in the full output, including
`ok github.com/hollis-labs/nanite/internal/loop 2.182s` (freshly run, not cached).

## Review notes

The prior reviewer's substantive findings on the rest of this task stand (evidence-formula
duplication resolution, `loopRunPersistentConfig`, one-active-LoopRun-per-goal enforcement,
WAIT/ESCALATE status collapse, `resumeBlockedIteration`'s in-flight detection, `MaxIterations`
non-duplication, REPLAN v1 scope, `classifyIterationProgress`) — that was the sole real
blocker, everything else already checked out.

This pass specifically re-confirmed the context-cancellation/budget-exhaustion conflation fix
in `driveIterations` is correct: `ctx.Err()` is checked before `runCtx.Err()`, and the
caller-context-death branch returns a plain error without any `UpdateLoopRunStatus` call —
only once `ctx.Err()` is confirmed nil does a non-nil `runCtx.Err()` unambiguously mean the
derived `MaxRuntimeSeconds` timeout genuinely fired. The new regression test
(`TestLoopEngine_DriveIterations_CallerContextDead_DoesNotEscalateOrFailBudget`) is genuine,
not vacuous — it asserts on the actual persisted `loop_runs.status` via a fresh `GetLoopRun`
call (confirmed `LoopRunStatusRunning`, unchanged), not just the return value, plus zero
iterations launched and no terminal timestamp set. Diff scope confirmed exactly 2 hunks in
`engine.go` (doc comment + the fix) and a pure addition in `engine_test.go`.

`go build ./cmd/nanite/`, `go vet ./internal/loop/...`, and `go test -count=1 ./...` all
green with real, unmasked exit codes (repo-wide `go vet ./...` has the same 4 pre-existing,
unrelated `internal/service/container.go` findings, confirmed untouched by this task).
