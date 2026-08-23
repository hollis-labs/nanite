# Fix the test-fixture SQLite migration cost that blocks every `-race` run

**Phase:** Audit remediation — Wave 9 (follow-ups)
**Status:** not-started
**Depends on:** none. **Promotable ahead of its wave** — see Urgency.
**Blocks:** `08/08`'s deferred race gate; the Wave 2 `internal/service` race-suite follow-up; and in practice any future wave's aggregate race verification.
**Parallel-safe with:** most things — it touches test fixtures, not production code. Not with `13/03`.
**Touches:** test helpers that construct a store/`Container` and apply migrations; likely `internal/store`'s test setup and whatever `internal/service` and `internal/selftools` tests use to build fixtures. **No production code changes expected** — if the fix requires one, stop and say so.
**Gated on:** nothing.
**requires_regression_test:** false — the deliverable is a timing improvement, and the existing suite passing unchanged is the correctness bar.

---

> ## Urgency: this is no longer hygiene. It is blocking verification.
>
> Promoted from follow-up candidate to task on 2026-08-23, after the **fourth**
> occurrence. Consider running it before Wave 6 rather than in wave order.

---

## Context

### What is happening

Test fixtures repeatedly apply SQLite migrations. Every package whose tests
construct a store pays that cost per fixture, and under `-race` — which
slows execution substantially — it compounds until the suite cannot finish.

### The four occurrences

| Wave | Symptom |
|---|---|
| 2 | `internal/service` combined-package `-race` timeout, logged "real, pre-existing, non-blocking" |
| 3 | Focused `internal/selftools` race run took **1,446.675s**, passing only under a 30-minute timeout |
| 3 | `08/08` closed **`implemented`, not `reviewed`** — the operator deferred its full-race gate rather than authorize longer runs |
| 5 | **Both** aggregate `internal/service` and `internal/selftools` race suites timed out in migration setup at 20 minutes. No race report emitted; neither recorded as passing |

### Why it matters more than the individual symptoms suggest

Wave 5 was the batch's largest refactor — `10/01` decomposed a 3,684-line
function into a six-action pipeline, `10/02` restructured a 2,481-line
transport into four selective owners. **Concurrent-correctness changes of
precisely the kind `-race` exists to validate, with no aggregate race verdict
for either.**

Focused race suites did pass, and Wave 5's handoff is careful that focused
passes do not convert an aggregate timeout into a pass. But the trend is
one-directional: the suites have gone from slow, to needing a 30-minute
timeout, to not completing. Waves 6–8 will hit the same wall.

### Desired invariant

**`go test -race ./...` completes.** Not quickly — completes, and emits a
verdict, within a timeout a person will actually wait for.

## The cause is confirmed, and the fix already exists in this repo

**Found 2026-08-23, before dispatch.** This task was written as
measure-then-decide. That work is done, and the answer narrows it
considerably — read this before the (retained) measurement step.

**`internal/store`'s own tests already solved this.**
`internal/store/store_test.go:24-70`:

- `testStoreTemplate` uses a `sync.Once` to build **one** migrated database in a
  temp dir, once per test binary.
- `newTestStore` then **copies that template file** into the test's own
  `t.TempDir()` and opens it.

Full migrations run once; every fixture after that is a file copy.

**The packages that time out do not use it.** `internal/service` has its own
`newTestStore` (`internal/service/a2a_gate_integration_test.go:205`) that calls
`store.New(...)` directly — a full migration run per fixture. Repo-wide there
are **96 direct `store.New(` calls in `_test.go` files**, across
`internal/service`, `internal/selftools`, `internal/api`, `internal/chat`,
`internal/skill`, `internal/skillinstall` and others.

That is the cost, and it explains the pattern exactly: `internal/store`'s own
race suite is fine, while the packages that build stores *through* it are the
ones that cannot finish.

**So this is an adoption task, not an invention task.** Lift `internal/store`'s
template helper into something the other packages can use — a small exported
test helper, or `internal/storetest`, or whatever fits this repo's conventions
— and migrate the 96 call sites onto it. There is a proven in-repo pattern to
copy; do not design a new one.

**The one hazard to respect:** a shared *template* must not become shared
*state*. `internal/store` gets this right — the template is read-only and each
test copies it to its own temp file. Any adoption that has tests share one live
database is a test-isolation bug, not a speedup, and this batch has already had
one of those (`08/10` writing synthetic memories into the operator's real
Tesseract database).

## What to do

### 1. Measure before optimising

Establish where the time goes before changing anything. The Wave 3
attribution — repeated migration application — is credible and specific, but it
is an attribution, not a profile.

```bash
go test -race ./internal/selftools/ -count=1 -v 2>&1 | tail -40
go test ./internal/store/ -count=1 -run TestNothingMatchesThis -v   # fixture setup cost alone
```

Record the starting numbers. They are the only way to know whether the fix
worked, and this task's own "done" is a timing claim.

### 2. Likely approaches, in rough order of value

**Primary approach, per the section above: adopt `internal/store`'s existing
template-copy helper across the 96 `store.New(` test call sites.** Everything
below is fallback, for use only if measurement contradicts the diagnosis:

- **In-memory with a shared cache** where a test needs no file durability.
- **Serialise the migration-heavy packages** rather than making them faster —
  Wave 3's named fallback. Weakest option; it manages the symptom.

If the measurement shows the cost is *not* dominated by migration application,
**stop and report** — the diagnosis above would be wrong and the approach with
it.

### 3. Do not change what the tests assert

The bar is the existing suite passing **unchanged**. A fixture that is faster
because it applies fewer migrations, or shares state it should not, is not a
fix — it is a test-isolation bug, and this batch already had one of those
(the `08/10` incident that wrote synthetic memories into the operator's real
Tesseract database).

**Related and worth folding in if cheap:** follow-up candidate 6 — tests that
construct a `Container` must prove *all* independently resolved stores are
redirected, not just the one they remembered. If you are already restructuring
fixture setup, that is the natural moment.

## Done means

- `go test -race ./...` **completes and emits a verdict** within a timeout a
  person will wait for. State the before and after numbers.
- **Zero remaining `store.New(` calls in `_test.go` files** that could use the
  shared template helper — or each remaining one justified in the Work log.
- Test isolation preserved: every fixture still gets **its own** database file,
  not a shared live one. Say how this was verified.
- `go test ./...` and `go test -race ./...` both pass, with the suite's
  assertions unchanged.
- `internal/selftools` and `internal/service` aggregate race suites — the two
  that failed in Wave 5 — specifically confirmed completing.
- **`08/08` re-run and promoted to `reviewed`**, since its deferred gate is now
  runnable. Update `findings.json` for `GO-SEC-001` and `GO-SEC-002`.
- The Wave 2 `internal/service` race-suite follow-up closed in
  `TASKS/ESCALATIONS.md`, and candidate 4 struck from
  `14-followups/README.md`'s register.
- If test isolation was touched, say explicitly whether candidate 6 was
  addressed or deliberately left.

## Work log

## Review notes
