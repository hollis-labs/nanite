# Fix internal/api test suites' Container shutdown leak (no test calls Container.Shutdown())

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none — a test-only, mechanical change independent of task 01 in this folder (constructor cleanup) and task 03 (a *different*, unsolved problem — see below).
**Touches:** `internal/api/artifacts_test.go`, `internal/api/loom_curator_wake_test.go`, `internal/api/providers_test.go` (4 call sites), `internal/api/tools_call_test.go`, `internal/api/recovery_test.go`, `internal/api/api_test.go`. Possibly `internal/service`'s own test suite, per the audit's recommendation — see "Scope" below for why this task treats that as **out of scope in practice**, deferred to task 03.
**Requires architect decision:** false — mechanical test-hygiene fix, no design ambiguity.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2a`
> - **Depends on:** `00/01`
> - **Blocks:** `04/03`
> - **Parallel-safe with:** `04/01`, `04/04`, `04/05`, `05/01`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

This task implements **`GO-TEST-001`** (medium severity, high confidence — `docs/audits/2026-08-21-go-quality/REPORT.md:538-589`, and referenced again in §7's testing baseline, `REPORT.md:519-536`).

**This is explicitly distinct from task 01 (`01-fix-container-constructor-partial-failure-cleanup.md`) and task 03 (`03-investigate-internal-service-race-timeout.md`) in this same folder — do not conflate them.** Per the remediation guide's own instruction (guide "Container/reaper lifecycle" subsection, Wave 2): keep distinct (1) constructor partial-failure cleanup [task 01], (2) API tests that create Containers without shutdown [this task], and (3) `internal/service`'s own race timeout, whose cause was explicitly found to be *different* from (2) [task 03]. This task is purely about (2).

### Findings addressed
- `GO-TEST-001` — `internal/api` test suites construct many `service.Container` instances via `NewContainer` and never call `Shutdown()` on any of them.

### Root cause
`internal/api`'s test files that stand up a real `service.Container` for integration-style testing follow a consistent pattern: construct a `store.Store`, register `t.Cleanup(func() { s.Close() })` for it, then construct a `service.Container` via `service.NewContainer(...)` — but never register any cleanup for the `Container` itself. Each successful `NewContainer` call starts 2 permanently-running background goroutines (the subagent reaper and the `agent_runtime` reaper, each on its own DB-polling ticker — see task 01's Context for how these are started). With no `Shutdown()` call, those goroutines outlive the individual test and keep running for the rest of that test binary's process — across dozens of tests in a 46-test-file package, they accumulate.

### Current behavior (verified against current `HEAD`)
- Confirmed via direct `grep -n "NewContainer\|t.Cleanup\|Shutdown()"` against all 6 files: every one calls `service.NewContainer(...)` at least once, every one registers `t.Cleanup(func() { s.Close() })` for the store, and **none** register any cleanup that touches the returned `Container`/`svc` value. Call sites (current line numbers, verify before editing since the audit's own baseline is `8feeee5c` and these files may have shifted):
  - `internal/api/artifacts_test.go:95` (`svc, err := service.NewContainer(...)`), store cleanup at line 85.
  - `internal/api/loom_curator_wake_test.go:100`, store cleanup at line 75.
  - `internal/api/providers_test.go` — **4 separate call sites**: lines 52, 99, 153, 194 (each with its own local store cleanup at lines 76 and similarly-scoped points for the others — verify per-site, they are not all sharing one setup).
  - `internal/api/tools_call_test.go:33`, store cleanup at line 31.
  - `internal/api/recovery_test.go:31`, store cleanup at line 29.
  - `internal/api/api_test.go:28`, store cleanup at line 26.
- Goroutine-dump evidence from the audit: in the original 10-minute `-race` timeout dump, `internal/api`'s goroutine dump alone contained 56 goroutines traced to `internal/service.NewContainer` reaper-start calls (verified via line-range bucketing against the other 4 packages' dumps in that run — zero elsewhere). In a 25-minute re-run, the combined `internal/api` + `internal/service` timeout dump contained 288 such goroutines (up from 56 at 10 minutes) — consistent with accumulation growing across the run, not a fixed one-time cost.
- **Confirmed NOT a production defect.** `cmd/nanite/main.go:747` calls `container.Shutdown()` on the real shutdown path (verified current), wired through `daemonLifecycle.Shutdown` — the real server tears down correctly. This leak is specific to the test suites never calling the already-correct, already-existing cleanup API.
- Practical effect: `go test -race ./internal/api` cannot complete even at 25 minutes (2.5x Go's default 10-minute per-package timeout) — nobody can currently get a real "is `internal/api` race-clean?" verdict.

## What to do

### Desired invariant
Every test-constructed `service.Container` is torn down via `Shutdown()` in the same test that created it, symmetrically with the existing store cleanup pattern already in place. No test-only code path should leave a reaper goroutine running past the end of its own test function.

### Scope
The 6 `internal/api` test files listed above and in **Touches**, at the specific call sites cited. **`internal/service`'s own test suite is explicitly excluded from this task's scope**, despite the audit's `GO-TEST-001` recommendation text saying to "check `internal/service`'s own test suite for the same gap in its home package" — a *later*, more precise finding (`GO-SVCCORE-006`, task 03 in this folder) confirmed via exhaustive grep that **zero of `internal/service`'s 126 test files ever call `NewContainer`**, so there is no such gap to fix there. `internal/service`'s own `-race` timeout is a real, separate, unsolved problem — see task 03.

### All production callers
Not applicable in the security/correctness-migration sense (this is test-only code, not a shared production primitive) — but for completeness: `internal/api`'s production request-handling code does not itself call `NewContainer`; only its test setup does. The one production caller of `NewContainer` (`cmd/nanite/main.go:372`) already calls `Shutdown()` correctly (see above) and needs no change.

### Proposed direction
Add `t.Cleanup(func() { container.Shutdown() })` (using whatever local variable name each call site already uses for the `*service.Container` return value — `svc` in most of these files, verify per-site) immediately alongside the existing store cleanup, at each of the 6 call sites (9 total registrations, since `providers_test.go` has 4 independent call sites each needing its own `t.Cleanup`). Register it right after the `NewContainer` call succeeds (after the `if err != nil { t.Fatalf(...) }` guard), mirroring where the store's own `t.Cleanup` is registered relative to `store.Open`/equivalent.

### Non-goals
- Do not change `service.Container.Shutdown()`'s implementation itself.
- Do not attempt to fix `internal/service`'s own `-race` timeout here (task 03).
- Do not attempt to fix the constructor partial-failure leak here (task 01) — this task only concerns *successfully constructed* Containers.
- Do not restructure these test files' setup/teardown patterns beyond adding the missing cleanup call.

### Dependencies
None on other tasks in this folder — independently landable. (It is sequenced before task 03 only in the sense that fixing this first removes it as a *candidate explanation* for `internal/service`'s timeout, which task 03 already treats as ruled out by direct evidence rather than depending on this task's completion — see task 03's Context.)

### Tests required
- No new test *logic* is required — this is a fix to test infrastructure/cleanup, not new coverage. The verification is behavioral: does the existing suite complete under `-race` within the default timeout afterward.
- If a specific call site's `NewContainer` invocation is reused across multiple subtests via a shared helper, confirm the `t.Cleanup` registration happens once per actual `Container` construction, not once per subtest sharing a single Container (over-registering `Shutdown()` on an already-shut-down Container should be checked for idempotency — see `Shutdown()`'s own implementation before assuming multiple calls are safe; if it isn't idempotent, this is a real finding to flag, not silently work around).

### Prevention
Consider a lint rule, code-review checklist item, or a shared test helper (e.g. a `newTestContainer(t *testing.T) *service.Container` wrapper in an `internal/api` test-support file that registers the `Shutdown()` cleanup internally, so future test files can't reproduce this gap by construction) — a shared helper is the stronger prevention mechanism since it removes the possibility of forgetting the cleanup call entirely, rather than relying on every future test author remembering the pattern. This maps to the remediation guide's "Lifecycle Ownership" standard (Wave 7): "Every goroutine/background worker/resource has an explicit owner and shutdown path."

### Verification
**This is the actual acceptance test — cite it explicitly as the Done means gate:** after the fix, `go test -race ./internal/api` must complete within the **default 10-minute timeout** (no `-timeout` override) with **zero `DATA RACE` reports**. This is a stronger bar than merely "doesn't time out" — a completed run could still surface real data races that were previously masked by the timeout; those would be new findings to report, not silently ignored.

### Risk / rollback
Very low risk — purely additive test cleanup code, no production code touched, no behavioral change to any test's assertions. Rollback is a straightforward revert.

### Done means
- [ ] All 9 `NewContainer` call sites across the 6 named `internal/api` test files register a `t.Cleanup(func() { <container>.Shutdown() })` alongside the existing store cleanup.
- [ ] `go test -race ./internal/api` completes within the default 10-minute timeout (verify by running without a `-timeout` override) with zero `DATA RACE` reports.
- [ ] If `go test -race ./internal/api` surfaces any real `DATA RACE` reports once it can actually complete, those are logged as new findings (not silently fixed inline as part of this task, unless trivial) and escalated per this batch's normal process.
- [ ] `go build ./...` and `go test ./internal/api/...` (non-race) remain clean.

## Work log

<!-- Worker fills this in as it goes. -->

## Review notes

<!-- Reviewer fills this in. -->
