# `StepKindLoop` executor + `RunStatusWaitingOnLoop`

**Phase:** 2 — Runtime engine (`TASKS/loops`)
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
