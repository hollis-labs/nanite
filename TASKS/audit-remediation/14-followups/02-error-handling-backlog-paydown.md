# Pay down the errcheck / errorlint / nilerr backlog to zero

**Phase:** Audit remediation — Wave 9 (follow-ups taxonomy)
**Execution:** Wave 8, with explicit operator approval
**Status:** reviewed
**Depends on:** `12/01` stage 1 (the regression gate) should land first, so this paydown is measured against a gate that already exists.
**Blocks:** Cleared — `12/01` stage 2 was activated after errcheck, errorlint, and nilerr reached zero.
**Parallel-safe with:** **nothing meaningful.** It touches error handling across the tree; treat it like the ctx sweep — its own window, landing in as few merges as practical.
**Gated on:** AD-21 — decided; this task *is* the decided work.
**requires_security_review:** false
**requires_regression_test:** true — semantic review found behavior-changing fixes; dedicated regressions cover each confirmed defect in addition to the unchanged full suite.

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
invisible because the pre-commit hook then ran `golangci-lint run --new`, which
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

- 2026-08-24: Executed in Wave 8 with explicit operator approval. Re-derived
  the backlog at the isolated starting commit
  `e8ca7c5c529ea82b514c3dc28b1600680f317d7c` using the audit config:
  `errcheck` **281**, `errorlint` **48**, and `nilerr` **24**. Worked in the
  required value order: nilerr, errorlint, then errcheck.
- The nilerr review found two genuine defects among the 24 reports. An
  unscoped PCC lookup treated every `os.ReadDir` failure as an absent cache;
  it now preserves not-found as the empty result but propagates permission,
  I/O, and other failures. Stale subagent expiry treated a malformed persisted
  `created_at` as "not stale"; it now returns a wrapped parse error. The other
  nilerr reports were deliberate in-band MCP/builder results, best-effort
  discovery, cancellation status, or fail-open classification paths and now
  carry narrow per-line explanations.
- The errorlint sweep converted sentinel comparisons and type assertions to
  `errors.Is`/`errors.As` and changed contextual `fmt.Errorf` calls to `%w`, so
  callers can now match wrapped causes. Existing call sites were checked. The
  sole exact-identity assertion (`recover.TestWrap_UnrecoverablePassesThrough`)
  remains exact and has a narrow, reasoned `errorlint` suppression; no blanket
  suppression was added and test assertions were not discarded.
- Errcheck review produced several correct behavior changes rather than merely
  silencing returns: corrupt session metadata is no longer overwritten as an
  empty map; artifact, plugin-archive, copied-file, and Darwin seatbelt output
  close failures abort before the result is persisted or used; plugin and CLI
  directory/output failures now reach the caller; rows are explicitly closed
  before schedule/tool backfill writes; and orphan worktree cleanup no longer
  reports/counts a directory as cleaned when removal failed. MCP transport,
  coordination-store, worker-cancellation, JSON-response, discovery, prune,
  and other best-effort failures are now logged. Deferred query/transaction
  cleanup remains best-effort through documented helpers. Every introduced
  explicit ignore is accompanied by its reason.
- The data-integrity-relevant session/artifact finalization defects were
  recorded durably in `TASKS/ESCALATIONS.md`; both are fixed here and require
  no separate follow-up. Two later-task overlaps are intentional:
  `internal/mcp/manager.go` now logs transport close failures while `13/05`
  will move that close outside the registry lock, and
  `internal/contextbroker/source_pcc.go` now distinguishes absence from real
  directory errors while `13/05` will revisit the PCC relevance contract.
  Those later changes must preserve these error semantics.
- Activated `12/01` Stage 2 only after all three correctness linters reached
  zero. The comparator now requires Stage 2 active, requires exactly
  `errcheck`/`errorlint`/`nilerr`, refuses nonzero committed baselines for
  them, and independently rejects any future finding. The initial comparator
  suite covered eight positive and negative cases. The full audit-config
  ratchet passed at **3,259/3,259**; incidental reductions in `govet` (619 to 617) and
  `staticcheck` (75 to 74) were preserved in the committed Stage 1 baseline.
- Initial implementation verification: correctness lint reported
  `errcheck=0`, `errorlint=0`, `nilerr=0` (`0 issues`, exit 0); full
  audit-config ratchet exit 0; `python3 -m py_compile` exit 0;
  `python3 -m unittest scripts/quality-ratchet_test.py` runs 8 tests and exits
  0; `git diff --check` exit 0; `go build ./...` exit 0; `go vet ./...` exit 0;
  `go test -count=1 ./...` exit 0; and
  `go test -race -count=1 ./...` exit 0 (`internal/store` 226.687s). No review
  or approval is claimed.
- Formal review-fix pass, 2026-08-24: added regressions for every confirmed
  behavior change: a non-directory PCC base proves non-ENOENT `ReadDir`
  failures propagate; malformed persisted subagent `created_at` prevents
  approval and preserves requested status; corrupt session metadata returns an
  error and remains byte-for-byte unchanged; a closed database proves
  `NextShortCode` does not turn query failures into `c1`; and injected artifact
  copy/close failures prove primary-error precedence, staging cleanup, no DB
  record, and preservation of any existing final-path file.
- Artifact uploads now write to a same-directory temporary file and atomically
  rename it only after copy and close succeed. Added a per-API test seam rather
  than global mutable state, so close failure is reliable under both ordinary
  and race tests. The original final path is never created or truncated on
  copy/close failure.
- Corrected reviewer-confirmed observability gaps: autocomplete walk warnings
  include root and error; MCP update discovery and malformed args/env/
  env-allowlist warnings include server identity and underlying error; logging
  sink close failure goes directly to an independent stderr writer after the
  sink is closed; and three web-fetch cleanup comments now describe their
  actual redirect, retryable-5xx, and non-retryable-4xx branches.
- Expanded the Stage 2 comparator suite from 8 to 13 cases. It now proves
  absent or structurally malformed `stage_2`, a missing active flag, missing
  linters, and the wrong linter set all fail closed, in addition to the prior
  zero/nonzero correctness-count checks.
- Formal review-fix verification on the finished tree: `jq empty` and
  `git diff --check` exit 0; `python3 -m py_compile` exit 0; all 13 comparator
  tests pass; every new focused regression passes; correctness lint reports
  `errcheck=0`, `errorlint=0`, and `nilerr=0` (`0 issues`); the full tracked
  109-package audit ratchet passes at **3,255/3,255** with no new-from-HEAD
  finding. Artifact factoring reduced `cyclop` 290 to 289, `gocyclo` 287 to
  286, and `gosec` 637 to 635; those reductions are preserved in the committed
  baseline. `go build ./...`, `go vet ./...`, and
  `go test -count=1 ./...` all exit 0; `go test -race -count=1 ./...` exits 0
  with `internal/api` at 108.973s and `internal/store` at 224.709s. No review
  or approval is claimed.

## Review notes

- Fresh review was split across behavior/error propagation, errcheck cleanup
  semantics, and Stage 2/tracker integrity. The first pass found missing
  behavioral regressions, incorrect `NextShortCode` error ordering, incomplete
  artifact failure cleanup, three under-specified diagnostics, a post-close log
  sent through the closed sink, inaccurate ignore comments, and stale tracking.
  Separate worker commits `2d532314` and `0adb654e` fixed those findings.
- All three reviewers passed the finished integrated diff. They independently
  confirmed the focused regressions, atomic artifact promotion and cleanup,
  all explicit-ignore rationales, diagnostic context, fail-closed Stage 2
  configuration coverage, exact tracker scope, and current `0/0/0` correctness
  counts. Focused ordinary/race checks, the 13 comparator tests, build, vet,
  full ordinary tests, full race tests, and diff checks passed.
