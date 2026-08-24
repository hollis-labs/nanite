# Dependency and toolchain version bumps for reachable CVEs

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** reviewed — deferred full race-suite gate completed on 2026-08-23
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

- [x] `otlptracehttp` (and sibling `otel` modules) bumped to `>= v1.43.0`
- [x] Go toolchain directive bumped to `>= 1.26.6`
- [x] `govulncheck ./...` clean for both GO-2026-4985 and the 13 named stdlib CVEs
- [x] Full test suite and `-race` suite both pass post-bump
- [x] `go mod tidy -diff` clean

## Work log

- 2026-08-22: Verified the version choices against primary current sources before editing. The official `go.dev/dl/?mode=json&include=all` feed lists `go1.26.7` as the latest 1.26 patch, so the `go` directive moved from 1.26.2 to 1.26.7 rather than stopping at the task's older 1.26.6 floor. The Go module proxy lists v1.45.0 as the current `otlptracehttp` release.
- 2026-08-22: Bumped every OpenTelemetry module pinned in `go.mod` to v1.45.0 (`otel`, `trace`, `metric`, `sdk`, `otlptrace`, and `otlptracehttp`). `go mod tidy` also advanced the OTLP exporter's required protocol/gRPC transitives (`grpc-gateway`, `proto/otlp`, `genproto`, and `grpc`); no unrelated dependency sweep was performed. Confirmed the production wiring remains `cmd/nanite/main.go` -> `internal/otel.Init` and builds/tests successfully.
- 2026-08-22: Verification passed: `govulncheck ./...` (`No vulnerabilities found`, so GO-2026-4985 and all 13 named stdlib findings are absent), `go build ./...`, `go vet ./...`, `go test ./...`, `go mod verify` (`all modules verified`), and `go mod tidy -diff` (empty output).
- 2026-08-22: The unmodified full `go test -race ./...` command was run twice and did not complete cleanly on this machine. The first run timed out after 10 minutes in four packages (`internal/messaging`, `internal/selftools`, `internal/service`, `internal/subagent`); the exact cached rerun allowed `internal/messaging` to pass in 531.617s but the other three again exceeded the same per-package timeout. All failure stacks showed CPU-bound `modernc.org/sqlite`/Goose test-database migration work, and neither run emitted a `WARNING: DATA RACE`. A focused default-timeout `go test -race ./internal/selftools` also timed out in migration work; `go test -race -timeout 30m ./internal/selftools` then passed in 1446.675s with no race finding. A focused extended-timeout `internal/subagent` run was stopped after approximately 10 minutes, with no race/failure output, to avoid spending another 20 minutes on an out-of-scope suite-runtime issue; `internal/service` was not rerun a third time. This race-suite runtime/timeout limitation is preserved honestly and was not "fixed" by broad, out-of-scope test changes.
- 2026-08-23: Task `14/03` replaced repeated fresh migrations with isolated
  per-test copies of a fully migrated template. The deferred gates now complete:
  final `go test ./... -count=1` passed in 39.65s wall and final
  `go test -race ./... -count=1` passed with a verdict in 263.45s wall. The two
  formerly blocked aggregates specifically passed (`internal/selftools`
  25.649s and `internal/service` 52.746s in the final full run). This closes the sole
  remaining acceptance gap without changing the dependency remediation.

## Review notes

- 2026-08-22: Fresh dependency/code review confirmed Go 1.26.7 and the aligned OpenTelemetry v1.45.0 module set are current and correctly scoped, production OTel wiring still builds, and focused OTel tests pass. Review verdict remained FAIL solely because the explicit full `go test -race ./...` acceptance gate timed out; no race warning or dependency defect was found.
- 2026-08-23: The operator explicitly deferred that full-race gate after the repeated migration-bound timeouts stalled Wave 3. The task remains `implemented`, not `reviewed`; the unchecked Done-means item and exact qualification above are intentional. The final merged Wave 3 `go build ./...`, `go vet ./...`, and `go test ./...` baseline passed.
- 2026-08-23: The deferral condition is resolved. The fixture remediation's
  full non-race and race runs above both passed; this task is promoted to
  `reviewed`, with its original timeout evidence retained as historical context.
