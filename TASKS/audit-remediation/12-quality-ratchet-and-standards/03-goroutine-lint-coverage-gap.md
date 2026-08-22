# Add the trust-boundary primitives cluster to `lint-goroutines`'s scanned packages

**Phase:** Wave 7 — Quality ratchet
**Status:** not-started
**Depends on:** none.
**Touches:** `Makefile` (`lint-goroutines` target only, `Makefile:74-81`).

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 7 — quality ratchet · **Dispatch unit:** `W7`
> - **Depends on:** `04/04` — that task performs the `safego` adoption this one lints
> - **Blocks:** none
> - **Parallel-safe with:** `12/01`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

`requires_architect_decision: false` — this is a small, mechanical, one-
line-per-package addition to an existing grep-based sweep target. There is
no design ambiguity: the fix is to add five package paths to a list that
already contains eight others in the same style.

### Findings addressed

- `GO-SEC4-009` — severity **informational**, confidence **high**.
  `docs/audits/2026-08-21-go-quality/REPORT.md` §8.12 (full writeup, in the
  "Trust-boundary primitives: internal/sandbox, internal/permission,
  internal/secrets, internal/pathsafe, internal/fsutil, internal/safego"
  cluster review); `findings.json` id `GO-SEC4-009`.

### Root cause

`make lint-goroutines` (`Makefile:74-81`) is a Phase 1 Wave 1 (2026-04-12)
addition — per its own comment, a grep-based sweep for bare `go` statements
in packages that "should adopt `internal/safego`" (this repo's lifecycle-
tracked goroutine wrapper), since `forbidigo` cannot match the bare `go`
keyword itself. Its current scanned-package list is:

```makefile
.PHONY: lint-goroutines
lint-goroutines:
	@echo "==> internal/safego adoption sweep (bare 'go ' statements)"
	@-grep -rn --include='*.go' --exclude='*_test.go' -E '^\s+go [A-Za-z_][A-Za-z0-9_.]*\(' \
		internal/plugin internal/worker internal/mcp internal/service \
		internal/server internal/api internal/memory internal/workflow \
		2>/dev/null | grep -v 'safego\.Go' | grep -v 'safego\.Call' || \
		echo "(no bare goroutines in target packages)"
```

(`Makefile:74-81`)

That list — `internal/plugin`, `internal/worker`, `internal/mcp`,
`internal/service`, `internal/server`, `internal/api`, `internal/memory`,
`internal/workflow` — omits every package in the trust-boundary primitives
cluster this audit reviewed together in §8.12: `internal/sandbox`,
`internal/permission`, `internal/secrets`, `internal/pathsafe`,
`internal/fsutil`. (`internal/safego` itself, the sixth package in that
cluster's review scope, is the wrapper being adopted, not a scan target —
correctly absent from the list for that reason, not an oversight.)

The audit manually ran the equivalent check against this cluster and found
exactly one bare `go func()`: `internal/sandbox/proxy.go:414`, inside
`(*Proxy).runTunnel` (`internal/sandbox/proxy.go:399-405` for the enclosing
function, whose own doc comment states it uses "lifecycle-tracked
goroutines," tracked via `p.lc` so `Proxy.Stop` can cancel the root
context). The bare `go func()` at line 414 is a small internal watcher
goroutine — it starts a `select` that closes connections when the lifecycle
context is cancelled, and it self-terminates via its own `done` channel
close in the enclosing `defer`. The audit confirmed this specific instance
is benign: tightly-scoped, self-terminating, and already running inside a
function (`copyOne`, called via `p.lc.Go(...)`) that is itself
lifecycle-tracked — so the untracked inner `go func()` doesn't represent an
actual unowned-goroutine risk in this instance.

But that manual confirmation is exactly the problem the finding names: it
was **manual**, one-time, and done by the audit — not by the guardrail tool
that's supposed to catch this automatically going forward. `lint-goroutines`
simply never looks at this cluster. A future genuinely-unowned goroutine
introduced into `internal/sandbox`, `internal/permission`, `internal/secrets`,
`internal/pathsafe`, or `internal/fsutil` — packages that, per this cluster's
own §8.12 framing, sit directly on this project's trust boundaries — would
not be caught by this tool, even though it's precisely the cluster where an
unowned/unaccounted-for goroutine matters most (per this same batch's
"Lifecycle Ownership" standard being codified in
`02-add-engineering-standards-docs.md`, sibling task in this folder).

### Desired invariant

`make lint-goroutines`'s scanned-package list includes every package in the
trust-boundary primitives cluster, so a future bare, untracked `go func()`/
`go <call>()` introduced anywhere in that cluster is surfaced by the existing
sweep rather than depending on another manual audit to catch it.

## Scope

One file, one target, one list. No Go source changes — `internal/sandbox/proxy.go:414`'s
existing bare `go func()` is confirmed benign per the audit and is explicitly
not required to change as part of this task (see Non-goals). This task only
extends what the tool *looks at* going forward.

## All production callers

Not applicable — this is a tooling-coverage task, not a shared-primitive
migration. There is no caller enumeration; the "all production callers"
concern for this task is really "all packages in the named cluster," which
is exactly the five listed above, taken directly from the audit's own
`GO-SEC4-009` package field (`"internal/sandbox, internal/permission,
internal/secrets, internal/pathsafe, internal/fsutil"`) and §8.12's cluster
scope.

## Proposed direction

Add the five packages to `lint-goroutines`'s existing `grep -rn` package-path
argument list in `Makefile:78-79`, in the same style as the existing eight:

```makefile
.PHONY: lint-goroutines
lint-goroutines:
	@echo "==> internal/safego adoption sweep (bare 'go ' statements)"
	@-grep -rn --include='*.go' --exclude='*_test.go' -E '^\s+go [A-Za-z_][A-Za-z0-9_.]*\(' \
		internal/plugin internal/worker internal/mcp internal/service \
		internal/server internal/api internal/memory internal/workflow \
		internal/sandbox internal/permission internal/secrets \
		internal/pathsafe internal/fsutil \
		2>/dev/null | grep -v 'safego\.Go' | grep -v 'safego\.Call' || \
		echo "(no bare goroutines in target packages)"
```

(Exact line-wrapping/formatting is this task's own polish call — match
existing `Makefile` conventions, e.g. the backslash-continuation style
already used for the first eight packages.)

After adding, run `make lint-goroutines` once to confirm it surfaces
`internal/sandbox/proxy.go:414` (the known, audit-confirmed-benign instance)
— this is the expected, correct new output, not a regression. Do not
"fix" that line as part of adding it to the scan; see Non-goals.

## Non-goals

- **Do not modify `internal/sandbox/proxy.go`'s `runTunnel`/`copyOne`.** The
  audit already confirmed the one bare `go func()` in scope is benign
  (tightly-scoped, self-terminating, nested inside an already lifecycle-
  tracked call). Migrating it to `internal/safego.Go`/`.Call` is optional
  follow-up cleanup at best, not part of closing this finding — this finding
  is about the *tool's* coverage gap, not about that specific line's
  correctness. If a future worker wants to migrate it for consistency, that
  is separate, unscoped work.
- **Do not add `internal/safego` itself to the scanned list.** It is the
  wrapper being adopted, not a migration target — adding it would produce
  meaningless self-referential noise (its own internal implementation
  necessarily contains real `go` statements).
- **Do not expand `lint-goroutines`'s scanned list beyond these five
  packages** as part of this task. Whether other packages outside both the
  original eight and this cluster's five should eventually be covered is a
  separate, broader question this task does not attempt to answer.
- **Do not change the sweep's mechanism** (the `grep`-based approach, its
  exclusion of `_test.go` files, its `safego.Go`/`safego.Call` allowlist
  pattern, or its non-fatal `-` prefix keeping `make lint` as a whole
  ungated on adoption). All of that is working as designed; this task only
  extends its input list.

## Dependencies

- None. Fully independent of every other task in this batch. Could
  alternatively have been batched into `TASKS/audit-remediation/13-mechanical-cleanup/`
  given how small and mechanical it is — it's placed here instead because
  it's a tooling-coverage gap directly tied to this folder's quality-gate
  theme (the same trust-boundary-cluster gap this batch's
  `02-add-engineering-standards-docs.md` sibling task's new "Lifecycle
  Ownership" and "Trust-Boundary Paths" standards are meant to guard
  against). A planner is free to move it if `13-mechanical-cleanup/`'s own
  batching turns out to be a better fit at sequencing time.

## Tests required

- No unit test applicable — this is a `Makefile` target, not Go source.
  The verification step below (running `make lint-goroutines` and confirming
  it now reports the known `internal/sandbox/proxy.go:414` line) is the
  functional-correctness check for this change.

## Prevention

This task *is* the prevention mechanism for `GO-SEC4-009` — closing the
coverage gap is the fix. No further meta-prevention needed; the existing
`lint-goroutines` target (already wired as a `.PHONY` target callable
standalone, and worth confirming at implementation time whether it's also
invoked from `make lint` or another aggregate target — check current
`Makefile` structure, since this task file does not assert that it is)
continues to serve as the durable, automated check going forward.

## Verification

```bash
make lint-goroutines
```

Observable behavior required for PASS: output includes a line identifying
`internal/sandbox/proxy.go:414` (the known bare `go func()` inside
`runTunnel`/`copyOne`) — confirming the newly-added packages are actually
being scanned, not just listed. If the tool instead reports "(no bare
goroutines in target packages)" after this change, the addition did not take
effect and needs to be re-checked (e.g. a typo in the package path, or the
`grep -v` allowlist accidentally matching something it shouldn't).

## Risk / rollback

Zero risk to production code — `Makefile`-only change to a non-blocking,
informational sweep target (`make lint` as a whole is explicitly not gated
on this target's output, per its own comment: "kept non-fatal by the leading
`-` so `make lint` as a whole isn't gated on adoption"). Rollback is a
single-line revert to the package list.

## Done means

- [ ] `lint-goroutines`'s scanned-package list in `Makefile` includes
      `internal/sandbox`, `internal/permission`, `internal/secrets`,
      `internal/pathsafe`, and `internal/fsutil`, alongside the existing
      eight.
- [ ] `make lint-goroutines` run after the change reports
      `internal/sandbox/proxy.go:414` (confirming the scan actually covers
      the new packages).
- [ ] No change made to `internal/sandbox/proxy.go` or any other Go source
      file.
- [ ] `internal/safego` itself remains outside the scanned list.

## Work log

<!-- Worker fills in: what was actually done, any deviation and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
