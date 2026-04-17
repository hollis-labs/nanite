# [Info] Praise and positive observations

**Scope:** internal/store/
**Topic:** Info
**Date:** 2026-04-11

## SQL injection: Clean

Every query in the store package uses parameter placeholders (`?`). No string concatenation of user input into SQL was found. The one instance of `fmt.Sprintf` for a table name (`agents.go:L205`) uses a hardcoded string slice, not user input. This is the primary security concern for the scope and it passes cleanly.

Queries checked across all 24 `.go` files in the package (excluding tests). Total query count: ~120 SELECT/INSERT/UPDATE/DELETE statements. All parameterized.

## Transaction discipline

`Seed()`, `SeedProviders()`, and `CreateMessage()` correctly use transactions with `defer tx.Rollback()` and explicit `tx.Commit()`. The `defer tx.Rollback()` pattern is correct -- calling Rollback after Commit is a no-op in `database/sql`.

## COALESCE discipline

Nullable columns consistently use `COALESCE(column, default)` in SELECT statements to avoid nil scan issues. This is applied uniformly across sessions, messages, agents, bookmarks, artifacts, and workspaces. The two exceptions (ListAgentSkills, ListPromptTemplatesForAgent) are bugs, not omissions -- the COALESCE is missing from the SELECT because the column itself is missing.

## Test coverage

The package has 15 test files covering the major CRUD paths. Tests use `t.TempDir()` for isolated SQLite databases, which is the correct pattern for test isolation. The `-race` run passes cleanly.

## Seed design

The separation of DDL (migrations) from seed data (`seed.go`) is clean and explicitly documented. `SeedProviders()` is designed for boot-time upsert with `INSERT OR IGNORE`, which is the right pattern for idempotent seeding.

## WAL + busy_timeout configuration

The WAL mode + 5-second busy timeout is appropriate for a single-user application with concurrent read/write access from API handlers and background workers.

## References

- This finding is informational. No action required.
