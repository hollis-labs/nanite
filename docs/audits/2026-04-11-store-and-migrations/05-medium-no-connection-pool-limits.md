# [Medium] No connection pool size limits configured for SQLite

**Scope:** internal/store/store.go
**Topic:** Connection pool / Concurrency
**Date:** 2026-04-11

## Problem

The `Store.New()` function opens a SQLite connection with WAL mode and `busy_timeout=5000` but does not set `SetMaxOpenConns`, `SetMaxIdleConns`, or `SetConnMaxLifetime` on the `*sql.DB`. The `database/sql` default is unlimited open connections.

## Evidence

```go
// store.go:L32-61
func New(dbPath string) (*Store, error) {
    // ...
    db, err := sql.Open("sqlite", dbPath)
    // ...
    // Enable WAL mode and foreign keys.
    for _, pragma := range []string{
        "PRAGMA journal_mode=WAL",
        "PRAGMA foreign_keys=ON",
        "PRAGMA busy_timeout=5000",
    } {
        if _, err := db.Exec(pragma); err != nil {
            // ...
        }
    }
    // No SetMaxOpenConns / SetMaxIdleConns
    // ...
}
```

## Impact

SQLite WAL mode supports concurrent reads but only one writer at a time. With unlimited connections:

1. **PRAGMA consistency.** PRAGMAs like `foreign_keys=ON` and `journal_mode=WAL` are per-connection settings. When the pool opens a new connection, it does NOT re-apply the PRAGMAs. `modernc.org/sqlite` with `database/sql` creates new connections as needed, and these new connections will have the default PRAGMA values (`foreign_keys=OFF`, journal_mode potentially not WAL). This is a **correctness risk** -- any query that runs on a newly-pooled connection that was not part of the initial batch may bypass FK checks.

2. **Writer contention.** Multiple concurrent writes from different goroutines each acquire a separate connection and attempt to write. SQLite serializes writers; with `busy_timeout=5000`, writers wait up to 5 seconds. With many goroutines writing concurrently (e.g., SSE presence events, message creation, usage recording, event logging), contention increases.

3. **FindAgent FK bypass path.** The `DeleteAgent` function explicitly disables FKs (finding 01), but even without that, any new connection from the pool may already have FKs disabled.

## Recommendation

Set `MaxOpenConns(1)` for write-heavy SQLite workloads, or use the connection string DSN to set PRAGMAs that apply per-connection. With `modernc.org/sqlite`, PRAGMAs can be set via the connection string:

```go
dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", dbPath)
db, err := sql.Open("sqlite", dsn)
```

This ensures every connection from the pool has the correct PRAGMAs applied.

Alternatively, set pool limits:
```go
db.SetMaxOpenConns(4)  // WAL allows concurrent reads
db.SetMaxIdleConns(4)
db.SetConnMaxLifetime(0)  // SQLite connections are cheap
```

And use a `ConnInitHook` or re-run PRAGMAs on each new connection via a wrapper.

The recommended fix is the DSN-based PRAGMA approach, which is both simpler and more correct.

## References

- `modernc.org/sqlite` DSN documentation: `_pragma` query parameter.
- SQLite WAL docs: concurrent readers, single writer.
- Finding 01 in this audit (FK disable in DeleteAgent) compounds with this issue.
