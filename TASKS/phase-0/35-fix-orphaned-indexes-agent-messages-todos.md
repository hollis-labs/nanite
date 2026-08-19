# Fix silently-orphaned indexes/trigger on agent_messages and todos

**Phase:** 0
**Status:** implemented
**Depends on:** `19-cut-legacy-rename-tables.md` (must land first — the name collisions this task fixes only clear once `agent_messages_legacy_089`/`todos_legacy_d1` are actually dropped)
**Touches:** new migration file (numbered after `095_drop_legacy_rename_tables.sql`)

## Context

Found during Phase 1-6 planning verification (2026-08-18) while investigating an unrelated Phase 4 reaper question — a real, currently-live performance gap. Independently confirmed against a copy of the pre-execution DB backup.

Two separate rename chains hit the same SQLite behavior: `RENAME TABLE` carries index/trigger names forward onto the renamed (legacy) table, so a later `CREATE INDEX IF NOT EXISTS`/`CREATE TRIGGER IF NOT EXISTS` targeting the same name against the *new* table silently no-ops — the name is already claimed by the legacy table's copy of it.

**`agent_messages`** (4 orphaned indexes): `018_rename_a2a_messages.sql` creates `a2a_messages` → renamed to `agent_messages` at line 33, with `idx_agent_messages_thread`/`idx_agent_messages_to_session_agent`/`idx_agent_messages_from_session_agent`/`idx_agent_messages_channel` created at lines 42/45/48/51. `090_agent_messages_subagent_result_kind.sql:28` renames this table to `agent_messages_legacy_089`, carrying all 4 index names with it; lines 60-70's `CREATE INDEX IF NOT EXISTS` for the same 4 names against the new `agent_messages` all silently no-op. Only `idx_agent_messages_kind_unread` (a genuinely new name, lines 72-73) actually exists on live `agent_messages` today.

**`todos`** (3 orphaned indexes + 1 orphaned trigger): `003_todos_and_plans.sql` creates `idx_todos_scope`/`idx_todos_parent`/`idx_todos_status` (lines 20/21/22) and `trg_todos_updated_at` (line 48). `043_scope_project_reminders_pins_todos.sql:65` renames `todos` to `todos_legacy_d1`, carrying all 4 names forward; the re-attempts at lines 98-100/111-116 all silently no-op. Only `idx_todos_project` (line 101, new column) actually exists on live `todos` today.

Both legacy tables (`agent_messages_legacy_089`, `todos_legacy_d1`) are already scheduled for removal by Phase 0 item 19, `095_drop_legacy_rename_tables.sql:38,40` — dropping them also drops their attached orphaned indexes/trigger, which is what finally clears the name collision and makes it safe to create the real ones. That's why this task depends on 19.

## What to do

1. **After task 19 lands**: verify `095_drop_legacy_rename_tables.sql` has actually run and both legacy tables are gone — confirm this directly against the schema, don't just trust `TASKS/INDEX.md`'s status column (per this project's log-integrity rule).
2. Add one new migration creating the 7 real indexes + 1 real trigger against the live tables:
   - `agent_messages`: `idx_agent_messages_thread`, `idx_agent_messages_to_session_agent`, `idx_agent_messages_from_session_agent`, `idx_agent_messages_channel`.
   - `todos`: `idx_todos_scope`, `idx_todos_parent`, `idx_todos_status`, `trg_todos_updated_at`.
3. Verify each index/trigger's original definition (cited above) still makes sense against the current table shape — columns may have changed across the many migrations between original creation and now. Don't blindly replay the old SQL; confirm it's still correct first.
4. Confirm via `EXPLAIN QUERY PLAN` (or equivalent) on the real query patterns these indexes were meant to serve that they're actually selected post-creation — this is the whole point of the fix, worth verifying it actually lands rather than just trusting `CREATE INDEX` succeeded.

## Done means

- All 7 indexes + 1 trigger exist on live `agent_messages`/`todos`, confirmed by direct schema query, not just migration-file presence.
- `EXPLAIN QUERY PLAN` confirms at least the primary lookup patterns each index was meant to serve now use it.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Added `internal/store/migrations/101_fix_orphaned_indexes_agent_messages_todos.sql`: creates the 4 real `agent_messages` indexes and the 3 `todos` indexes + `trg_todos_updated_at` trigger against the live tables, now that `19-cut-legacy-rename-tables` dropped the legacy tables that were silently claiming those names. One deliberate correction from the original definitions: `trg_todos_updated_at` now writes RFC3339 (matching every live app write path) instead of the original's space-separated `CURRENT_TIMESTAMP` format, which would have silently overwritten app-set timestamps with a mismatched format — flagged as a correction, not a deviation from task scope. Verified via `EXPLAIN QUERY PLAN` against a real copy of the production backup DB: all 7 indexes + trigger created, row counts unchanged, query plans confirm index usage on the primary lookup patterns. `go build`/`go vet`/`go test ./...` pass. No escalations.

Committed as `44812224` ("Phase 0 #35: fix silently-orphaned indexes/trigger on agent_messages and todos").

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
