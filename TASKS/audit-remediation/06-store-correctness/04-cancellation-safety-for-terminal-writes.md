# Fix `06/03`: terminal-outcome store writes must survive their operation's cancellation

**Phase:** Audit remediation — fix task for `06/03`
**Status:** complete
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

- Reproduced `TestWorkflowLauncher_Launch_RespectsTimeout` failing with
  `persist result: ... context deadline exceeded` before the fix.
- Audited all 50 named non-test sites individually: **29 detached** terminal
  outcome writes and **21 deliberately unchanged** sites.
- Added
  `TestBuiltinWorkflowEngine_PersistWorkflowRunStepOutcome_SurvivesCancelledContext`.
  Its executor cancels the workflow context before the terminal upsert; the
  stored row is then read back and asserted `failed` with the cancellation
  outcome.
- Mutation check passed: replacing the terminal upsert's
  `context.WithoutCancel(ctx)` with `ctx` makes the new test fail with
  `context canceled`; restoring it makes the test pass.
- No timeouts were added to detached writes. They retain context values and
  shed only cancellation/deadline through `context.WithoutCancel`.
- Found and corrected one underlying `06/03` marker defect at
  `chatServiceImpl.recordUtilityMetrics`: the AST sweep had split
  `context.TODO()` across lines and displaced the following function comment
  inside the call. The restored marked call changes the truthful final
  `TODO(ctx-sweep)` count from 245 to **246**; all 246 `context.TODO()` calls
  now have markers.
- Bugs noticed but deliberately not fixed: none beyond the in-scope
  cancellation regression and marker-expression repair above.

### Site dispositions (50/50)

| # | Method and site | Disposition and reason |
|---:|---|---|
| 1 | `LogEvent` — `cmd/nanite/admin_export_decisions.go:158` | **Left:** the event row is the export payload itself; cancellation should stop the export. |
| 2 | `LogEvent` — `internal/agent/reflexes/telemetry.go:279` | **Detached:** records a reflex firing after the action resolves. |
| 3 | `LogEvent` — `internal/api/sessions.go:241` | **Left:** already uses marked `context.TODO()`; no cancellable operation context is available. |
| 4 | `LogEvent` — `internal/recovery/orphansweep/orphan_sweep.go:171` | **Left:** already uses marked `context.TODO()` in post-reconciliation logging. |
| 5 | `LogEvent` — `internal/scheduler/telemetry.go:246` | **Detached:** records the completed schedule dispatch attempt outcome. |
| 6 | `LogEvent` — `internal/selftools/reactions/telemetry.go:148` | **Detached:** records the completed self-tool reaction outcome. |
| 7 | `LogEvent` — `internal/selftools/self_tools_dispatch.go:194` | **Detached:** records the broker decision failure returned by `Decide`. |
| 8 | `LogEvent` — `internal/selftools/self_tools_dispatch.go:202` | **Detached:** records the successful broker decision returned by `Decide`. |
| 9 | `LogEvent` — `internal/service/agent_deps.go:644` | **Left:** generic runtime-store adapter with no outcome semantics of its own; originating callers choose the context. |
| 10 | `LogEvent` — `internal/service/chat_generate.go:1136` | **Detached:** records the provider failure after overflow recovery is refused. |
| 11 | `LogEvent` — `internal/service/chat_generate.go:1196` | **Detached:** records the completed provider-stream failure. |
| 12 | `LogEvent` — `internal/service/chat_generate.go:1505` | **Detached:** records the provider stream's terminal inactivity timeout. |
| 13 | `LogEvent` — `internal/service/chat_generate.go:1594` | **Detached:** records the provider response's terminal max-token truncation. |
| 14 | `LogEvent` — `internal/service/chat_generate.go:1772` | **Detached:** records the completed response-envelope parse outcome. |
| 15 | `LogEvent` — `internal/service/chat_generate.go:3146` | **Left:** announces an envelope retry before the retry operation runs. |
| 16 | `LogEvent` — `internal/service/chat_reflexes.go:25` | **Detached:** records the completed reflex-evaluation failure. |
| 17 | `LogEvent` — `internal/service/chat_tool_executor.go:538` | **Detached:** records the failed tool-call outcome. |
| 18 | `LogEvent` — `internal/service/chat_tool_executor.go:546` | **Detached:** records the successful tool-call outcome. |
| 19 | `LogEvent` — `internal/service/chat_tool_executor.go:790` | **Detached:** records the completed tool-result truncation outcome. |
| 20 | `LogEvent` — `internal/service/container.go:993` | **Detached:** records the same terminal halt persisted immediately before it. |
| 21 | `LogEvent` — `internal/service/recovery_pack_glue.go:92` | **Left:** already uses marked `context.TODO()`; no cancellable parent context exists. |
| 22 | `LogEvent` — `internal/subagent/service.go:694` | **Detached:** records the terminal rejection of a recursive spawn. |
| 23 | `LogEvent` — `internal/subagent/service.go:797` | **Left:** records trusted approval bypass before the subagent dispatch continues. |
| 24 | `RecordScheduleRunAttempt` — `internal/scheduler/retrying_runner.go:223` | **Detached:** persists the completed successful dispatch attempt. |
| 25 | `RecordScheduleRunAttempt` — `internal/scheduler/retrying_runner.go:246` | **Detached:** persists terminal retry exhaustion after dispatch failure. |
| 26 | `RecordScheduleRunAttempt` — `internal/scheduler/retrying_runner.go:258` | **Detached:** persists the failed attempt and next retry window after dispatch. |
| 27 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine.go:133` | **Left:** pre-registers a pending step before execution. |
| 28 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine.go:383` | **Detached:** persists a terminal skipped result after dependency failure. |
| 29 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine.go:536` | **Left:** marks running before step execution. |
| 30 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine.go:543` | **Left:** marks the nonterminal `waiting_on_gate` transition. |
| 31 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine.go:561` | **Left:** marks the nonterminal `waiting_on_flex` transition. |
| 32 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine.go:599` | **Detached:** persists the terminal LLM/tool step result. |
| 33 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine_flex.go:179` | **Detached:** persists the terminal flex recheck resolution. |
| 34 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine_loop.go:362` | **Detached:** persists a loop launch that resolved terminally inline. |
| 35 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine_loop.go:372` | **Left:** marks the nonterminal `waiting_on_loop` transition. |
| 36 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine_loop.go:387` | **Detached:** terminal failure policy is explicit via `WithoutCancel(TODO)` while retaining the sweep marker for later context plumbing. |
| 37 | `UpsertWorkflowRunStep` — `internal/service/workflow_engine_loop.go:459` | **Detached:** persists the terminal loop recheck resolution. |
| 38 | `SetDurableAgentInstanceStatus` — `internal/service/durable_agents.go:335` | **Left:** `starting` is a pre-launch, nonterminal transition. |
| 39 | `SetDurableAgentInstanceStatus` — `internal/service/durable_agents.go:396` | **Left:** `resume_requested` precedes resume work and is nonterminal. |
| 40 | `SetDurableAgentInstanceStatus` — `internal/service/durable_agents.go:514` | **Left:** `stop_requested` precedes runtime shutdown and is nonterminal. |
| 41 | `RecordExecutionMetrics` — `internal/service/chat_generate.go:2004` | **Detached:** records metrics for the completed chat turn. |
| 42 | `RecordExecutionMetrics` — `internal/service/chat_generate.go:3445` | **Left:** utility-call metrics already use marked `context.TODO()` because the helper has no ambient context. |
| 43 | `SetAgentRuntimeState` — `internal/service/agent_deps.go:588` | **Left:** adapter callback already uses marked `context.TODO()` and has no ambient operation context. |
| 44 | `SetAgentRuntimeState` — `internal/service/agent_deps.go:671` | **Left:** manager state-sink callback already uses marked `context.TODO()`. |
| 45 | `MarkAgentRuntimeFailed` — `internal/service/agent_deps.go:578` | **Left:** runtime adapter callback already uses marked `context.TODO()`. |
| 46 | `MarkAgentRuntimeOrphaned` — `internal/service/agent_deps.go:637` | **Left:** orphan adapter callback already uses marked `context.TODO()`. |
| 47 | `MarkSessionHalted` — `internal/service/container.go:985` | **Detached:** persists the terminal halt even if the reflex context is cancelled. |
| 48 | `CompleteLoopRunIteration` — `internal/loop/engine.go:639` | **Detached:** persists the completed iteration decision after evaluation. |
| 49 | `MarkReminderFired` — `internal/reminders/engine.go:158` | **Left:** already uses marked `context.TODO()`; the reminder engine has no ambient cancellable context. |
| 50 | `RecordUsage` — `internal/service/chat_generate.go:1966` | **Detached:** records token usage for the completed chat turn. |

### Verification

- `go build ./...`: pass.
- `go vet ./...`: exactly four pre-existing findings in
  `internal/service/container.go` (`stopReaper` and `stopRuntimeReaper` not
  used on all paths, plus their paired reachable return findings); no fifth.
- `go test ./...`: pass on the final tree.
- `go test ./internal/service/ -run TestWorkflowLauncher_Launch_RespectsTimeout -count=3`:
  pass, three consecutive runs.
- `go test -race ./internal/store/... ./internal/service/... ./internal/api/...`:
  pass.
- Store oracles: non-context `Query` = 0, `Exec` = 0, `QueryRow` = 0.
- `gofmt -l` on all touched Go files: no output. `git diff --check`: pass.
- No `internal/store/` signature was changed by this follow-up.

## Review notes

**2026-08-22 — planner verification pass (not a full code review).** Every
acceptance criterion re-measured independently rather than accepted from the
summary: store SQL oracles 0/0/0; 371 total exported methods, 371 with `ctx`, 0
without (identical to `06/03`, so no signature drift); `go vet` exactly the 4
pre-existing `container.go` findings; `go test ./...` **0 FAIL / 94 ok** — the
criterion that failed the prior round; disposition table exactly 50 rows
splitting 29 detached / 21 left, matching the reported figures; regression test
present at `internal/service/workflow_launch_test.go:232`; 246 `context.TODO()`
calls against 246 markers, exact parity; `recordUtilityMetrics`' displaced doc
comment confirmed correctly repositioned at `chat_generate.go:3429`.

One figure reconciled: 24 `context.WithoutCancel` calls against 29 detached
sites, explained by `persistCtx`, `finalCtx`, and `toolOutcomeCtx` each being
declared once and reused.

Judgment quality spot-checked across the disposition table and found sound —
the 21 "left" calls show the rule being reasoned about rather than
pattern-matched (an envelope-retry announcement left because it precedes the
retry; only 2 of 11 `UpsertWorkflowRunStep` sites detached, the rest correctly
identified as pre-registration or nonterminal `waiting_on_*` transitions).

**Limits of this pass, stated plainly:** 381 changed files were not reviewed
line by line. Deep review is deferred by operator decision. Status is
`validated`, not `reviewed`, to reflect that accurately.


