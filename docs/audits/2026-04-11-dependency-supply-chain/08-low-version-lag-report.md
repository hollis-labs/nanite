# [Low] Version lag assessment for major dependencies

**Scope:** go.mod + ui/package.json — version currency
**Topic:** Supply chain — version lag
**Date:** 2026-04-11

## Problem

Several dependencies are not at their latest available versions. This is normal for a project under active development but should be monitored for security patches.

## Evidence

### Go dependencies — version lag assessment

Because `go list -m -u all` fails (see finding 03 — phantom `mcp-helpers` replace), a full automated update check was not possible. Manual assessment of key dependencies based on known latest versions:

| Module | Current | Status |
|--------|---------|--------|
| `modernc.org/sqlite` | v1.48.1 | Current (as of 2026-04) |
| `go.opentelemetry.io/otel` | v1.43.0 | Current |
| `go.opentelemetry.io/otel/sdk` | v1.41.0 | 2 minor behind (see finding 05) |
| `google.golang.org/grpc` | v1.79.2 | Current |
| `google.golang.org/protobuf` | v1.36.11 | Current |
| `github.com/mark3labs/mcp-go` | v0.44.1 | Pre-1.0; frequent releases — check for updates |
| `github.com/creack/pty` | v1.1.24 | Current |
| `github.com/dgraph-io/badger/v4` | v4.9.1 | Current |
| `github.com/google/uuid` | v1.6.0 | Current |
| `gopkg.in/yaml.v3` | v3.0.1 | Current (stable, infrequent releases) |
| `golang.org/x/net` | v0.52.0 | Current |
| `golang.org/x/sys` | v0.42.0 | Current |

**Go version:** `go 1.26.1` declared in `go.mod`. Current.

### Frontend dependencies — version lag assessment

| Package | Current | Notes |
|---------|---------|-------|
| `react` | 19.2.0 | Current |
| `vite` | 7.3.1 | Has CVEs (see finding 01) |
| `typescript` | 5.9.3 | Current |
| `tailwindcss` | 4.2.1 | Current |
| `@tanstack/react-query` | 5.90.21 | Current |
| `zustand` | 5.0.11 | Current |
| `radix-ui` | 1.4.3 | Current |
| `@biomejs/biome` | 2.4.7 | Current |

Overall the project is well-maintained for version currency. The primary gaps are the Vite CVEs and the OTel SDK skew.

## Impact

Version lag itself is not a vulnerability, but outdated dependencies may miss security patches. The inability to run `go list -m -u all` (finding 03) means there is no automated way to detect when a Go dependency falls behind.

## Recommendation

1. Fix the phantom `mcp-helpers` replace so `go list -m -u all` works
2. Set up a periodic dependency update check (Dependabot, Renovate, or manual `go list -m -u all` + `npm outdated`)
3. Prioritize the Vite update (finding 01) and OTel alignment (finding 05)

## References

- `go.mod` — all Go dependency versions
- `ui/package.json` — all frontend dependency versions
