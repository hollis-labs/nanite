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

Not prescriptive — pick against what the measurement shows:

- **Migrate once per package, not per fixture.** A `TestMain` or `sync.Once`
  that applies migrations to a template database, with each test copying or
  transacting against it.
- **Template-database snapshot.** Apply migrations once, snapshot the file,
  and have each fixture copy the bytes. Usually far cheaper than replaying DDL.
- **In-memory with a shared cache** where the test does not need file
  durability.
- **Serialise the migration-heavy packages** rather than making them faster —
  the fallback Wave 3 named. Weakest option: it manages the symptom.

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
