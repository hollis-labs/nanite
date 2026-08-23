# Sandbox proxy's http.Server has no ReadHeaderTimeout

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/sandbox/proxy.go`
**Requires architect decision:** false (matches `findings.json`)

This is **the smallest task in this folder** — a single missing `http.Server` field.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** Wave 2 complete
> - **Blocks:** none
> - **Parallel-safe with:** `08/01`–`08/05`, `08/10`
> - **Gated on:** none
> - **requires_security_review:** true · **requires_regression_test:** true

## Findings addressed

- **GO-SEC4-008** (low severity, high confidence, security) — report §8.12.

## Context

The sandbox proxy's `http.Server` has no `ReadHeaderTimeout` set — gosec's G112 rule (Slowloris-class request-header-timeout exposure). Real-world exposure is genuinely narrow: this proxy is loopback-only bind, reachable only by the sandboxed subprocess itself (per report §8.12's broader review of this same file, which independently confirms the proxy's SSRF/DNS-rebind defenses are otherwise among the strongest code in the whole audit — pinned-IP dialing, CONNECT restricted to a TLS-port allowlist, no auto-followed redirects). This isn't a network-reachable-from-outside issue, but it's a zero-cost fix regardless of exposure size, and gosec will keep flagging it every scan until it's set.

**Trust classification:** OS-derived/internal — the only caller able to reach this listener at all is the sandboxed subprocess Nanite itself spawned, on loopback. Severity stays low under the guide's "severity should follow the actual trust boundary" instruction, matching the audit's own low rating.

## What to do

Set `ReadHeaderTimeout` (the report suggests ~10s as a reasonable default) on the `http.Server` construction in `internal/sandbox/proxy.go`. That is the entire code change.

## Non-goals

Do not add other `http.Server` hardening (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`) beyond what this finding names, unless a follow-up finding specifically asks for it — keep this task to the one-line fix it actually is.

## Tests required

None strictly necessary for a config-value fix, but check whether the package has an existing server-construction test that would be surprised by a non-zero `ReadHeaderTimeout` (unlikely, but verify before landing). A minimal test asserting the constructed `http.Server` has a non-zero `ReadHeaderTimeout` is a reasonable, cheap addition.

## Prevention

Gosec G112 will pass on this file going forward; no further structural change is needed to prevent recurrence — this is not a class of defect that spreads, it's a single omitted default.

## Verification

`gosec ./internal/sandbox/...` confirming G112 no longer fires on `proxy.go`; `go build ./...`.

## Risk / rollback

Negligible — a legitimate slow client (if any exists) hitting the header-timeout limit would need to complete sending headers within the configured window; at 10s this is not expected to affect real usage. Rollback is a one-line revert.

## Done means

- [x] `ReadHeaderTimeout` set on the proxy's `http.Server`
- [x] `gosec` G112 no longer flags `proxy.go`

## Work log

- 2026-08-22: Set the sandbox proxy server's `ReadHeaderTimeout` to 10 seconds; no other `http.Server` timeout was added.
- Added `TestProxy_ReadHeaderTimeout` to lock the server configuration to the intended non-zero value.
- Verification passed: `go test ./internal/sandbox/...`; `gosec ./internal/sandbox/...` reported no G112 finding on `proxy.go` (11 pre-existing unrelated findings remain in the package scan); `go build ./cmd/nanite/`; `go vet ./...`; `go test ./...`; `git diff --check`.

## Review notes

<!-- Reviewer fills this in. -->
