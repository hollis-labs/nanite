# 12 — Quality ratchet and standards

"Quality ratchet" here means exactly what the remediation guide's Wave 7
means by it: an enforcement point that stops backlog from growing, without
requiring the existing backlog to hit zero first. The guide's own framing
is explicit — *"After meaningful backlog reduction, establish enforcement,"*
and *"Do not require historical low-value debt to hit zero before
introducing a ratchet. Baseline and reject regressions/new actionable
findings."* That ordering matters: this folder's three tasks are naturally
**late** in any eventual sequencing, not early, even though this
task-creation pass isn't doing sequencing itself. A gate that starts
blocking on a 2,338-issue pre-existing baseline before anyone has had a
chance to work that backlog down (via this batch's `13-mechanical-cleanup/`
and the semantic/architectural work in `10-`/`11-`) would either never turn
green or would need its baseline immediately overridden — either way,
undermining the ratchet's own credibility on day one. A future planner
should treat this folder as a late-wave item that follows meaningful
progress elsewhere in this batch, not a quick win to front-load.

## Task files

- **`01-full-repo-scheduled-lint-gate.md`** — `GO-HYG-001` (informational),
  `GO-SVCCORE-005` (informational). The project's uncapped `make lint` is
  already correctly configured but nothing ever runs it outside a developer
  typing it manually — no hook, no CI workflow exists in this repo at all.
  This task adds a full-repo scheduled/merge gate covering the six checks
  the guide names (uncapped lint, `govulncheck`, `gosec`, module
  verification, dead-code report, race suite), explicitly leaves the fast
  developer gate (`lefthook.yml`) untouched, and folds in a tool-syntax
  caveat (`//nolint:gosec` not being understood by a bare `gosec` binary)
  so the new gate's `gosec` leg doesn't cry wolf on day one.
  `requires_architect_decision: true` — both whether a CI mechanism already
  exists outside this repo's visibility, and where a new one should live,
  are open questions this task explicitly defers rather than guesses at.
- **`02-add-engineering-standards-docs.md`** — guide-derived, not tied to a
  specific finding ID. Adds the remediation guide's six named standards
  (Production Reachability, Security/Correctness Migration Completeness,
  Semantic Duplication, Lifecycle Ownership, Trust-Boundary Paths, Silent
  Security Degradation) to this project's existing
  `docs/engineering/standards/coding-standards.md`, with each standard
  traced back to the real, repeating audit pattern it was derived from.
  `requires_architect_decision: false` — the content is already fully
  drafted; only placement and wording polish are left.
- **`03-goroutine-lint-coverage-gap.md`** — `GO-SEC4-009` (informational).
  `make lint-goroutines`'s scanned-package list omits the entire
  trust-boundary primitives cluster (`internal/sandbox`,
  `internal/permission`, `internal/secrets`, `internal/pathsafe`,
  `internal/fsutil`); a manual audit-time check found one bare `go func()`
  in that cluster and confirmed it benign, but the guardrail tool itself
  wouldn't catch a future, genuinely-unowned one. Small, mechanical,
  one-line-per-package `Makefile` addition. `requires_architect_decision:
  false`.
