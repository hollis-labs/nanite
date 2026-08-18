-- +goose Up
-- CW-20260420-0012 — SessionObjects: session-scoped structured-payload store (P2 primitive)
--
-- Parallel to tool_result_cache (internal/tool/cache.go) but distinct because:
--   (a) payload is opaque JSON with no tool-specific columns
--   (b) lifecycle is session-end cleanup, not per-entry TTL
--
-- D5 invariant: lookups must be scoped by both session_id and id. Cross-session
-- reads must fail closed. Eviction runs inside Store.ArchiveSession's tx so
-- archival + cleanup are atomic.

CREATE TABLE IF NOT EXISTS session_objects (
    id            TEXT NOT NULL PRIMARY KEY,        -- ULID (26 chars)
    session_id    TEXT NOT NULL,
    content_type  TEXT NOT NULL DEFAULT 'application/json',
    byte_size     INTEGER NOT NULL,
    payload       TEXT NOT NULL,                    -- opaque JSON, validated by consumer
    created_at    TEXT NOT NULL,                    -- RFC3339
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

-- Composite index covers both the WHERE (session_id) and ORDER BY
-- (created_at DESC, id DESC) of ListSessionObjects in one index scan.
CREATE INDEX IF NOT EXISTS idx_session_objects_session_created_id
    ON session_objects(session_id, created_at DESC, id DESC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
