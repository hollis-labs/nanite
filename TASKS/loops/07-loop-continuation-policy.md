# Continuation policy — the `decide()` function

**Phase:** 2 — Runtime engine (`TASKS/loops`)
**Status:** not-started
**Depends on:** `01-goals-schema.md`, `02-goal-evidence-schema.md`,
`03-loop-runs-schema.md`, `04-loop-run-iterations-schema.md`
**Touches:** new `internal/loop/decide.go` (new package — first file in it; needs a
`doc.go` too, see item 3), `internal/service/workflow_step_executor.go` (read-only
reference — calling `ExecuteLLMStep`, not modifying it).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s "the one genuinely new decision
engine" — quoted directly: *"Nothing existing computes `CONTINUE | RETRY | REPLAN |
REARCHITECT | WAIT | ESCALATE | COMPLETE | FAIL` from an evaluation, history, and budget...
a new, small function in `internal/loop`:*
```go
func decide(goal Goal, evaluation Evaluation, history []IterationResult, budget Budget) Decision
```

**Precedent in shape, not job** — `internal/agent/reflexes.Resolve` (confirmed this session,
`internal/agent/reflexes/resolve.go:177`): *"candidates in, one resolved outcome out,
deterministic-first."* The design doc is explicit this is borrowed vocabulary and discipline
only, not a shared mechanism — folding continuation policy into `Resolve` "would reproduce
the exact governance-illegibility problem" `docs/engineering/architecture/10-reflex-action-taxonomy.md`
already ruled out for a generic `callback` reflex action kind. Confirmed this session,
`10-reflex-action-taxonomy.md:120`: *"an opaque callback target is illegible to the
combining-algorithm and provenance-ceiling facets."* Do not attempt to route `decide()`
through `reflexes.Resolve` — it's a genuinely separate function, in a genuinely separate
package.

**Deterministic-first decision order**, per the design doc:
1. `goal_met` (task `02`'s `EvidenceSatisfiesGoal` walk) → `COMPLETE`.
2. Budget exhausted (`current_iteration >= budget.MaxIterations`, or a failure-count/
   runtime check against the rest of `Budget`'s fields) → `ESCALATE` or `FAIL`, per this
   task's own decided `on_exhausted` field (task `03`'s `Budget.OnExhausted`,
   `∈ {"escalate","fail"}`).
3. `no_progress_streak >= budget.MaxNoProgressIterations` → **the one case needing
   reasoning.** Design doc, quoted exactly: *"That reasoning fallback is not a nested
   WorkflowRun — it's a bare `StepExecutor.ExecuteLLMStep` call, the identical pattern
   `VerifyModeAgent` already uses for its independent reviewer step... a judgment call gets
   an isolated model call, not a whole new DAG."* Confirmed this session — `VerifyModeAgent`
   (`internal/service/workflow_step_executor.go`, `verifyAgent` at line 355) builds an
   `agentworkflow.LLMStepRequest{...}` and calls `ExecuteLLMStep` (line 123's signature:
   `func (e *workflowStepExecutor) ExecuteLLMStep(ctx context.Context, req
   agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error)`) directly — that
   exact call shape is `decide()`'s own reasoning-fallback template. The LLM call's output
   must resolve to exactly one of `RETRY | REPLAN | REARCHITECT | WAIT | ESCALATE | FAIL` —
   design a small, strict output contract (e.g. a one-word/JSON-object response parsed the
   same defensive way `VerifyModeAgent`'s own result-parsing does) rather than free-text.
4. Otherwise → `CONTINUE`.

**REARCHITECT's mechanism, this planning session's own decision** (README, "What this
session decided"): REARCHITECT is REPLAN *plus* a revision to the Goal's
`desired_state_json`/`acceptance_criteria_json` (task `01`'s columns), computed by the
*same* reasoning-fallback `ExecuteLLMStep` call as step 3 above — not a second, separate
mechanism or a literal architect-agent dispatch. When the reasoning fallback resolves to
`REARCHITECT`, its LLM response also carries the proposed revision, and `decide()`'s caller
(task `08`) is responsible for calling `UpdateGoal` with the revised fields before launching
the next iteration under the new shape.

**Per-iteration evaluation reuses `Verify` wholesale** — design doc: *"an iteration's
`IterationResult.progress` is a rollup over that iteration's `WorkflowRun` step
`VerifyResult`s, not a second evaluator subsystem."* This task's `Evaluation` input type
(task `04`'s `evaluation_json` shape) is that rollup, computed by task `08` from the
completed iteration's `WorkflowRun` steps and passed in — `decide()` itself doesn't call
`Verify` or touch `workflow_run_steps` directly; keep it a pure function of its four
parameters (goal, evaluation, history, budget) plus the one real side effect (the
reasoning-fallback LLM call), for testability.

**Growing `workflowEngineChecks`** — the design doc names `tests_pass`/`lint_pass` as
loop-relevant deterministic checks to add to the existing registry (confirmed this session,
`internal/service/workflow_step_executor.go:47`, `map[string]workflowEngineCheck` — same
"start narrow, grow as real workflows need more" registry `Verify` already uses). This task
adds those two check functions to that map (same file, same package) — they're consumed by
an iteration `WorkflowRun`'s own `Verify` step, upstream of `decide()`, not by `decide()`
itself.

## What to do

1. **`internal/loop/decide.go`** — `Goal`, `Evaluation`, `IterationResult`, `Budget`,
   `Decision` types (thin wrappers or direct aliases over the `internal/store` types from
   tasks `01`/`03`/`04` — your call whether `internal/loop` re-declares its own small
   value types or imports `internal/store`'s directly; if you re-declare, keep conversion
   functions colocated and tested). `func Decide(ctx context.Context, exec
   agentworkflow.StepExecutor, goal Goal, evidence []GoalEvidence, evaluation Evaluation,
   history []IterationResult, budget Budget) (Decision, error)` — note `ctx`/`exec` are
   needed for the reasoning-fallback branch's `ExecuteLLMStep` call, so the real signature
   necessarily differs from the design doc's bare illustrative sketch; document that
   deviation plainly, it's expected.

2. **Deterministic branches** (1-2-4 above) — plain Go, fully unit-testable with no LLM
   call, no DB. Branch 1 calls task `02`'s `EvidenceSatisfiesGoal`-equivalent (or receives
   its result already computed — your call which side owns the DB read; if `Decide` itself
   takes a `goal_id` and queries, it needs a store handle, which then makes it not a pure
   function — prefer the caller (task `08`) doing the evidence-walk query and passing the
   boolean result in, keeping `Decide` DB-free apart from the one LLM call).

3. **`internal/loop/doc.go`** — required, not optional, per the design doc's own naming
   section: *"disambiguated explicitly from two existing, different things also reachable by
   the bare word 'loop'... This package needs a `doc.go`-style disambiguation note, the same
   precedent `agentworkflow/doc.go` set."* Confirmed this session — the real
   `agentworkflow/doc.go` documents that package's naming collision with `internal/workflow`
   (not the DAG-only claim, which actually lives in `dag.go`/`types.go` — cite those two
   files if you reference DAG-only-no-cycles elsewhere in this package's docs, not `doc.go`).
   Write `internal/loop/doc.go` distinguishing this package from: (a) `internal/loopdetect`
   (confirmed this session — the harness's own per-turn tool-call repeat-count guard,
   `internal/loopdetect/detector.go`, unrelated); (b) legacy `internal/workflow.LoopStep`
   (confirmed live-reachable via `POST /api/workflows/runs`, see this batch's README — a
   narrow, deterministic, no-LLM "repeat until gate passes" pipeline primitive, a different
   mechanism for a different audience, not retired or replaced by this package).

4. **Grow `workflowEngineChecks`** — `internal/service/workflow_step_executor.go`, add
   `"tests_pass"` and `"lint_pass"` entries to the existing map (same
   `func(agentworkflow.VerifySubject, map[string]any) (bool, string)` signature the three
   existing entries use). Keep these narrowly scoped to what's actually checkable from a
   `VerifySubject` today (likely: a tool-call result's exit code / stdout pattern, matching
   `output_contains`'s own existing implementation shape) — don't invent a new subsystem for
   "did tests pass," reuse whatever signal `VerifySubject` already carries.

## Done means

- `Decide` is unit-tested against all deterministic branches (goal met, budget exhausted
  under both `on_exhausted` values, otherwise continue) with zero LLM calls in those tests.
- The reasoning-fallback branch is tested with a stubbed/mocked `agentworkflow.StepExecutor`
  (matching whatever mocking pattern `workflow_step_executor_test.go` already uses for
  `ExecuteLLMStep`, if one exists — check before inventing a new one) asserting the output
  parses correctly into exactly one of the five reasoning-eligible `Decision` values, and
  that a malformed/unparseable LLM response fails safe (does not silently default to
  `CONTINUE` — escalate or error, your call, document it).
- `internal/loop/doc.go` exists with the required three-way disambiguation.
- `tests_pass`/`lint_pass` added to `workflowEngineChecks`, each with its own unit test.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
