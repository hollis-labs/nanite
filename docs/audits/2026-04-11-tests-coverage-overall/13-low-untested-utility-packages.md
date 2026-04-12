# [Low] Untested utility packages: secrets, brand, version, crossapp, allplugins, skill/builtin

**Scope:** Multiple packages
**Topic:** Test Quality — utility package gaps
**Date:** 2026-04-11

## Problem

6 small utility packages have no test files. Combined they total ~267 lines of code.

## Evidence

| Package | File | Lines | Justification for no tests |
|---------|------|-------|---------------------------|
| `internal/secrets` | `keyring.go` | 47 | Thin wrapper around `go-keyring`. Testing requires OS keychain access. Absence is **partially justified** — a mock-based test could verify the wrapper logic without touching the real keychain. |
| `internal/brand` | `brand.go` | 41 | Constants and string formatting only. Absence is **justified**. |
| `internal/version` | `version.go` | 14 | Single `const Version = "0.3.0-beta"`. Absence is **justified**. |
| `internal/crossapp` | `engine_client.go` | 97 | HTTP client for inter-service calls. Absence is a **gap** — this is a trust boundary. |
| `internal/plugin/allplugins` | `allplugins.go` | 26 | Import aggregator (blank imports). Absence is **justified**. |
| `internal/skill/builtin` | `embed.go` | 39 | Embedded filesystem for built-in skills. Absence is **justified**. |

## Impact

Most of these are justified absences. `internal/crossapp/engine_client.go` at 97 lines is the only real gap — it makes HTTP requests to external services. `internal/secrets/keyring.go` stores provider API keys in the OS keychain; a bug in the wrapper could cause silent key loss.

## Recommendation

1. Add `crossapp/engine_client_test.go` with an httptest mock verifying request construction and error handling
2. Optionally add `secrets/keyring_test.go` with a mock keyring interface to verify wrapper logic

The remaining packages (brand, version, allplugins, skill/builtin) can remain untested.

## References

- `cmd/nanite/main.go` — crossapp client used for Engine API calls
- `internal/secrets/keyring.go` — provider key storage
