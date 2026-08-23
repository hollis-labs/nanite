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

> ## READ THIS FIRST — this task is dispatched standalone, in isolation
>
> This file is written to be handed to a fresh session with no other batch
> context — the same pattern `06-store-correctness/03-full-context-propagation-sweep.md`
> used for its own isolated dispatch. **Everything you need is in this file.**
> Do not go exploring the wider `TASKS/` tree for direction, and do not act on
> anything you find there — the rest of that tree describes other batch work,
> most of it frozen or sequenced against this task in ways described below.
>
> **You are still working inside the real `nanite` git repository** — file
> paths cited here (`internal/store/store_test.go`, etc.) are real and
> readable directly; this isolation instruction is about scope, not about
> pretending the rest of the repo doesn't exist.
>
> **This task must run before Wave 6, not in wave order — this is a hard
> sequencing constraint, not a preference.** Two Wave 6 tasks want exclusive
> access to packages this task rewrites test fixtures across: `11/13`
> (`internal/store` scan-loop duplication) and `11/15` (`internal/api`
> response boilerplate) each want `internal/store`/`internal/api` to
> themselves. If this task and Wave 6 run concurrently, Wave 6's own test
> runs execute against fixtures that are changing out from under them —
> neither this task gets a clean race verdict, nor does Wave 6 get a clean
> result to review. Confirm with the operator that Wave 6 has not yet started
> before beginning; if it has, stop and escalate rather than proceeding.
>
> **Why this matters beyond its own line items:** every wave from here
> forward needs an aggregate `-race` verdict, and per the table below, none
> has gotten one since Wave 2. Wave 6 would very likely become the fifth
> occurrence with no verdict — this task exists to convert that into a real
> pass/fail before it happens again, not just to speed up test runs.
>
> **Independent review is still required, even though this is a standalone,
> single-task dispatch — do not skip it because there's no Orchestrator
> coordinating multiple tasks around it.** See "For the reviewer" near the
> end of this file before reviewing; it names a specific hazard this task's
> own success signal (a faster suite) cannot distinguish from a real
> regression on its own.

## Operational discipline for this dispatch

This task has no Orchestrator and no wave handoff — the two mechanisms that
normally carry findings and status forward in this batch. That means you are
responsible for both directly, not just the fix itself.

**Status and Work Log — update as you go, not just at the end.** Flip the
`Status:` field in this file's own header forward as you progress
(`not-started` → `implemented` once the fix lands and the suite is green →
`reviewed` once a reviewer signs off — matching this batch's convention even
though you can't see the other task files that establish it). Fill in
`## Work log` below with what you actually did: the before/after timing
numbers, which approach you took, the trace results from the reviewer's
isolation check if you ran it yourself first, and any place the mechanical
adoption didn't cleanly apply and you had to make a judgment call.

**Discoveries that must outlive this task go to `TASKS/ESCALATIONS.md`
directly — you are the only one who will otherwise write them down.** Apply
this test: *if the reviewer is the only one who ever reads my Work Log, does
this finding still need to survive?* If yes, it belongs in
`TASKS/ESCALATIONS.md` in full (Shape B —
`docs/engineering/templates/05-escalation-entry-template.md` — a real
correction to a stated fact, a bug found and deliberately not fixed because
it's out of scope, a process incident, anything with an undetermined owner),
referenced from your Work Log rather than duplicated into it. Concretely for
this task: a fixture bug that isn't the migration-cost issue, a package
whose `store.New(` call sites resist the mechanical pattern for a real
reason, or anything you find while checking candidate 6 (redirected-store
verification) that looks like a second instance of the `08/10` class of
incident.

**Work directly against a clean `main`; no worktree isolation needed for
this task specifically** — nothing else should be running concurrently with
it (see the isolation banner above), so there's no parallel worktree to
isolate against. If you discover something else *is* running against
`internal/store`/`internal/service`/`internal/selftools`/`internal/api`
concurrently, stop and escalate per the sequencing constraint above rather
than proceeding.

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

## For the reviewer — read this before reviewing, not a generic "review this" ask

**The specific hazard: "faster" and "broken isolation" look identical from
the inside.** A fixture that's quick because every test now shares one live
database file passes its own timing check perfectly — the suite genuinely
does get faster, the worker's before/after numbers will genuinely look
good, and nothing about a passing `go test -race ./...` run distinguishes
that outcome from a correct one. A timing win is not evidence of a correct
fix here; it's exactly as consistent with the bug this instruction exists to
catch.

**Do this specifically, not "check that tests still pass":**

1. Pick at least 3-4 of the migrated call sites across different packages
   (`internal/service`, `internal/selftools`, `internal/api` at minimum —
   the packages named in this task's own Done-means).
2. For each, trace the actual DB path/file each test instance opens — not
   the helper function's signature, the real resolved path at runtime (e.g.
   confirm it's under that specific test's own `t.TempDir()`, not a shared
   package-level path, a fixed filename, or anything computed once and
   reused across tests).
3. Confirm two different tests running concurrently (as `-race` runs them)
   provably open two different files — read the code path, don't infer this
   from the suite passing.
4. If the worker's fix follows `internal/store`'s own template-copy pattern
   correctly, this should be structurally easy to confirm — the template is
   read-only, each test gets a fresh copy. Anything that instead has tests
   open the *same* path, or the *same* already-open handle, is the bug this
   check exists to catch, no matter how fast or green the suite looks.

**This is not a hypothetical concern for this batch specifically.** Wave 3's
`08/10` review found regression tests that had been writing synthetic
memories into the operator's real Tesseract database — a genuine test-
isolation failure that shipped past a passing suite and was only caught on
review (`TASKS/ESCALATIONS.md`, 2026-08-23, "Wave 3 `08/10` regression tests
wrote synthetic memories to the operator Tesseract database — CLOSED AND
CLEANED"). That incident is why this task's own "hazard to respect" section
above exists, and it's why this reviewer instruction is specific rather than
generic — a general "make sure tests pass" review would not have caught it
the first time either.

Independent of the isolation check: also verify the closing acts in Done-means
actually landed, not just the timing/isolation fix itself — `08/08`
re-run and promoted to `reviewed`, `GO-SEC-001`/`GO-SEC-002` updated in
`findings.json`, the Wave 2 follow-up closed in `TASKS/ESCALATIONS.md`, and
candidate 4 struck from `14-followups/README.md`'s register. A worker who
lands the fix but skips these leaves three tracked items open pointing at a
now-solved problem — check each one exists, don't take the Work Log's word
for it.

## Work log

## Review notes
