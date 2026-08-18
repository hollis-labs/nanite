-- Phase 0 item 26 (TASKS/phase-0/26-cut-session-compaction-summary-fields.md):
-- drop sessions.compaction_summary/compacted_at and the write-only
-- UpdateSessionCompaction accessor that populated them.
--
-- TASKS.md Phase 0 Cuts item 26 and decision log §24 both call these
-- columns dead ("zero call sites"). That framing was inaccurate as of this
-- migration: internal/api/sessions.go's handleCompactSession (the manual
-- POST /api/sessions/{id}/compact handler) had one real, reachable call to
-- UpdateSessionCompaction. The columns were still write-only, though — the
-- store.Session Go struct never declared CompactionSummary/CompactedAt
-- fields, so GetSession/ListSessions never read them back, and the
-- handler's JSON response already computed its "summary" field entirely
-- in-memory from the compaction pipeline's result rather than from a DB
-- round-trip. The fix landed in the same change as this migration: the
-- UpdateSessionCompaction call was deleted from handleCompactSession (the
-- endpoint's response shape is unaffected), and UpdateSessionCompaction
-- itself was deleted from internal/store/sessions.go and the
-- service.SessionWriter interface. This migration is the schema half of
-- that same cut.
--
-- Follows migration 064_drop_agent_profiles_default_mode.sql's shape: a
-- bare ALTER TABLE ... DROP COLUMN per statement, no explicit transaction
-- wrapper. That precedent predates the real goose runner (goose wraps each
-- migration file in its own managed transaction already), but there is no
-- reason to reintroduce a redundant explicit BEGIN/END here either.

-- +goose Up
ALTER TABLE sessions DROP COLUMN compaction_summary;
ALTER TABLE sessions DROP COLUMN compacted_at;

-- +goose Down
ALTER TABLE sessions ADD COLUMN compaction_summary TEXT;
ALTER TABLE sessions ADD COLUMN compacted_at DATETIME;
