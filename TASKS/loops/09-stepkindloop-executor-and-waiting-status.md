# `StepKindLoop` executor + `RunStatusWaitingOnLoop`

**Phase:** 2 — Runtime engine (`TASKS/loops`)
**Status:** reviewed
**Depends on:** `06-stepkindloop-schema.md`, `08-loop-engine-core.md`
**Touches:** `internal/store/migrations/` (new migration — widens `status` CHECK, see
below), `internal/agentworkflow/types.go` (new `RunStatus` constant), `internal/service/workflow_engine.go`
(`flexOrGateWaitingStatus`-equivalent precedence function — extend, don't replace),
`internal/service/a2a_task_manager.go` (`deriveFromWorkflowRun` — new switch arm), a new
step-executor file for `StepKindLoop` (mirrors wherever `StepKindFlex`'s executor lives,
likely `internal/service/workflow_engine_flex.go`'s sibling).

## Context

Implements the runtime half of `StepKindLoop` — the "Workflow-contains-Loop" ledger item.
Design doc, quoted exactly: *"a `StepKindLoop` step returns a waiting status when it starts,
and something external (the contained `LoopRun` reaching a terminal state) is what calls
`Resume` on the *outer* workflow... a workflow paused on a contained loop needs its own
distinct status: `RunStatusWaitingOnLoop`. A loop is not a gate and is not a flex phase — an
outer consumer needs to tell all three apart. If a run is blocked by more than one kind at
once, gate still takes priority..., then flex, then loop — the least
human-attention-demanding kind loses the tie-break."*

**Real precedence function to extend, confirmed this session** —
`internal/service/workflow_engine.go:362`:
```go
func flexOrGateWaitingStatus(byID map[string]agentworkflow.StepDefinition, waiting map[string]bool) agentworkflow.RunStatus {
	for stepID := range waiting {
		if byID[stepID].Kind != agentworkflow.StepKindFlex {
			return agentworkflow.RunStatusWaiting
		}
	}
	return agentworkflow.RunStatusWaitingOnFlex
}
```
called from `finishRun` (line 336). This must become a three-way precedence (gate > flex >
loop) — rename or add a case, your call, but the tie-break order above is a required
correctness property, not a style preference, and needs a direct unit test covering all
three single-kind cases plus at least one mixed case (a run waiting on both a flex step and
a loop step simultaneously must resolve to `waiting_on_flex`, not `waiting_on_loop`).

**Real A2A status-derivation switch to extend, confirmed this session** —
`internal/service/a2a_task_manager.go:489-521`'s `deriveFromWorkflowRun` maps
`workflow_runs.status` → `a2a.TaskState`: today `"running","waiting_on_flex"` → `Working`;
`"waiting_on_gate"` → `InputRequired`; terminal statuses → terminal. **Confirmed this
session: there is no `"waiting_on_loop"` case yet — a bare switch with no default-loop
handling.** Add one. Whether `waiting_on_loop` maps to `Working` (like flex — an internal
mechanism working, not blocked on a human) or `InputRequired` (like gate) is a real design
call for this task: per the design doc's own framing, a loop is "the least
human-attention-demanding kind" of the three waiting states in the normal `WAIT` case, but
a `LoopRun` that itself escalated (`loop_runs.status = 'waiting_on_escalation'`) genuinely
does need human attention. Recommend: `waiting_on_loop` → `Working` by default, and task
`10`'s escalation surface is a *separate*, explicit signal (surfaced via the `LoopRun`'s own
status, not overloaded onto the outer `WorkflowRun`'s A2A state) — document whichever call
you make.

**Real trigger-fire mechanism, corrected this session — read before writing the
resume-on-terminal wiring.** The design doc's own phrasing ("something external — the
contained `LoopRun` reaching a terminal state — is what calls `Resume` on the outer
workflow") implies a push. Confirmed this session (`internal/service/workflow_engine.go:150-169`'s
own comment on the flex-step precedent this design explicitly reuses): flex's own
"external" trigger is actually a **lazy re-check piggybacked on whatever unrelated caller
next calls `.Resume()`** for the *outer* run — nothing in the current codebase pushes a
`Resume` call purely because an inner condition changed. For `StepKindLoop`, this task must
build the real push: when `LoopEngine.Run`/`.Resume` (task `08`) drives a `LoopRun` to a
terminal state (`completed`/`failed`/`cancelled`), it must itself call `.Resume` on the
*outer* `WorkflowRun` that's waiting on it (found via `workflow_run_steps` — the specific
step row in `waiting`/`RunStatusWaitingOnLoop` state whose config references this
`loop_run_id`) — a direct function call from `internal/loop` back into
`internal/service`'s resume path (the same `GetEngine`+concrete-engine-`.Resume` two-step
task `08`'s Context documents), not a lazy re-check. Confirm the calling direction (does
`internal/loop` import `internal/service`, or does this need a callback/interface passed
into `LoopEngine` to avoid an import cycle — `internal/service` likely already imports
`internal/loop` for task `08`'s own wiring, so a direct `internal/loop → internal/service`
import would cycle) and resolve it — likely: `LoopEngine` takes a small
`OuterResumeNotifier` interface (one method, `NotifyLoopRunTerminal(ctx, loopRunID string)
error`) as a constructor dependency, implemented in `internal/service` and wired at
container-build time, avoiding the cycle. Document your actual resolution.

## What to do

1. **New migration** — widen `workflow_runs.status` and `workflow_run_steps.status` CHECKs
   to add `'waiting_on_loop'`, mirroring `133_workflow_run_flex_waiting_status.sql`'s exact
   rebuild pattern (both tables, one migration — confirmed this session that `133` already
   established doing both together as the norm). Re-verify the actual next-available
   migration number at dispatch time; this task provisionally claims `144`.

2. **`RunStatusWaitingOnLoop` constant** — `internal/agentworkflow/types.go`, alongside
   `RunStatusWaitingOnFlex`.

3. **Extend the three-way precedence function** (`workflow_engine.go`'s
   `flexOrGateWaitingStatus`) — gate > flex > loop, tested per Context above.

4. **`StepKindLoop` step executor** — when a `StepKindLoop` step is reached: resolve its
   config (which `LoopDefinition`/preset, how outer-workflow params map to the inner Loop's
   `goal_id`/overrides — mirror `flexStepConfig`'s shape from Teams for the config-schema
   discipline, even though the fields differ), call `LoopEngine.Run` (task `08`) to launch
   the contained loop, record the `loop_run_id` on the step (a new column or JSON field on
   `workflow_run_steps` — your call, document it), and return the waiting status. Wire the
   `OuterResumeNotifier` (or whatever resolution you picked in Context) so a terminal
   `LoopRun` triggers the outer `.Resume` for real.

5. **Extend `deriveFromWorkflowRun`** — add the `waiting_on_loop` arm per the Context
   discussion, with your documented mapping choice.

## Done means

- Migration applies cleanly against a real backup with existing `flex`/`gate` waiting rows
  intact.
- Three-way precedence tested (gate-only, flex-only, loop-only, gate+flex, gate+loop,
  flex+loop, all three — 7 cases minimum).
- **End-to-end integration test, not just unit tests**: a `WorkflowDefinition` with a
  `StepKindLoop` step whose contained `LoopDefinition` is a trivial 1-2-iteration bounded
  loop reaches `RunStatusWaitingOnLoop` when the step starts, and the outer `WorkflowRun`
  genuinely transitions out of that status (via the real push mechanism built in item 4, not
  a manually-triggered test-only `.Resume()` call standing in for it) once the contained
  `LoopRun` completes. This is the "phase-closure race" stress test analog Teams' own
  `06-stepkindflex-executor.md` was required to cover for flex — treat it with the same
  weight, not a passing mention.
- `deriveFromWorkflowRun` returns the documented `a2a.TaskState` for a
  `waiting_on_loop`-status run, tested.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

**Migration number.** Used `141` exactly as the dispatch instruction required (confirmed
`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -3` showed `140_workflow_runs_loop_scoping.sql`
as the highest landed migration; no `141_*` existed yet). File:
`internal/store/migrations/141_workflow_run_waiting_on_loop_status.sql`. Widens
`workflow_runs.status` and `workflow_run_steps.status` CHECKs to add `'waiting_on_loop'`
(mirroring 133's exact dual-table rebuild pattern, in one migration as 133 established), and —
bundled into the same rebuild since `workflow_run_steps` was already being rebuilt for the
CHECK widening — adds a new nullable `workflow_run_steps.loop_run_id TEXT REFERENCES
loop_runs(id)` column (this task's own "your call" resolution to item 4's "where does the
loop_run_id get recorded on the step" question: a real, indexable column, not a JSON field,
since it's looked up in both directions — given a step, read its loop_run_id; given a
terminal loop_run_id, find the step waiting on it, `GetWorkflowRunStepByLoopRunID`). Verified
against a real production backup copy
(`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`,
copied into a scratch path first, per EXECUTION-PROCESS.md): that backup predates migration 130
entirely (zero gate/flex waiting rows of its own — same finding migration 136's own test
already documented), so the "existing flex/gate waiting rows survive" requirement was verified
the same way migration 136's test precedent handles this: `goose DownTo(140)`, insert synthetic
`waiting_on_gate`/`waiting_on_flex` rows via raw SQL (not the Go helpers — see below), then
`Up()` to replay through 141, confirming every real pre-existing row plus both synthetic rows
survive unchanged, and that `waiting_on_loop`/`loop_run_id` are now usable. Test:
`internal/store/migration_141_workflow_run_waiting_on_loop_status_test.go`.

**Real gap found and fixed in already-landed schema-task scope, not just this task's own
work.** `agentworkflow.Validate` (`internal/agentworkflow/validate.go`) still rejected
`StepKindLoop` in its step-kind switch — task 06 ("schema/const half") added the DB-level
`kind='loop'` CHECK widening (migration 136) and the `StepKindLoop` constant, but never updated
`Validate`'s own switch, so a `WorkflowDefinition` with a loop step would have failed
`agentworkflow.Validate(wf)` before ever reaching the engine, making `StepKindLoop` completely
unusable regardless of how correct this task's own runtime logic is. Fixed by adding
`StepKindLoop` to `Validate`'s accepted-kinds switch — a one-line, unambiguous correction (not
a design question), noted here per this project's "note the correction and do the task anyway"
policy rather than treated as a blocker.

**Import-cycle resolution — verified, then corrected the task file's own stated reasoning
while keeping its recommended shape.** Confirmed directly (`grep` on both packages' imports):
`internal/loop` already imports `internal/service` (task 08's `WorkflowLauncher` dependency) —
**not** the reverse, which is what the task file's own parenthetical guessed ("internal/service
likely already imports internal/loop"). This flips which direction is actually the cycle risk:
- The **notify** direction (`internal/loop` calling back into `internal/service` to resume an
  outer run) has **zero** cycle risk — `internal/loop` already imports `internal/service`, so a
  concrete type reference there would compile fine regardless. Built it as an interface anyway
  (`OuterResumeNotifier`, declared in `internal/loop/outer_resume.go`, the consumer) purely for
  the same narrow-dependency-footprint reason `TeamMembershipStore`/`FlexStepStateCollector`
  exist in `workflow_engine_flex.go`, not because of a cycle. Implemented by
  `service.LoopResumeNotifier` (`internal/service/workflow_engine_loop.go`), wired at
  container-build time (`cmd/nanite/main.go`).
- The **launch** direction (`internal/service`'s new `StepKindLoop` step executor needing to
  call `LoopEngine.Run`) is the *genuine* cycle risk: `internal/service` importing
  `internal/loop` for `LoopEngine`/`LoopDefinition`/`LoopInput`/`LoopResult` types would create
  `internal/service -> internal/loop -> internal/service`, given `internal/loop`'s existing
  import. Fixed with a `LoopStepLauncher` interface declared in `internal/service`
  (`workflow_engine_loop.go`), speaking only `internal/service`-native request/result types
  (`LoopStepLaunchRequest`/`LoopStepLaunchResult` — no `internal/loop` type ever appears in that
  file's exported surface), satisfied structurally by a `LaunchLoop` method added directly onto
  `*loop.LoopEngine` (`internal/loop/step_launcher.go` — legal since `internal/loop` already
  imports `internal/service`, so that method can freely reference those types).
- The task file's *recommended shape* (a small notifier interface, implemented in
  `internal/service`, wired at container-build time) was correct and is exactly what got built
  — only the stated reasoning for *why* was backwards, and the *other* direction (launch) turned
  out to be the one that actually needed the interface-based fix.

**A2A status mapping.** `waiting_on_loop` → `a2a.TaskStateWorking` (`deriveFromWorkflowRun`,
`internal/service/a2a_task_manager.go`) — the task file's own recommended default, taken as-is:
a loop-waiting run is a contained `LoopRun` making progress toward its goal, not blocked on a
human. A `LoopRun` that itself escalates (`loop_runs.status = 'waiting_on_escalation'`) is
deliberately *not* surfaced through this outer `WorkflowRun`'s A2A state — that's a separate
signal on the `LoopRun`'s own status, left for whichever future task builds a `LoopRun`
escalation surface (task 10+). Tested: `TestTaskManager_deriveFromWorkflowRun`'s new
`waiting_on_loop` case (`internal/service/a2a_task_manager_test.go`).

**Three-way precedence.** `flexOrGateWaitingStatus` renamed to `waitingRunStatus`
(`internal/service/workflow_engine.go`) and extended to gate > flex > loop: any gate present
wins immediately; else any flex present wins over loop; else (only loop steps waiting) reports
`waiting_on_loop`. Tested with all 7 required cases (gate-only, flex-only, loop-only, gate+flex,
gate+loop, flex+loop, all-three) in `TestWaitingRunStatus_Precedence`
(`internal/service/workflow_engine_loop_test.go`) — the mixed gate+loop / flex+loop / all-three
cases confirm gate wins over both, and flex wins over loop specifically (per the task's own
called-out requirement).

**Config-schema shape for a `StepKindLoop` step (item 4, "your call, document it").** Full
schema documented on `parseLoopStepConfig`'s own doc comment
(`internal/service/workflow_engine_loop.go`): `workflow_name`/`agent_profile_id` required;
exactly one of `goal_id` or an inline `goal: {...}` block (mirrors `LoopInput`'s own
GoalID/Goal union rule); optional `budget`/`continuation_policy` sub-objects mirroring
`store.Budget`/`loop.ContinuationPolicy`'s own fields; optional `workflow_params`,
`project_id`, `parent_session_id`, `timeout_seconds`. Documented default for "how
outer-workflow params map to the inner Loop's overrides": `workflow_params`, when the step's
own `Config` omits it, defaults to forwarding the *outer* run's own `WorkflowInput.Params`
verbatim — an explicit, low-machinery default (no new templating support invented) rather than
requiring every loop step to repeat the outer run's params by hand. Deliberately **not** run
through `resolveStepConfig`'s `{{ }}` templating, matching `StepKindGate`/`StepKindFlex`'s own
precedent (neither templates its Config either). Tested in `TestParseLoopStepConfig` (8 sub-cases).

**Where `loop_run_id` gets recorded on the step (item 4, second half).** A real column,
`workflow_run_steps.loop_run_id` (migration 141), populated on every write from the moment
`LoopEngine.Run` first returns a `loop_run_id` (whether the step then resolves immediately or
parks `waiting_on_loop`) through to its terminal resolution — see migration's own doc comment
for the "why a column, not JSON" reasoning (bidirectional lookup: step→loop_run_id via the row;
terminal loop_run_id→step via the new `GetWorkflowRunStepByLoopRunID` store method).

**`StepKindLoop` step executor** (`internal/service/workflow_engine_loop.go`,
`workflow_engine.go`'s `runStep`/`execute()`): first entry (`startLoopStep`) resolves config,
calls `LoopStepLauncher.LaunchLoop`, and either resolves the step immediately (the contained
`LoopRun` already went terminal synchronously within that one launch call — the common
Ralph-shaped case, per `internal/loop`'s own "drives iteration-to-iteration synchronously
within one call" framing) or records `loop_run_id` and marks `waiting_on_loop`. A step already
`waiting_on_loop` never re-enters `runStep`; `execute()`'s per-level dispatch loop instead
dispatches `recheckLoopStep` on every `Resume` pass (mirrors `StepKindFlex`'s own
`recheckFlexStep` dispatch shape, renamed the shared `flexResolved` outcome-loop field to
`resolvedFromWait` since both kinds now use it) — re-reading the step's own recorded
`loop_run_id` and the `LoopRun`'s live status, resolving only on a genuine terminal state.
This recheck is **not** flex's lazy "piggyback on any unrelated caller's Resume" pattern: the
reason the outer run ever gets a fresh `Resume` call while a loop step is waiting is the real
push (`OuterResumeNotifier.NotifyLoopRunTerminal`, called synchronously by `LoopEngine` the
moment it persists a terminal `loop_runs.status`, at `DecisionComplete`/`DecisionFail` in
`evaluateDecideAndAct` and the fail branch of `escalateOnBoundedContextExceeded`) — `execute()`'s
recheck is the idempotency/correctness layer on top of that push, not the trigger itself.
`isPausedRunStatus` and `classifyIterationProgress` in `internal/loop/engine.go` were also
extended to recognize `RunStatusWaitingOnLoop` as non-terminal, for the nested case (one loop
iteration's own `WorkflowRun` can itself contain a `StepKindLoop` step).

**End-to-end integration test** (not reduced to unit tests, per the task's explicit
instruction): `internal/loop/stepkindloop_integration_test.go`,
`TestStepKindLoop_OuterWorkflowRun_ResolvesViaRealPush_NotManualResume`. Builds a real outer
`WorkflowDefinition` with one `StepKindLoop` step whose inner `WorkflowDefinition` is a single
gate step (deliberately, so the `LoopRun`'s first iteration pauses `waiting_on_gate` rather
than completing synchronously within the first launch call — otherwise the outer run would go
straight to `RunStatusCompleted` and never genuinely sit in `RunStatusWaitingOnLoop`, defeating
the point of the test). Wires one shared `*service.BuiltinWorkflowEngine` for both the outer
run and the loop's own iterations (matching production wiring exactly), a real `*loop.LoopEngine`
with `WithOuterResumeNotifier` pointed at a real `service.LoopResumeNotifier`, and confirms: (1)
the outer run genuinely reaches and persists `RunStatusWaitingOnLoop`; (2) after resolving the
inner gate and recording qualifying goal evidence, calling `Resume` **only on the inner
`LoopRun`** (never on the outer run) drives the `LoopRun` to `completed`, and the outer
`workflow_runs` row is independently confirmed to have transitioned to `completed` purely as a
side effect of `NotifyLoopRunTerminal` calling `BuiltinWorkflowEngine.Resume` on the outer run
internally — this test never calls `.Resume`/`.Run` on the outer run itself after the initial
launch, satisfying "not a manually-triggered test-only `.Resume()` call standing in for it."

**Production wiring** (`cmd/nanite/main.go`): after `workflowLauncher` is constructed,
`loop.NewLoopEngine(container.Store, workflowDefinitionsRegistry, workflowLauncher)` and
`service.NewLoopResumeNotifier(container.Store, workflowDefinitionsRegistry, workflowLauncher)`
are built reusing the exact same registry/launcher every other workflow-launching path already
shares, wired together via `WithOuterResumeNotifier`, and `workflowEngine.WithLoopSupport(...)`
attaches loop support to the one shared `BuiltinWorkflowEngine` instance.

**Collateral fix to a pre-existing migration-boundary test.** Widening
`workflowRunStepColumns`/`UpsertWorkflowRunStep` to always include the new `loop_run_id` column
broke `internal/store/migration_136_workflow_run_steps_loop_kind_test.go`'s
`TestRealBackupWorkflowRunStepsSurviveLoopKindMigration`, which calls the Go `UpsertWorkflowRunStep`
helper directly against a database intentionally rolled back to schema version 134 (before
`loop_run_id` existed) — the helper's INSERT statement is written against this worktree's head
schema and failed with "no such column: loop_run_id" at that rolled-back version. Fixed by
switching that one synthetic-row insertion to raw SQL matching the literal schema at version
134 (matching the same technique this task's own new migration-141 test already uses for its
synthetic rows). Confirmed the fix doesn't weaken that test's own assertions — same row shape,
same table, only the insertion mechanism changed.

**`go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`** — all verified by reading the
actual command output text directly (redirected to a file, then read with the `Read` tool),
never through a piped/masked exit code:
- `go build ./cmd/nanite/`: clean, empty output, exit 0.
- `go test ./...`: exit 0, zero `FAIL`/`panic` lines across the full run (93 packages report
  `ok`, confirmed after `go clean -testcache` to rule out stale cached passes).
- `go vet ./...`: exit 1, but the **only** reported issues are pre-existing lostcancel warnings
  in `internal/service/container.go` (lines 1180/1200/1260, part of commit `92caae84` "Phase
  6/04" work, confirmed via `git log`/`git status` to be a file this session never touched).
  `go vet` scoped to every package this task actually modified
  (`./internal/service/... ./internal/loop/... ./internal/store/... ./internal/agentworkflow/...
  ./cmd/nanite/...`) reports the identical, sole pre-existing failure — no new vet issue was
  introduced by this task's changes. Not fixed here: out of this task's scope and unrelated to
  loops/StepKindLoop.

**No deviation from the task's core design asks.** Every "What to do" item (1–5) and every
"Done means" bullet is implemented and tested as specified, including the exact tie-break order
(gate > flex > loop) and the required end-to-end push-mechanism test.

## Review notes

Reviewed 2026-08-21 — pass, with one minor, non-blocking gap found and fixed. Deep review
covering: the corrected import-cycle resolution (independently confirmed `internal/loop`
imports `internal/service`, not the reverse; zero `internal/loop` imports anywhere in
`internal/service`'s non-test files), the three-way gate>flex>loop precedence (all 7 required
cases plus 3 extra, all correct), the real synchronous push mechanism (traced
`notifyOuterOnTerminal`'s call sites — fires immediately after the terminal status persists,
never deferred or conditional on an unrelated caller), the race-condition handling (confirmed
`startLoopStep`'s own immediate terminal-status check resolves the step correctly independent
of whatever the notifier already attempted), the end-to-end integration test (re-ran directly,
confirmed it never calls `.Resume()`/`.Run()` on the outer `WorkflowRun` — only on the inner
`LoopRun` — and asserts the outer run's `completed` transition against real persisted state),
all cross-cutting edits to already-shipped Teams/A2A code (both purely additive, no existing
case altered), the migration-136 collateral fix (assertions byte-for-byte unchanged), and
`cmd/nanite/main.go`'s wiring (reuses the same registry/launcher/store instances, both
`WithLoopSupport`/`WithOuterResumeNotifier` genuinely called).

**Gap found:** `LoopStepLauncher` had a compile-time interface-satisfaction assertion
(`var _ service.LoopStepLauncher = (*LoopEngine)(nil)`, `internal/loop/step_launcher.go`) but
the symmetric `OuterResumeNotifier` side did not — a future signature drift on
`NotifyLoopRunTerminal` could go undetected unless it also happened to touch one of the two
real call sites that currently exercise it. Fixed directly (non-behavioral, one-line, exact
fix already specified) by the Orchestrator: added
`var _ OuterResumeNotifier = (*service.LoopResumeNotifier)(nil)` to
`internal/loop/outer_resume.go`. `go build ./cmd/nanite/`, `go vet ./internal/loop/...`, and
`go test -count=1 ./internal/loop/... ./internal/service/...` all confirmed green after the
fix.

`go build ./cmd/nanite/`: exit 0. `go vet ./...`: exit 1, only the same pre-existing, unrelated
`internal/service/container.go` findings. `go test -count=1 ./...`: exit 0, zero `FAIL`/`panic`
across the full suite.
