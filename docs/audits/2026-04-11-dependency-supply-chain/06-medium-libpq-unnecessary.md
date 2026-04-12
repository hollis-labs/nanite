# [Medium] lib/pq (PostgreSQL driver) is a direct dependency but nanite uses SQLite

**Scope:** go.mod + plugins/support-ticket — unnecessary dependency
**Topic:** Supply chain — attack surface
**Date:** 2026-04-11

## Problem

`github.com/lib/pq v1.12.0` is listed as a direct (non-indirect) dependency in `go.mod`. Nanite uses SQLite exclusively via `modernc.org/sqlite`. The only consumer of `lib/pq` is the `support-ticket` example plugin.

## Evidence

`go.mod:56`:
```
github.com/lib/pq v1.12.0
```

Note: this line lacks the `// indirect` comment, making it appear as a direct dependency.

```
$ go mod why github.com/lib/pq
# github.com/lib/pq
github.com/hollis-labs/nanite/plugins/support-ticket
github.com/lib/pq
```

The only import is in `plugins/support-ticket/kb.go:10`:
```go
import "github.com/lib/pq"
```

No other Go file in the nanite codebase imports `lib/pq`. The `support-ticket` plugin was described in `backend.md` as an example plugin.

## Impact

- Adds a PostgreSQL driver to the dependency tree unnecessarily, increasing the attack surface.
- `lib/pq` is in maintenance mode — the Go community has largely moved to `pgx`. While `lib/pq` is MIT-licensed and has no current CVEs, keeping an unnecessary database driver in the dependency graph is poor supply-chain hygiene.
- The `go mod tidy` diff shows `go-isatty` should change from indirect to direct, but `lib/pq` is not flagged for removal — meaning `go mod tidy` considers it legitimately needed (via the plugin).
- If the support-ticket plugin is meant to be an example/template, it should ideally use the same database (SQLite) as the host.

## Recommendation

If the support-ticket plugin genuinely needs PostgreSQL, move it to its own Go module with a separate `go.mod` (consistent with the plugin-per-module model). If it doesn't need PostgreSQL, replace the import with SQLite. Either way, `lib/pq` should not be a direct dependency of the main nanite module.

## References

- `go.mod:56`
- `plugins/support-ticket/kb.go:10`
- https://github.com/lib/pq — maintenance mode notice in README
