-- +goose Up
-- CW-20260420-0027 — compaction_events: structured metadata from compaction pipeline (P8 CompactionContract Part C)
--
-- Emitted by the compaction pipeline after successful stage execution. Records what was
-- preserved, what was evicted, which stages ran, and token counts for analytics/recovery.
-- Links to handoff_stashes.id when a stash was written (P7 integration).
--
-- Idempotent: CREATE TABLE IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS compaction_events (
    id TEXT NOT NULL PRIMARY KEY,
    session_id TEXT NOT NULL,
    coverage_window_start TEXT,
    coverage_window_end TEXT,
    evicted_cache_pointers TEXT,
    preserved_sources TEXT,
    summary_mode TEXT NOT NULL,
    summary_token_count INTEGER NOT NULL DEFAULT 0,
    original_token_count INTEGER NOT NULL DEFAULT 0,
    handoff_stash_id TEXT,
    stages_applied TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_compaction_events_session
    ON compaction_events(session_id, created_at DESC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
