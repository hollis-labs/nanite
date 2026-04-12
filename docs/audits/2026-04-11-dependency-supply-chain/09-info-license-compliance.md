# [Info] License compliance — all dependencies are permissively licensed

**Scope:** go.mod + ui/package.json — license audit
**Topic:** License compliance
**Date:** 2026-04-11

## Problem

No problem found. This is a positive finding.

## Evidence

### Go direct dependencies — license check

| Module | License |
|--------|---------|
| `github.com/creack/pty` | MIT |
| `github.com/dgraph-io/badger/v4` | Apache-2.0 |
| `github.com/google/uuid` | BSD-3-Clause |
| `github.com/hollis-labs/*` (all sibling libs) | Private (same org) |
| `github.com/mark3labs/mcp-go` | MIT |
| `github.com/zalando/go-keyring` | MIT |
| `github.com/lib/pq` | MIT |
| `github.com/santhosh-tekuri/jsonschema/v6` | Apache-2.0 |
| `modernc.org/sqlite` | BSD-3-Clause |
| `go.opentelemetry.io/otel` | Apache-2.0 |
| `google.golang.org/grpc` | Apache-2.0 |
| `google.golang.org/protobuf` | BSD-3-Clause |
| `gopkg.in/yaml.v3` | MIT + Apache-2.0 (dual) |

All transitive Go dependencies checked are MIT, BSD-3-Clause, Apache-2.0, or ISC. **No GPL, LGPL, AGPL, or SSPL dependencies found.**

### Frontend dependencies

The npm ecosystem packages (React, Vite, TailwindCSS, Zustand, Radix UI, etc.) are all MIT-licensed. No copyleft dependencies were found in the `package.json` dependency tree.

## Impact

No license compliance issues. All dependencies are compatible with proprietary and open-source distribution.

## Recommendation

No action needed. Consider adding an automated license checker (e.g., `go-licenses` for Go, `license-checker` for npm) to CI to prevent accidental introduction of copyleft dependencies.

## References

- License files verified in `/Users/chrispian/go/pkg/mod/` cache
- `ui/package.json` — all frontend dependencies
