# Fix `06/03`: terminal-outcome store writes must survive their operation's cancellation

**Phase:** Audit remediation — fix task for `06/03`
**Status:** not-started
**Depends on:** `06/03`'s working tree. **This is not a fresh start** — it builds directly on the uncommitted sweep. Do not revert it, do not re-run it.
**Blocks:** everything `06/03` blocks. The sweep cannot land until this closes.
**Parallel-safe with:** nothing, same as `06/03`.
**Touches:** call sites in `internal/service/`, and wherever else the audit below leads. **No changes to `internal/store/` signatures** — those are correct as swept.
**requires_regression_test:** true — one test, named below.

---

> ## You just finished `06/03`. This is its fix task, and the context is the same.
>
> The sweep itself verified clean and I re-measured it independently: 265 → 0
> non-context calls, 141 → 406 context calls (exact conservation — every call
> converted, none added or lost), 237 → 0 methods without `ctx`, `go vet`
> exactly the 4 expected `container.go` findings, 245 markers, zero
> `context.Background()` in non-test files. Your correction to my baseline was
> right: my grep ended at the opening paren so it counted all 371 exported
> methods rather than the 237 lacking `ctx`.
>
> **But `go test ./...` does not pass.** That claim in the summary doesn't
> hold — `internal/service` fails deterministically, 3 of 3 runs. Please re-run
> the full suite yourself before reporting on this one.

## The failure

```
--- FAIL: TestWorkflowLauncher_Launch_RespectsTimeout (1.46s)
    workflow_launch_test.go:209: Launch: unexpected error
      workflow: run "slow-workflow": agentworkflow: step "only":
      persist result: upsert workflow_run_steps <id>:only: context deadline exceeded
```

Repro: `go test ./internal/service/ -run TestWorkflowLauncher_Launch_RespectsTimeout -count=1`

## Root cause — and why it is not a test problem

`internal/store/workflow_runs.go:252` was swept correctly:

```go
-func (s *Store) UpsertWorkflowRunStep(row *WorkflowRunStepRow) error {
-	_, err := s.DB.Exec(
+func (s *Store) UpsertWorkflowRunStep(ctx context.Context, row *WorkflowRunStepRow) error {
+	_, err := s.DB.ExecContext(ctx,
```

The signature and the variant swap are both right. The problem is at the **call
sites**, which pass the *workflow's own* context — the one carrying the
deadline the workflow just exceeded.

`UpsertWorkflowRunStep` records **what happened to a step, after the step ran**.
So when a workflow times out, the write that records the timeout is itself
cancelled by that same timeout. **The record of the failure is destroyed
precisely when it is most needed.** Before the sweep this was structurally
impossible, because the write had no context to be cancelled by.

Do **not** fix this by changing the test, and do **not** fix it by reverting
`UpsertWorkflowRunStep` to `Exec`. Both hide a real defect the sweep exposed.

## The rule

> **A store write that records the *outcome* of an operation must not be
> cancellable by that operation's own context.**

Everything else keeps the swept context, unchanged. A `SELECT` serving a
request should absolutely die with that request — that is the whole point of
the sweep. This carve-out is narrow and applies only to writes that record
terminal state.

The fix at each qualifying site:

```go
// The step outcome must persist even when the workflow's own deadline
// has expired — otherwise a timed-out run loses the record of its timeout.
persistCtx := context.WithoutCancel(ctx)
if err := e.store.UpsertWorkflowRunStep(persistCtx, &store.WorkflowRunStepRow{...}); err != nil {
```

`context.WithoutCancel` (Go 1.21+) keeps the context's values — trace IDs,
request-scoped data — while detaching cancellation and deadline. That is
exactly the semantics wanted here, and it is why this is preferable to
`context.Background()`, which would silently drop trace propagation.

**If a site genuinely needs its own bound** (a write that must not hang
forever), use `context.WithTimeout(context.WithoutCancel(ctx), <short>)` rather
than inheriting the dead parent. Pick a duration only if you can justify it in
the Work log; otherwise plain `WithoutCancel`.

## Scope — a bounded audit, not a sweep

**Start here.** `UpsertWorkflowRunStep` has 11 non-test call sites, all in the
workflow engine, and it is the confirmed failure:

```
internal/service/workflow_engine.go:133, 382, 535, 542, 560, 597
internal/service/workflow_engine_flex.go:178
internal/service/workflow_engine_loop.go:361, 371, 385, 456
```

Note `workflow_engine_loop.go:385` already carries a `context.TODO()` marker
from the sweep — that one is unaffected by cancellation today, but it should
get the same treatment for consistency once a real context is plumbed there.
Leave the marker; just note it.

**Then audit these**, which record terminal state by name. 50 non-test call
sites total across the twelve — small enough to inspect each one by hand:

| Method | Sites | | Method | Sites |
|---|---:|---|---|---:|
| `LogEvent` | 23 | | `RecordScheduleRunAttempt` | 3 |
| `UpsertWorkflowRunStep` | 11 | | `SetDurableAgentInstanceStatus` | 3 |
| `RecordExecutionMetrics` | 2 | | `SetAgentRuntimeState` | 2 |
| `MarkAgentRuntimeFailed` | 1 | | `MarkAgentRuntimeOrphaned` | 1 |
| `MarkSessionHalted` | 1 | | `CompleteLoopRunIteration` | 1 |
| `MarkReminderFired` | 1 | | `RecordUsage` | 1 |

**Judge each site individually against the rule.** The method name is a
starting heuristic, not the answer — `LogEvent` at 23 sites will not uniformly
qualify. The question is always: *does this write record the outcome of the
very operation whose context it is using?* If the write happens before or
during the operation, leave it swept. If it records how the operation ended,
detach it.

There are **zero** uses of `context.WithoutCancel` in the tree today, so you
are establishing the pattern. Keep the comment style consistent across sites so
they are greppable later.

### Why this audit must be done by hand, not by running the tests

Exactly **one** of the 50 sites had a test that caught this. The regression is
real at that site and *latent* at any other that qualifies — there is no test
that will go red to tell you. `go test ./...` passing after your fix is
necessary but **not** sufficient evidence that you found them all.

That is why the Done-means below asks for a written disposition per site rather
than a green suite. Treat the test run as a floor, not as the audit.

(Test coverage is a known, tracked weakness in this codebase and is on the
roadmap separately — the audit this batch remediates found whole files at 0%
coverage. Do not scope any of that in here.)

## What NOT to do

- **Do not** change any `internal/store/` signature. The sweep is correct.
- **Do not** revert any `ExecContext`/`QueryContext`/`QueryRowContext` back to
  its non-context form.
- **Do not** modify `TestWorkflowLauncher_Launch_RespectsTimeout` to make it
  pass. It is asserting correct behaviour and it caught a real regression.
- **Do not** wrap every site in the table reflexively. An over-broad
  `WithoutCancel` is its own bug — it makes writes uninterruptible that should
  be interruptible.
- **Do not** widen scope beyond cancellation safety. Same fence as `06/03`: no
  bug fixes, no renames, no cleanups. Log anything you notice.
- **Do not** add test coverage beyond the one required regression test below,
  however tempting the gaps look. Coverage is tracked separately.

## Verification

```bash
go build ./...
go vet ./...                       # still exactly 4 container.go findings, no fifth
go test ./...                      # MUST be fully green — this is the criterion that failed
go test ./internal/service/ -run TestWorkflowLauncher_Launch_RespectsTimeout -count=3
go test -race ./internal/store/... ./internal/service/... ./internal/api/...

# The sweep's own oracles must still hold — confirm you didn't regress them:
grep -rhoE '\.Query\(' --include='*.go' --exclude='*_test.go' internal/store/ | wc -l   # 0
grep -rhoE '\.Exec\(' --include='*.go' --exclude='*_test.go' internal/store/ | wc -l    # 0
grep -rhoE '\.QueryRow\(' --include='*.go' --exclude='*_test.go' internal/store/ | wc -l # 0
```

Note the grep form above is corrected — `06/03`'s version used `-h -o` before
`grep -v _test`, which strips filenames first and so never excluded test files.
Use `--exclude` as shown.

### Required regression test

Add one test asserting the rule directly, not just that the existing test
passes: **a workflow step whose context is already cancelled still persists its
outcome row.** Cancel the context, call the persist path, then read the row
back and assert it exists with the expected terminal state. Put it next to the
existing workflow launcher tests.

## Done means

- `go test ./...` fully green, verified by you on the final state.
- `go vet ./...` reports exactly the 4 pre-existing `container.go` findings.
- All three sweep oracles still return 0.
- Every one of the 50 audited sites has an explicit disposition — detached or
  deliberately left alone — recorded in the Work log with one line of reasoning
  each. A bare count is not enough; the reasoning is the deliverable.
- The regression test above exists and fails if `WithoutCancel` is removed
  (verify by removing it once, watching it fail, and putting it back).
- No `internal/store/` signature changed from `06/03`'s state.

## Work log

## Review notes
