# Continuation policy — the `decide()` function

**Phase:** 2 — Runtime engine (`TASKS/loops`)
**Status:** reviewed
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

**Built:**

- `internal/loop/doc.go` — package doc with the required three-way
  disambiguation (`internal/loopdetect`, legacy `internal/workflow.LoopStep`,
  and a note that this package's own docs cite `agentworkflow/dag.go`/
  `types.go`, not `agentworkflow/doc.go`, for the "DAG only, no cycles"
  claim — verified both files actually carry that exact phrase before
  citing them).
- `internal/loop/decide.go` — `Decide(ctx, exec, goal, evidence, evaluation,
  history, budget, policy) (Decision, error)`, plus:
  - `Goal`, `GoalEvidence`, `Evaluation`, `Budget`, `IterationResult` as
    direct type aliases onto `store.Goal`/`store.GoalEvidence`/
    `store.Evaluation`/`store.Budget`/`store.LoopRunIteration` — chose
    aliasing over re-declaring parallel types (the task left this an open
    choice) because `IterationResult = store.LoopRunIteration` already
    carries exactly the `IterationNumber`/`Decision`/`ProgressState`/
    `Evaluation()` shape `Decide` needs, and `Goal`'s
    `AcceptanceCriteria()`/`Constraints()`/`Invariants()` accessors are
    reused as-is by `evidenceSatisfiesGoal` below.
  - `DecisionKind` (new type, 8 constants mirrored 1:1 from
    `store.LoopRunIterationDecision*`) and `Decision{Kind, Reason,
    Revision *GoalRevision}` (a struct, not a bare string) — the struct
    shape is what makes REARCHITECT's revision payload representable at
    all; see the deviation note below.
  - Branch 1 (`goal_met`): `evidenceSatisfiesGoal` re-implements task 02's
    `EvaluateGoalEvidence`/`EvidenceSatisfiesGoal` formula locally against
    an already-loaded `Goal` + already-queried `[]GoalEvidence`, rather than
    calling `store.EvaluateGoalEvidence` (which always does its own
    `GetGoal`/`ListGoalEvidence` DB reads) — this is the one place `Decide`
    duplicates ~15 lines of logic from `internal/store/goal_evidence.go`
    rather than reusing it, a deliberate trade-off to keep `Decide` DB-free
    per the task's own instruction ("prefer the caller doing the
    evidence-walk query... keeping Decide DB-free apart from the one LLM
    call"); the task's Touches section does not include modifying
    `internal/store`, so exposing a DB-free variant there was not an option.
  - Branch 2 (budget exhausted): checks all three `Budget` thresholds the
    task names (`MaxIterations`, `MaxFailures`, `MaxRuntimeSeconds`), not
    just `MaxIterations` — `current_iteration` is `len(history)` (history is
    expected to include the just-finished iteration Decide is deciding, see
    the "history shape" design note below); a "failure" for `MaxFailures`
    purposes is my own design call, documented inline: a history entry
    classified `store.LoopRunIterationProgressRegression` (closest existing
    classification to "a failure," distinct from the more neutral
    `NO_PROGRESS`/`BLOCKED` stalls branch 3 already handles); runtime is
    wall-clock elapsed from the first history entry's `StartedAt` to the
    last entry's `CompletedAt` (or now, if still in flight). A zero/unset
    threshold means "no cap on that dimension" (a `Budget{}` zero value must
    not read as "already exhausted at iteration 1") — verified by a
    dedicated test. `OnExhausted` empty defaults to `escalate`, mirroring
    `LoopRun.SetBudget`'s own default.
  - Branch 3 (no-progress streak): `noProgressStreak` walks history from the
    most recent entry backward, counting consecutive
    `LoopRunIterationProgressNoProgress` entries, stopping at the first
    non-match (including `REGRESSION`/`BLOCKED` — deliberately not folded
    in; `21-loops.md` itself calls the v1 streak counter "minimal," deeper
    heuristics deferred, and `REGRESSION` already has its own
    `MaxFailures` accounting in branch 2). When the streak reaches
    `budget.MaxNoProgressIterations`, `decideByReasoning` builds an
    `agentworkflow.LLMStepRequest` and calls `exec.ExecuteLLMStep` — the
    exact `VerifyModeAgent`/`verifyAgent` call shape the task names,
    confirmed by direct reading of `workflow_step_executor.go:355`.
  - The reasoning-fallback output contract is a strict single JSON object
    (`{"decision": "...", "reason": "...", "revised_desired_state": [...],
    "revised_acceptance_criteria": [...]}`), parsed defensively
    (`parseReasoningVerdict`): locates the first `{`...`}` span, decodes it,
    lower-cases and validates `decision` against exactly the six
    reasoning-eligible `DecisionKind` values (`RETRY|REPLAN|REARCHITECT|
    WAIT|ESCALATE|FAIL` — the design doc's own list in this task's Context
    section names six values, though the "Done means" section's prose calls
    it "five"; treated as a minor off-by-one in the task's own authoring,
    not a real discrepancy to resolve — all six are implemented and each is
    individually unit-tested). `revised_desired_state`/
    `revised_acceptance_criteria` are only read into a `GoalRevision` when
    `decision == "rearchitect"`.
  - **Malformed-response fail-safe, as pre-authorized by this task's own
    dispatch instructions**: an unparseable JSON body, a `decision` value
    outside the six reasoning-eligible values, or a nil/misconfigured
    `StepExecutor`/`ContinuationPolicy` all return a non-nil `error` (zero
    `Decision`) rather than defaulting to `DecisionContinue` or any other
    value. Chosen over escalating internally because `Decide` staying a
    pure-ish function whose caller (task 08) owns reacting to a hard failure
    is more consistent with every other error path in this file and with
    `StepExecutor`'s own convention of surfacing failures as errors, not
    absorbing them into a result value. Verified with dedicated tests
    (invalid JSON, unknown decision, `CONTINUE`/`COMPLETE` explicitly
    rejected as reasoning-fallback outputs, `ExecuteLLMStep` error
    propagation, nil executor, missing policy provider/model) — none of
    them ever produce `DecisionContinue`.
- `internal/service/workflow_step_executor.go` — added `"tests_pass"` and
  `"lint_pass"` to `workflowEngineChecks` (same
  `func(VerifySubject, map[string]any) (bool, string)` signature the three
  existing entries use). Both reuse only `VerifySubject.IsError`/`.Output`
  (no new subsystem): fail if `IsError`, else fail if `Output` contains a
  configurable `fail_marker` param (default `"FAIL"` for `tests_pass`,
  matching `go test`'s own top-level summary line; default `"error"` for
  `lint_pass`, matched case-insensitively). Six new unit tests in
  `workflow_step_executor_test.go` (clean pass, fail-marker match, subject
  error, and a custom-marker override for `tests_pass`; clean pass,
  case-insensitive fail-marker match, and subject error for `lint_pass`).
  This file is listed in the task's own Touches line as "read-only
  reference," which describes `Decide`'s read-only *use* of the
  `ExecuteLLMStep` call shape as a template — the task's own "What to do"
  #4 explicitly requires this file's `workflowEngineChecks` map to grow, so
  this edit is the task's own instruction, not scope creep beyond it.

**Two documented signature deviations from the design doc's bare
illustrative sketch** (`decide(goal, evaluation, history, budget) Decision`)
— the task pre-authorized one (`ctx`/`exec`) and I made a second, load-bearing
one myself, both documented in `decide.go`'s own top-of-file comment and
`Decide`'s doc comment, not just here:

1. `ctx context.Context, exec agentworkflow.StepExecutor` — exactly as the
   task calls out as expected, needed for the reasoning-fallback branch's
   `ExecuteLLMStep` call.
2. `policy ContinuationPolicy` (new type: `Provider`, `Model`, `AgentID`,
   `SessionID`, `Tools []string`) — **not explicitly named in the task's own
   given signature, added because the reasoning-fallback branch's
   `LLMStepRequest` hard-requires a real `Provider`/`Model`
   (`ExecuteLLMStep` errors without them) and no other given parameter
   carries that.** This is exactly the placeholder
   `internal/store/loop_runs.go`'s own `LoopRun.ContinuationPolicyJSON`
   doc comment names this task as the owner of ("task 07 defines and
   consumes its real shape without this table needing to be revisited") —
   I judged completing that reserved shape to be fulfilling the task's own
   instruction (build a real, callable, non-decorative `Decide`) rather than
   reopening its settled decision, per this dispatch's own "Decision vs.
   rationale" guidance. Documented in-code and here rather than left
   silent.

**Malformed-LLM-response fail-safe question** (flagged in this task's "Done
means" as the implementer's call): resolved as **return a non-nil error**,
per the reasoning given directly in this task's own dispatch instructions
(more consistent with `Decide` staying a pure-ish function whose caller,
task 08, handles the ESCALATE/error path explicitly) — implemented exactly
that way, tested, and documented in `parseReasoningVerdict`'s doc comment.

**Not done / left for task 08 (by design, per this task's own scope):**
`Decide` never queries `internal/store`, never calls `UpdateGoal` for a
REARCHITECT revision, never writes `loop_run_iterations`/`loop_runs` rows,
and never computes the per-iteration `Evaluation`/`ProgressState` rollup
from `Verify` results — all explicitly task 08's job per this task's own
Context section.

**Verification:** `go build ./cmd/nanite/`, `go vet ./...`, and
`go test ./... -count=1` all read directly from their own real, unpiped
output (redirected to files, checked by reading the file content, never
through a masked pipe). `go build`/`go test` are fully clean across the
whole repo (every package `ok`, including the new `internal/loop` package
and the two new `internal/service` test additions). `go vet ./...` reports
exactly the same two pre-existing, unrelated `internal/service/container.go`
findings (`stopReaper`/`stopRuntimeReaper` context-leak warnings) documented
repeatedly across this project's own `TASKS/ESCALATIONS.md` as predating
every batch back through Phase 1 — confirmed pre-existing here too by
`git log -1 -- internal/service/container.go` (last touched by an unrelated
Phase 6 task) and by `container.go` not appearing in `git status` against
this worktree's own changes.

## Review notes

PASS. Fresh review confirmed: deterministic-first order matches
21-loops.md exactly; Decide() genuinely does not route through
reflexes.Resolve (verified resolve.go:177 and taxonomy doc:120 directly);
verifyAgent's ExecuteLLMStep call shape is correctly reused (confirmed
pre-diff line 355 citation was accurate); malformed-LLM-response fail-safe
returns an error and never defaults to CONTINUE, tested by 6 dedicated
cases; reasoningEligibleKinds correctly excludes CONTINUE/COMPLETE, enforced
in code via a map lookup, tested for all 8 values; tests_pass/lint_pass
correctly reuse VerifySubject.IsError/.Output with no new subsystem,
6 dedicated tests added; doc.go's three-way disambiguation independently
verified against the real internal/loopdetect, internal/workflow.LoopStep,
and agentworkflow/dag.go|types.go files, not just trusted from the doc
comment. Both documented signature deviations (ctx/exec; policy
ContinuationPolicy) are well-reasoned — the ContinuationPolicy addition is
directly pre-authorized by internal/store/loop_runs.go's own
ContinuationPolicyJSON comment naming this task as its owner.

go build ./cmd/nanite/: exit 0. go vet ./...: exit 1, but only the two
pre-existing, unrelated internal/service/container.go findings (confirmed
via git log/git show against this commit's diff). go test -count=1 ./...:
all 93 packages ok, including internal/loop and internal/service.

Non-blocking follow-up logged, not fixed here: decide.go's
evidenceSatisfiesGoal re-implements store.EvaluateGoalEvidence's formula
locally rather than either (a) extracting a shared DB-free core function in
internal/store/goal_evidence.go, or (b) having Decide take a precomputed
bool from its caller (the option the task's own "What to do" §2 text
appears to prefer, and one that would have required zero internal/store
changes). Functionally identical to the original today, but nothing
guards against the two drifting apart if task 02's formula changes later
(e.g. when Result-vocabulary-aware evaluation lands, per goal_evidence.go's
own noted future work). Recommend task 08 or a fast-follow revisit this;
not a blocker for Phase 2 proceeding.
