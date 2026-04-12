# Audit: dependency-supply-chain

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `dependency-supply-chain`

**Interpretation:** Cross-stack audit of Go `go.mod` and frontend `ui/package.json` dependency trees. Covers: CVE scanning, license compliance, vendoring model, version lag, replace directive hygiene, known-bad package detection.

**Files read in full:**
- `go.mod` (82 lines)
- `go.sum` (165 lines — verified via `go mod verify`)
- `ui/package.json` (63 lines)

**Files sampled:**
- `ui/package-lock.json` (via `npm audit` and `npm ls --depth=0`)
- Sibling library `go.mod` files (7 libraries — header only)
- Go module cache license files (12 modules)

**Packages skipped:** Individual Go source files were not reviewed for dependency usage patterns (that's a code-review scope, not a supply-chain scope). Exception: `plugins/support-ticket/kb.go` was checked to confirm `lib/pq` usage.

## Methodology

**Categories applied:**
- Security — CVE scanning (npm audit, govulncheck attempted)
- License compliance — manual license file review for all direct Go deps + npm tree
- Supply chain — replace directives, vendoring model, version lag, known-bad detection, build reproducibility

**Tools run:**
- `npm audit` (in `ui/`) — 4 vulnerabilities found (1 moderate, 3 high)
- `go mod verify` — all modules verified
- `go mod tidy -diff` — 1-line diff found
- `go mod why` — run for `lib/pq`, `mcp-helpers`, `go-queue`, `badger/v4`
- `go list -m all` — full module listing (100+ modules)

**Tools NOT run (blind spots):**
- `govulncheck` — **not installed** on this machine. This is a documented gap from the whole-repo-tooling-sweep. Go-side CVE scanning is absent. Filed as finding 03.
- `go list -m -u all` — **failed** because the phantom `mcp-helpers` replace directive causes a network lookup failure against a non-existent GitHub repository. Update availability for Go modules could not be checked automatically.
- `npm outdated` — not run (manual version assessment performed instead)
- `go-licenses` — not installed; license check was manual

## Findings

### By severity

**Critical (0)**
- _none_

**High (3)**
- [01 — Vite dev server has 3 active CVEs](01-high-vite-cves.md)
- [02 — npm transitive dependencies have active CVEs](02-high-npm-transitive-cves.md)
- [03 — govulncheck is not installed — Go vulnerability scanning is absent](03-high-govulncheck-gap.md)

**Medium (4)**
- [04 — Phantom and unnecessary replace directives in go.mod](04-medium-phantom-replace-directives.md)
- [05 — OpenTelemetry version skew between core and SDK/exporters](05-medium-otel-version-skew.md)
- [06 — lib/pq (PostgreSQL driver) is a direct dependency but nanite uses SQLite](06-medium-libpq-unnecessary.md)
- [07 — go mod tidy produces a diff](07-medium-go-mod-tidy-dirty.md)

**Low (1)**
- [08 — Version lag assessment for major dependencies](08-low-version-lag-report.md)

**Info (2)**
- [09 — License compliance — all dependencies are permissively licensed](09-info-license-compliance.md)
- [10 — Supply chain posture summary](10-info-supply-chain-posture.md)

### By topic

**CVEs / Vulnerabilities**
- [01 — Vite dev server has 3 active CVEs](01-high-vite-cves.md)
- [02 — npm transitive dependencies have active CVEs](02-high-npm-transitive-cves.md)
- [03 — govulncheck is not installed](03-high-govulncheck-gap.md)

**Replace directives / Module hygiene**
- [04 — Phantom and unnecessary replace directives](04-medium-phantom-replace-directives.md)
- [07 — go mod tidy produces a diff](07-medium-go-mod-tidy-dirty.md)

**Version management**
- [05 — OpenTelemetry version skew](05-medium-otel-version-skew.md)
- [08 — Version lag assessment](08-low-version-lag-report.md)

**Unnecessary dependencies**
- [06 — lib/pq is unnecessary in the main module](06-medium-libpq-unnecessary.md)

**License compliance**
- [09 — All dependencies permissively licensed](09-info-license-compliance.md)

**Supply chain architecture**
- [10 — Supply chain posture summary](10-info-supply-chain-posture.md)

## Recommended next steps

1. **Immediate:** Run `npm audit fix` in `ui/` to resolve all 4 npm vulnerabilities (findings 01, 02). All have fixes available.
2. **Immediate:** Remove the phantom `mcp-helpers` replace directive from `go.mod` (finding 04). This unblocks `go list -m -u all` for future update checks.
3. **Short-term:** Install `govulncheck` and run it against the Go module tree (finding 03). Add to CI.
4. **Short-term:** Run `go mod tidy` to clean the `go-isatty` indirect marker (finding 07).
5. **Medium-term:** Align OTel SDK version with core API in the `go-otel` sibling lib (finding 05).
6. **Medium-term:** Move the support-ticket plugin to its own Go module or replace its `lib/pq` import with SQLite (finding 06).

## Known issues skipped

- Plugin scaffold template broken imports (P0-1 in plugin-dev.md) — not supply-chain scope
- Envelope emission broken system-wide — not supply-chain scope
- All items in `docs/beta-known-issues.md` — not supply-chain scope

## Noticed but out of scope

- **Badger v4 as a direct dependency** (`go.mod:8`): Badger is a heavyweight embedded KV store used by `internal/coordination`. If coordination is a small feature, the dependency cost (Badger pulls in flatbuffers, ristretto, xxhash, humanize, and compress) may be disproportionate. Follow-up scope: `dependency-weight-analysis`.
- **`go-keyring` platform dependency** (`go.mod:13`): `zalando/go-keyring` requires D-Bus on Linux and Keychain on macOS. In headless/CI/Docker environments, keyring operations will fail unless `DBUS_SESSION_BUS_ADDRESS` is set or a keyring daemon is running. This could cause silent failures in credential storage. Follow-up scope: `credential-management`.
- **Sibling library Go version misalignment**: `go-otel` and `go-toolbroker` declare `go 1.25.0` while nanite and other sibling libs declare `go 1.26.1`. The Go toolchain handles this gracefully (higher version wins), but it may mask compatibility issues in the sibling libs. Follow-up scope: `sibling-lib-hygiene`.
- **566 total npm packages** for a SPA frontend: The `npm audit` metadata reports 566 total packages (305 prod, 260 dev). This is within normal range for a React+Vite+TailwindCSS+TipTap stack but worth monitoring for bloat. Follow-up scope: `frontend-bundle-analysis`.
