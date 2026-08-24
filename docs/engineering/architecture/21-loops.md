# Loops

Design produced by a dedicated architecture-alignment session (2026-08-21), reviewing an external proposal (`~/dev/chrispian/inbox/nanite-loop-engineering-and-goals.md`) against the real code: `agentworkflow`, `internal/agent/reflexes`, the Scheduling design (12-scheduling.md), and the Teams design (15-teams.md) — the two most structurally similar precedents. **Design only — no code or schema changed in this session.** Implementation is deferred to follow-up work, not yet filed.

## The problem this solves

Nanite has agent turns, agent workflows (`agentworkflow`), reflexes, scheduling, subagents, and durable agents — but nothing answers "keep working toward this target state, across multiple bounded executions, until it's actually true, on a budget, with an escalation path." Today that's either hand-rolled per case or approximated by an agent deciding for itself when it's "done." The proposal's core claim survives contact with the real code: the missing piece is a **control loop above execution**, not another kind of agent, and not a rewrite of `agentworkflow` — whose scope is explicitly, deliberately **DAG only, no cycles** (`internal/agentworkflow/doc.go`).

## The core split

```
Workflow  = prescribed execution        (internal/agentworkflow, unchanged — still DAG-only, no cycles)
Goal      = desired state                (new: first-class, own lifecycle)
Loop      = convergent repetition        (new: internal/loop — orchestrates a sequence of WorkflowRuns)
LoopRun   = a peer to WorkflowRun          (NOT a WorkflowRun itself — see Decision 1)
```

This is the same shape Teams drew one layer over (Team = prescribed organization, TeamRun IS a WorkflowRun). Loop is deliberately **not** the same collapse. Team could compile fully into a WorkflowRun because a Team's phase sequence, gates included, is itself still one static DAG. A Loop's defining property — the plan can change shape between iterations (REPLAN/REARCHITECT), and iterations can run genuinely different executors (a different provider/model, even a different WorkflowDefinition) — is exactly what `agentworkflow`'s DAG-only scope deliberately excludes. Collapsing Loop into WorkflowRun the way Team did would either reopen that scope boundary (add real cycles to the DAG engine) or corrupt what WorkflowRun means. Both are worse than one new peer entity.

## Decision 1 — LoopRun is a new peer entity to WorkflowRun, not a WorkflowRun itself

Each iteration's actual unit of work is an entirely ordinary `WorkflowRun`, launched via the existing `WorkflowLauncher.Launch`/`.Resume`. `LoopRun` orchestrates a *sequence* of these — varying the definition/params across iterations under REPLAN/REARCHITECT — rather than owning a second execution engine. Nothing about `StepExecutor`, `Verify`, `StepKindGate`, or `StepKindFlex` changes; a loop iteration is free to contain gates, flex phases, or nested Team runs exactly as any WorkflowRun is today.

**One new, thin step kind is still needed** — `StepKindLoop` — so a Workflow can *contain* a Loop (goal-doc §12: "a workflow node can invoke a loop"). It reuses the identical pause/external-resolve/`Resume` plumbing `StepKindFlex` already reused from `StepKindGate` — a `StepKindLoop` step returns a waiting status when it starts, and something external (the contained `LoopRun` reaching a terminal state) is what calls `Resume` on the *outer* workflow. This is not new engine behavior, just the third reuse of a pattern the engine already has twice.

By the same reasoning `RunStatusWaitingOnFlex`'s own doc comment gives for why it needed to be a distinct literal from `RunStatusWaiting` (so A2A's `deriveFromWorkflowRun` doesn't misreport a flex-waiting run as needing human input), a workflow paused on a contained loop needs its own distinct status: **`RunStatusWaitingOnLoop`**. A loop is not a gate and is not a flex phase — an outer consumer needs to tell all three apart. If a run is blocked by more than one kind at once, gate still takes priority (matching `flexOrGateWaitingStatus`'s existing precedence), then flex, then loop — the least human-attention-demanding kind loses the tie-break.

The reverse direction — **a Loop invoking a Workflow** — needs nothing new at all: an iteration's `WorkflowDefinition` is just a `WorkflowDefinition`.

Ralph falls out of this for free: it's `max_iterations` runs of a trivial one-`llm`-step (optionally + `tool` + `verify`) `WorkflowDefinition`, no REPLAN, no REARCHITECT. That's the proposal's own closing line, realized directly: "Ralph support can therefore be a preset over a more general loop abstraction rather than a special subsystem."

## Decision 2 — Goal is a first-class entity with its own lifecycle, not embedded state

Operator call: Goal gets a real `goals` row and the full lifecycle the proposal describes (§15) — `DRAFT | DEFINED | ACTIVE | BLOCKED | SATISFIED | FAILED | CANCELED | SUPERSEDED` — from day one, not deferred to a later phase. This is a deliberate divergence from this repo's usual "start narrow, add a table later" bias (the one applied to Verify's check registry, and the one this design would otherwise have defaulted to) — the operator's reasoning: a Goal outliving any single LoopRun (survives an abandon-and-restart, survives a REPLAN, potentially spans a Team's flex phases and a Loop's iterations under one shared target state) is load-bearing for how this is meant to be used, not a nice-to-have.

**A `loop_runs` row always has exactly one `goal_id` — a `goals` row does not require a `loop_runs` row.** A goal can exist `DRAFT`/`DEFINED` before any loop launches against it (authored by a planning session, an architect agent, or an operator), and `parent_goal_id` supports the decomposition the proposal describes in §16 (one goal recomputing its own subgoals as barriers are discovered) independent of execution. `LoopLaunchRequest` accepts either an existing `goal_id` or an inline goal spec, which the launcher upserts into `goals` first — so Ralph-shaped ergonomics (`Loop::ralph()->goal($goal)->run()`) don't require a separate authoring step for the common case, even though storage is always first-class underneath.

**Goal evidence is a thin pointer table, not a duplicate content store** — `goal_evidence(goal_id, loop_run_id, iteration_number, evidence_type, ref_table, ref_id, result, summary, recorded_at)`. It points into `workflow_run_steps.verify_json`, a gate's resolution record, or an `event_log` row — the same "derived/observability, not new persistent state" discipline Teams applied to routing provenance. Goal satisfaction (`goal_met = acceptance_criteria_satisfied AND constraints_satisfied AND invariants_preserved AND required_evidence_present`, proposal §7) is computed by walking these pointers, not by trusting a bare status flag.

## Illustrative shape

```yaml
goal:
  id: g_feature_loop_engine
  intent: "Implement durable loop execution in Nanite"
  desired_state:
    - loops can execute agent-workflow iterations
    - state persists across iterations
    - evaluators can terminate execution
  acceptance_criteria: [loop_unit_tests_pass, ralph_integration_test_passes]
  constraints: [existing agentworkflow API remains backwards compatible]
  status: active

loop_run:
  id: lr_2026_08_21_01
  goal_id: g_feature_loop_engine
  definition_name: implementation_iteration   # a WorkflowDefinition name/version
  status: running
  current_iteration: 3
  budget: { max_iterations: 20, max_failures: 3, max_no_progress_iterations: 2 }
  continuation_policy: default

loop_run_iterations:
  - iteration_number: 1
    workflow_run_id: wr_abc
    decision: continue
    progress_state: progress
  - iteration_number: 2
    workflow_run_id: wr_def
    decision: replan          # WorkflowRun for iteration 3 uses a different definition
    progress_state: no_progress
  - iteration_number: 3
    workflow_run_id: wr_ghi
    decision: null             # in flight
```

Compiles/executes roughly like `agentworkflow`'s own `BuiltinWorkflowEngine.Run`/`.Resume` pair, one level up:

```go
type LoopEngine interface {
    Run(ctx context.Context, def LoopDefinition, input LoopInput) (LoopResult, error)
    Resume(ctx context.Context, loopRunID string) (LoopResult, error)
}
```

Its only executor dependency is the existing `WorkflowLauncher` (`Launch`/`Resume`) — `LoopEngine` does not get its own `StepExecutor`. For a bounded loop (Ralph, test/fix, review/fix), `Run` drives iteration-to-iteration synchronously within one call, exactly as `BuiltinWorkflowEngine.Run` already iterates over ready DAG nodes in one call. A `WAIT` or `ESCALATE` decision persists `loop_runs.status` and returns — resumed later by a human action, a scheduled tick, or a reflex firing calling `Resume`, mirroring exactly how a gate-blocked `WorkflowRun` already works.

## Continuation policy — the one genuinely new decision engine

Nothing existing computes `CONTINUE | RETRY | REPLAN | REARCHITECT | WAIT | ESCALATE | COMPLETE | FAIL` from an evaluation, history, and budget. The closest precedent in shape — not in job — is `reflexes.Resolve` (the shared combining-algorithm decision primitive from `10-reflex-action-taxonomy.md`): candidates in, one resolved outcome out, deterministic-first. It is a different question (which reflex action wins this turn, vs. what a loop does next given an evaluation) and forcing them into one function would reproduce the exact governance-illegibility problem that doc already ruled out for a generic `callback` action kind. **Continuation policy borrows the vocabulary and the deterministic-first discipline, not the mechanism** — a new, small function in `internal/loop`:

```go
func decide(goal Goal, evaluation Evaluation, history []IterationResult, budget Budget) Decision
```

Deterministic first: `goal_met` (per Decision 2's evidence walk) → `COMPLETE`; budget exhausted → `ESCALATE` or `FAIL` depending on an `on_exhausted` policy field (borrowing Scheduling's own `retry`/`disable`/`notify` vocabulary as the starting point, not locked here); `no_progress_streak >= budget.max_no_progress_iterations` → the one case that needs reasoning. That reasoning fallback is **not a nested WorkflowRun** — it's a bare `StepExecutor.ExecuteLLMStep` call, the identical pattern `VerifyModeAgent` already uses for its independent reviewer step, one level up: a judgment call gets an isolated model call, not a whole new DAG.

Per-iteration evaluation reuses `Verify` wholesale — an iteration's `IterationResult.progress` (`PROGRESS | NO_PROGRESS | REGRESSION | BLOCKED | GOAL_MET`) is a rollup over that iteration's `WorkflowRun` step `VerifyResult`s, not a second evaluator subsystem. Growing the deterministic check registry (`workflowEngineChecks`: today `no_error`/`tool_called`/`output_contains`) with loop-relevant checks (`tests_pass`, `lint_pass`, and similar) is the same "start narrow, grow as real workflows need more" registry Verify already uses — no new registry.

## Trigger surface

No new trigger mechanism — every trigger the proposal names already has a home:

- **Manual/API launch** — `LoopLauncher.Launch`, mirroring `WorkflowLauncher.Launch`.
- **Event/predicate** — the same reflex trigger-spec AST (`internal/agent/reflexes`) Flex-step exit triggers already reuse, firing a `Resume` on a `WAIT`-status `LoopRun`.
- **Scheduled** — a new `loop_run_tick` `JobType`, the fifth alongside Scheduling's already-enumerated `durable_agent_wake`/`agent_workflow_run`/`command_run`/`reflex_dispatch` (12-scheduling.md) — for a "durable"-preset loop or a `WAIT`-status loop polling an external condition (the proposal's Event/Durable Loop type, §11). Depends on Scheduling's own design landing first; not itself new engine work.
- **Nested (workflow contains loop)** — `StepKindLoop`, Decision 1.
- **Human resolution of `waiting_on_escalation`** — an operator resolve call structurally identical to gate resolution (A2A's existing `resumeWorkflowRun` pattern), calling `LoopEngine.Resume` with an optional decision override (force `COMPLETE`, force `CANCEL`, or supply a replan).

## New mechanism vs. reuse — explicit ledger

| Piece | New or reused |
|---|---|
| Iteration execution | Fully reused (`WorkflowLauncher.Launch/Resume`, `StepExecutor`, `Verify`, `StepKindGate`, `StepKindFlex`) |
| Deterministic evaluators | Reused registry (`workflowEngineChecks`), grown with loop-relevant checks |
| LLM evaluators / actor≠evaluator | Fully reused (`VerifyModeAgent`) |
| Human escalation inside one iteration | Fully reused (`StepKindGate`) |
| Event/predicate triggers | Fully reused (reflex trigger-spec AST) |
| Scheduled ticks | Reused engine (`go-scheduler` design), new `JobType` (`loop_run_tick`) |
| Workflow-contains-Loop | **New** step kind (`StepKindLoop`) + **new** `RunStatus` (`waiting_on_loop`) — reuses Gate/Flex's pause/resume plumbing |
| `LoopRun` orchestration | **New** (`internal/loop` package, `LoopEngine`) |
| Continuation/decision policy | **New** (deterministic-first; reasoning fallback reuses `ExecuteLLMStep` directly, no nested workflow) |
| Goal (first-class entity + lifecycle) | **New** (`goals` table) |
| Goal evidence | **New**, thin pointer table (`goal_evidence`) — points into existing verify/gate/event records |
| Iteration history / progress tracking | **New**, thin (`loop_run_iterations`) — same shape as Scheduling's illustrative `schedule_runs`: one row per firing |
| No-progress / convergence detection | **New**, minimal v1 — a streak counter against a budget threshold; deeper heuristics (same-tests-failing, semantic-state-unchanged) deferred |
| Presets (ralph, test-fix, review-fix, …) | **New**, but pure configuration — named `(budget, continuation_policy, iteration-definition template)` bundles, no per-preset engine |

## Illustrative schema (not migration-ready — matching this repo's own convention at this design stage)

```
goals(
  id, parent_goal_id NULL,
  intent, desired_state_json, constraints_json, acceptance_criteria_json, invariants_json,
  priority, scope,
  status CHECK(draft|defined|active|blocked|satisfied|failed|canceled|superseded),
  owner, source,
  created_at, activated_at, completed_at
)

goal_evidence(
  id, goal_id, loop_run_id NULL, iteration_number NULL,
  evidence_type,        -- test_suite | verify_result | gate_approval | human_acceptance | artifact
  ref_table, ref_id,     -- pointer into workflow_run_steps / gate resolution / event_log, not duplicated
  result, summary, recorded_at
)

loop_runs(
  id, goal_id NOT NULL,
  definition_name,        -- initial iteration WorkflowDefinition (or preset name)
  status CHECK(running|completed|failed|canceled|waiting_on_gate|waiting_on_escalation),
  current_iteration,
  budget_json,             -- max_iterations, max_failures, max_runtime, max_no_progress_iterations
  continuation_policy_json,
  no_progress_streak,
  started_at, updated_at, completed_at
)

loop_run_iterations(       -- one row per firing, mirrors 12-scheduling.md's illustrative schedule_runs
  id, loop_run_id, iteration_number,
  workflow_run_id,          -- FK to workflow_runs — the actual executed unit
  decision,                 -- continue|retry|replan|rearchitect|wait|escalate|complete|fail
  progress_state,            -- progress|no_progress|regression|blocked|goal_met
  evaluation_json,           -- remaining_delta, confidence, regressions
  started_at, completed_at
)

workflow_runs.loop_run_id NULL   -- run-scoping FK, same pattern as migration 131's agent_reflexes scoping
workflow_runs.loop_iteration NULL
```

## Naming decisions made this session

- **`internal/loop`**, disambiguated explicitly from two existing, different things also reachable by the bare word "loop": (1) `internal/loopdetect` and the harness's own per-turn tool-call loop — the proposal's own "Agent Loop" type (§11, `reason → act → observe → reason`) names this *existing* mechanism, not the new package; (2) legacy `internal/workflow.LoopStep` — see below. This package needs a `doc.go`-style disambiguation note, the same precedent `agentworkflow/doc.go` set for its own naming collision with `internal/workflow`.
- **Not "checkpoint"** for any loop rollback/recovery concept (the proposal's Recovery Policy mentions "restore checkpoint," §2). `GLOSSARY.md` already claims that word for Tether's provider-session resume hint and says explicitly it "must never be used for filesystem state" — extending the same discipline, Loop should say "roll back to iteration N's state," not reuse "checkpoint."
- **`RunStatusWaitingOnLoop`** follows `RunStatusWaitingOnFlex`'s own precedent exactly — a distinct literal because a downstream consumer (A2A's `deriveFromWorkflowRun`, or any future equivalent) needs to know whether a paused run genuinely needs a human.
- **"Evidence"** is not currently claimed elsewhere in `GLOSSARY.md` — safe to adopt for Goal's evidence model as written.

## Legacy `internal/workflow.LoopStep` — boundary documented, not retired

`internal/workflow` (the older, generic YAML-authored pipeline runner — shell/skill/parallel/loop steps, wired live into `internal/api/workflows.go`) already has its own `LoopStep`: "repeats a step until a gate passes or max iterations reached," deterministic-only, no LLM involvement. It is real and API-wired; no repo-committed YAML uses `type: loop`, but that isn't evidence of zero runtime callers — the actual usage question wasn't checked in this session. This design does not propose retiring it. What matters now is that nobody conflates the two: legacy `LoopStep` is a narrow, deterministic, pre-existing pipeline-repeat primitive for a different audience (ops/automation pipelines); the new `internal/loop` is an agentic, goal-driven control layer over `agentworkflow`. If a real usage check later shows `LoopStep` has no production callers, that's a separate "kill dead code" pass under this repo's standing policy — not a decision made here.

## Guardrail

The net effect of every decision above is that Loop is a **control layer over existing runtime primitives**, not a new multi-agent runtime or a second workflow engine sitting beside `agentworkflow`:

```
Goal (first-class, own lifecycle)
    ↓ launch or resume
LoopRun
    ↓ evaluate prior iteration (Verify, reused)
    ↓ decide (deterministic-first, reasoning fallback = one ExecuteLLMStep call)
    ↓ launch next iteration
    ↓ ordinary WorkflowRun (agentworkflow, unchanged) does the actual work
```

Protect that framing during implementation. If building this starts to require a `LoopExecutionEngine`, `LoopStepExecutor`, `LoopScheduler`, or `LoopMemory` — anything duplicating a subsystem this doc already named as reused — that's a signal the design has drifted, not a natural extension of it.

## What this session did not decide

- Exact migration DDL for every table above — illustrative shape only, matching this repo's convention at this design stage.
- Whether `goal_evidence` needs to also capture free-text evidence not tied to a structured ref (e.g. a human's written acceptance note), or always points at something structured.
- REARCHITECT's concrete mechanism — whether it's "REPLAN but the Goal's `desired_state`/`acceptance_criteria` themselves get revised," or a literal architect-agent call whose output patches the `goals` row. Both are plausible; not resolved here.
- Whether two `LoopRun`s can run concurrently against the same `goal_id` (competing strategies racing toward one target state) — not addressed.
- The exact `budget`/`on_exhausted` field set and vocabulary — Scheduling's `retry`/`disable`/`notify` is proposed as a starting point, not locked.
- `loop_run_tick`'s payload shape — depends on Scheduling's own implementation landing first.
- Auth/permission model for launching, canceling, or force-resolving a `LoopRun` escalation.
- Whether presets (`ralph`, `test-fix`, `review-fix`, `goal`, `plan-execute`, `queue-drain`, `durable`, `self-improve`) are DB rows (like reflex seeds) or Go-coded constants for v1.
- Any implementation sequencing/task breakdown — none of this is scheduled or filed yet.

## Status

Design discussed and aligned with the operator 2026-08-21, including explicit resolution of the three load-bearing forks (LoopRun-vs-WorkflowRun relationship, Goal as first-class from day one, legacy `LoopStep` left undecided rather than retired). No code or schema changed. Implementation is deferred to follow-up work, not yet filed.
