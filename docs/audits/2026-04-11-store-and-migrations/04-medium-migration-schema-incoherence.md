# [Medium] Squashed 001_schema.sql is incoherent with post-squash migrations 002-005

**Scope:** internal/store/migrations/
**Topic:** Migration safety
**Date:** 2026-04-11

## Problem

The squashed `001_schema.sql` was created from a pre-005 state. It defines `a2a_messages` with `from_agent` and `to_agent` columns plus `idx_a2a_inbox` on `to_agent`. Migration `005_a2a_session_scoping.sql` then drops the index, drops both columns, and adds session-scoped replacements. This means every fresh database:

1. Creates `from_agent`/`to_agent` columns (001)
2. Creates `idx_a2a_inbox` referencing `to_agent` (001)
3. Immediately drops the index (005)
4. Immediately drops both columns (005)
5. Adds `from_session_id`/`from_agent_id`/`to_session_id`/`to_agent_id` (005)

Steps 1-4 are wasted DDL on every fresh install and make the schema harder to reason about.

## Evidence

```sql
-- 001_schema.sql:L293-310
CREATE TABLE IF NOT EXISTS a2a_messages (
    ...
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    ...
);
CREATE INDEX IF NOT EXISTS idx_a2a_inbox ON a2a_messages(to_agent, status, created_at DESC);
```

```sql
-- 005_a2a_session_scoping.sql:L4-8
DROP INDEX IF EXISTS idx_a2a_inbox;
ALTER TABLE a2a_messages DROP COLUMN from_agent;
ALTER TABLE a2a_messages DROP COLUMN to_agent;
```

The idempotency handler in `store.go:L100-118` correctly handles the case where `005` runs twice (catches `no such column`), so this is not a correctness bug. It is a maintenance hazard.

## Impact

- **Confusion risk.** A developer reading `001_schema.sql` to understand the current schema sees `from_agent`/`to_agent` columns that don't exist in the running database. The Go code (`a2a.go`) uses `from_session_id`/`from_agent_id`/`to_session_id`/`to_agent_id` -- there's no code path that reads the columns defined in `001`.
- **Next squash.** When `001` is next squashed, the squash must incorporate changes from 002-005. If someone re-squashes naively from the current `001`, they'll bake in the stale columns again.
- **Startup cost.** Minor -- a few extra DDL statements on fresh DBs.

## Recommendation

Re-squash `001_schema.sql` to incorporate migrations 002-005, producing a single coherent schema file. Then delete 002-005. The idempotency handler means existing DBs will continue to work (all DDL uses `IF NOT EXISTS` / `IF EXISTS`). Update the squash comment at the top of `001` to note the new squash date and which migrations were absorbed.

Alternatively, if the squash is deferred: add a comment block at the top of `001_schema.sql` noting that migrations 002-005 modify this schema and the file does NOT represent the current running schema.

## References

- `store.go:L68-123` -- migration runner with idempotency.
- `001_schema.sql:L1` -- "Nanite schema (squashed from 27 migrations, 2026-04-04)" -- the squash predates migration 005.
