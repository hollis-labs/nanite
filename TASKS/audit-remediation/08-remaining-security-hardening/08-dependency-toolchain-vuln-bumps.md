# Dependency and toolchain version bumps for reachable CVEs

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none
**Touches:** `go.mod`, `go.sum`, the toolchain/`go` directive in `go.mod`
**Requires architect decision:** false (matches `findings.json` for both findings)

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** `00/02` (refreshed `govulncheck` output)
> - **Blocks:** none
> - **Parallel-safe with:** **none — runs alone.** It rewrites `go.mod`/`go.sum`, which every other open worktree also carries; a parallel run guarantees a conflict in every branch. The guide's "independent dependency upgrades can run in parallel" does not survive contact with worktree-based dispatch.
> - **Gated on:** none
> - **requires_security_review:** true · **requires_regression_test:** false

## Findings addressed

- **GO-SEC-001** (medium severity, high confidence, security + dependency) — report §3.
- **GO-SEC-002** (low severity, high confidence, security + dependency) — report §3.

## Context

Both are standard, low-risk, mechanical version bumps — no architect judgment call needed, just execution and verification.

**GO-SEC-001:** `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` is pinned at v1.41.0, which has a reachable oversized-response memory-exhaustion bug (**GO-2026-4985**), fixed in v1.43.0. `govulncheck ./...` reports this reachable via `otel.Init -> otlptracehttp.New / client.UploadTraces` (`internal/otel/otel.go`). Reachability requires OTel export enabled at runtime and a malicious/misbehaving collector endpoint on the other end — a real but bounded precondition.

**GO-SEC-002:** `go.mod` pins the Go toolchain at `go 1.26.2`; `govulncheck` reports **13 distinct reachable stdlib vulnerabilities** fixed across go1.26.3–go1.26.6 (GO-2026-6218, 6091, 6090, 6089, 5972, 5856, 5039, 5037, 5026, 4982, 4980, 4971, 4918), spanning TLS handshake limits, an HTTP/2 SETTINGS-frame infinite loop, `html/template` XSS-escaper bypasses, `textproto`, `x509`, `asn1`, and more — all reported reachable from application code, not just theoretical stdlib surface.

**Refreshed at frozen HEAD `1d3bfd96` by `00/02`** (`docs/audits/2026-08-21-go-quality/raw-1d3bfd96/DELTA.md`, raw: `raw-1d3bfd96/govulncheck.log`): `govulncheck ./...` still reports **exactly the same 14 reachable vulnerabilities** — the identical 13 stdlib IDs listed above plus the same GO-2026-4985 module vuln — as at the audited commit `8feeee5c` (`raw/govulncheck.log`). Zero drift across 40 commits; this task's scope is unchanged and still fully current.

## What to do

1. Bump `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` to `>= v1.43.0`. The audit recommends bumping sibling `otel` modules for consistency at the same time — check `go.mod` for other `go.opentelemetry.io/otel/*` modules and align versions together rather than bumping only the one flagged module in isolation, to avoid a split-version otel dependency graph.
2. Bump the `go.mod` `go` directive / toolchain line from `1.26.2` to `1.26.6` (or the latest `1.26.x` patch available at implementation time, if a newer patch has shipped since the audit).
3. Run `go mod tidy` after both bumps to reconcile `go.sum`.

**All production callers:** N/A in the per-caller sense — these are dependency-graph-wide bumps, not a specific code-site fix — but confirm `otel.Init`'s actual production call site(s) (report cites `internal/otel/otel.go`) still build and initialize correctly post-bump.

## Non-goals

Do not use this task to perform a broader dependency-audit sweep beyond these two specific findings. Other `go.mod` entries are out of scope unless directly required to resolve a version conflict introduced by these two bumps.

## Tests required

- `govulncheck ./...` clean re-run after both bumps (confirm all 14 named vulnerabilities — GO-2026-4985 plus the 13 stdlib ones — no longer appear).
- Full test suite (`go test ./...`) re-run after both bumps.
- `go test -race ./...` re-run **specifically after the toolchain bump** — routine, but explicitly called out to be verified, not assumed, since a toolchain bump can occasionally shift race-detector timing/behavior in ways a dependency-only bump wouldn't.

## Prevention

This is exactly the kind of finding a scheduled `govulncheck` gate (guide §4 Wave 7, "Full-repo scheduled/merge gate": "add an enforcement point for... `govulncheck`") is meant to catch continuously rather than via periodic manual audit. If that gate doesn't already exist, cross-reference it against folder 12's quality-ratchet work — not this task's own scope to build, just worth noting the pairing.

## Verification

`govulncheck ./...`; `go build ./...`; `go vet ./...`; `go test ./...`; `go test -race ./...`; `go mod verify`; `go mod tidy -diff` (should report no diff after `tidy` is run and committed).

## Risk / rollback

Low — both are patch/point-release bumps to already-adopted dependencies/toolchain, not major-version jumps. Standard regression risk of any dependency bump (transitive version conflicts, subtle behavior changes in updated stdlib functions) — the required full-suite + `-race` rerun is the mitigation. Rollback is reverting the `go.mod`/`go.sum` diff.

## Done means

- [ ] `otlptracehttp` (and sibling `otel` modules) bumped to `>= v1.43.0`
- [ ] Go toolchain directive bumped to `>= 1.26.6`
- [ ] `govulncheck ./...` clean for both GO-2026-4985 and the 13 named stdlib CVEs
- [ ] Full test suite and `-race` suite both pass post-bump
- [ ] `go mod tidy -diff` clean

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
