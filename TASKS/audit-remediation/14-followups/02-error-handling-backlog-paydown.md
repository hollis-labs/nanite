# Pay down the errcheck / errorlint / nilerr backlog to zero

**Phase:** Audit remediation — Wave 9 (follow-ups)
**Status:** not-started
**Depends on:** `12/01` stage 1 (the regression gate) should land first, so this paydown is measured against a gate that already exists.
**Blocks:** **`12/01` stage 2.** Zero-tolerance on these three linters cannot be activated until this reaches zero.
**Parallel-safe with:** **nothing meaningful.** It touches error handling across the tree; treat it like the ctx sweep — its own window, landing in as few merges as practical.
**Gated on:** AD-21 — decided; this task *is* the decided work.
**requires_security_review:** false
**requires_regression_test:** false — this must not change behaviour, so there is no new behaviour to test. The existing suite passing unchanged is the safety net.

---

> ## This is a mechanical sweep with a known failure mode. Read `06/03` and `06/04` first.
>
> The context-propagation sweep (`06/03`) was mechanically perfect — every
> oracle green, conservation exact — and still introduced a behavioural
> regression that only one test in the repo caught: a store write recording an
> operation's outcome became cancellable by that operation's own context
> (`06/04`).
>
> **This task has the same shape and the same hazard.** A 365-item
> error-handling sweep is exactly where a silently-swallowed error becomes a
> loudly-returned one, or vice versa, in a path nobody tests. Assume that at
> least one of your changes alters behaviour, and structure the work so you
> find it rather than ship it.

---

## Context

### Decision

**AD-21 (2026-08-22)** made `errcheck`, `errorlint`, and `nilerr` zero-tolerance
in the full-repo gate — but staged, because the backlog makes immediate
activation impossible. A gate that fails every merge on day one gets disabled
within a week, taking the regression gate with it.

### The backlog

Measured at frozen HEAD `1d3bfd96`
(`docs/audits/2026-08-21-go-quality/raw-1d3bfd96/DELTA.md`):

| Linter | At `8feeee5c` | At `1d3bfd96` | What it catches |
|---|---:|---:|---|
| `errcheck` | 284 | **294** | Unchecked error returns |
| `errorlint` | 47 | **49** | Error comparison/wrapping that breaks `errors.Is`/`As` |
| `nilerr` | 22 | **22** | Returning `nil` when `err != nil` |
| | | **365** | |

**Re-derive these before starting.** They are from 2026-08-22 and Waves 4–8 will
move them. Use the audit config, not the project's:

```bash
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0
```

### Why these three, and why `nilerr` first

They were separated from the other linters on evidence, not taste.
**`nilerr` is the linter that already caught `GO-STORE-003`** — a high-severity
finding where `DeleteAgentByID` could not distinguish a genuine not-found from
a real database error. It was sitting in the lint output the whole time,
invisible because the pre-commit hook runs `golangci-lint run --new` and
structurally cannot surface a pre-existing finding in untouched code.

That is the argument for zero-tolerance on this class: these three don't report
style, they report **errors going missing**. And one of them has already proven
it finds real high-severity bugs in this codebase.

## What to do

### Order the work by value, not by count

1. **`nilerr` (22)** — smallest and highest value. Every hit is "we returned
   `nil` while holding a non-nil error," which is `GO-STORE-003`'s exact shape.
   **Expect real bugs here.** Any hit that turns out to be a genuine defect
   rather than a lint artifact goes in the Work log and, if it is
   security- or data-integrity-relevant, to `TASKS/ESCALATIONS.md` as a new
   finding — **not** into `findings.json`, which is a frozen catalog of the
   original audit.
2. **`errorlint` (49)** — mostly `==` comparisons and `%v`-wrapped errors that
   defeat `errors.Is`/`errors.As`. Mechanical, but each fix changes what
   `errors.Is` matches, so a caller relying on the broken comparison changes
   behaviour. Check callers.
3. **`errcheck` (294)** — the bulk, and the least individually interesting.
   This is where fatigue produces mistakes.

### The rule for `errcheck` fixes

For each unchecked error, choose deliberately:

- **Handle it** — if the error is actionable, act on it.
- **Return it** — if the caller can act, propagate.
- **Explicitly ignore it** with `_ =` **and a comment saying why.** A bare
  `_ =` with no reason is not a fix; it is the same defect with the linter
  silenced.

**Never** add a blanket `//nolint:errcheck` to a file or package. Per-line, with
justification, or not at all.

### The hazard, concretely

`errcheck` fixes change behaviour when a previously-ignored error starts being
returned. A `defer f.Close()` becoming `defer func() { _ = f.Close() }()` is
inert; a `w.Write(...)` whose error you start propagating is not. **Cleanup and
best-effort paths are where this bites** — the same category `06/04` had to
rescue with `context.WithoutCancel`.

## Verification

```bash
# The three must reach zero under the audit config
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 \
  --enable-only errcheck,errorlint,nilerr

go build ./... && go vet ./... && go test ./... && go test -race ./...
```

`go test ./...` must be **fully green**. It is the only thing standing between a
mechanical sweep and a shipped behaviour change — `06/03`'s summary reported a
clean suite that was not clean, and that is how the regression nearly landed.
Run it yourself on the final state and read the output.

## Done means

- `errcheck`, `errorlint`, and `nilerr` all report **0** under the audit config.
- Every `_ =` introduced carries a comment explaining why the error is safely
  ignored. Zero blanket `//nolint` directives added.
- `go test ./...` green on the final state, verified by the implementer
  directly, plus `-race` clean.
- The Work log records: the re-derived starting counts, every hit that turned
  out to be a **real bug** rather than a lint artifact, and any place a fix
  changed behaviour and why that was judged correct.
- **`12/01` stage 2 is activated** as the closing act — zero-tolerance on these
  three switched on in the gate config, since its prerequisite is now met.
  Without this step the task has paid down a backlog that will simply grow
  back.

## Work log

## Review notes
