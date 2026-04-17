# [Low] No indexes on tag columns for any entity

**Scope:** internal/store/migrations/001_schema.sql
**Topic:** Querying patterns / Performance
**Date:** 2026-04-11

## Problem

Tags are stored as JSON arrays in `TEXT` columns on `sessions`, `agent_profiles`, and `bookmarks`. None of these columns are indexed. Any tag-based filtering requires a full table scan with JSON parsing.

## Evidence

Schema grep for indexes on tag columns:

- `sessions`: Indexes exist for `workspace_id`, `is_pinned`, `status` -- no tag index.
- `agent_profiles`: No indexes at all beyond the PK and `slug` UNIQUE.
- `bookmarks`: Index on `session_id` -- no tag index.

Currently, no code path queries by tag for sessions or agents in the store layer:
- Session tags are only read (not filtered) in `ListSessions` and `GetSession`.
- Agent tags are only read in `GetAgent`, `ListAgents`.
- Bookmark tags are dead (finding 04).

The memory service has its own tag filtering (`internal/api/memories.go:L66-76`) but that goes through the Conduit backend, not SQLite.

## Impact

Low impact today because no store queries filter by tag. If tag-based queries are added (e.g., "list all sessions tagged 'refactor'"), they would require `json_each` with no supporting index, causing full table scans. For the current session count (likely < 10,000 for a single-user tool), this is acceptable. At scale it would degrade.

## Recommendation

Defer indexing until tag-based query features are implemented. When that happens, consider either:

1. **SQLite JSON1 index:** `CREATE INDEX idx_sessions_tags ON sessions(tags)` won't help with `json_each`. Instead, a generated column + index pattern:
   ```sql
   -- Virtual table for tag search
   CREATE VIRTUAL TABLE IF NOT EXISTS session_tags_fts USING fts5(session_id, tag);
   ```

2. **Normalize to a join table.** Extract tags into `session_tags(session_id, tag)` with an index. This is the relational-correct approach but requires migrating from the JSON column pattern.

For now, the JSON array approach is fine for the workload.

## References

- `internal/store/migrations/001_schema.sql:L100` -- sessions tags column.
- `internal/store/migrations/001_schema.sql:L52` -- agent_profiles tags column.
- `internal/store/migrations/001_schema.sql:L146` -- bookmarks tags column.
