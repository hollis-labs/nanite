-- +goose Up
-- S7 T9: Subagent run lifecycle.
--
-- A subagent run is "the primary agent asked for a child agent to
-- handle a subtask" — spawn / run / complete, with state tracked
-- here so parent streams can observe status_changed events.
--
-- Lifecycle states:
--   requested   — Spawn was called, waiting for approval (interactive mode)
--   approved    — user approved (api/yolo skip straight to running)
--   running     — runner started
--   completed   — runner returned a result
--   failed      — runner errored or timed out
--   cancelled   — parent issued Cancel

CREATE TABLE IF NOT EXISTS subagent_runs (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT NOT NULL,
    child_session_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'sync'
        CHECK(mode IN ('sync','async','api')),
    status TEXT NOT NULL DEFAULT 'requested'
        CHECK(status IN ('requested','approved','running','completed','failed','cancelled')),
    inputs_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_subagent_runs_parent
  ON subagent_runs(parent_session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_subagent_runs_status
  ON subagent_runs(status, created_at);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
