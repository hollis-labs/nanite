-- +goose Up
-- Phase 0 item 19 (TASKS/phase-0/19-cut-legacy-rename-tables.md): drop the
-- two rename-artifact tables left behind by the pre-goose
-- rename -> recreate -> copy CHECK-widening pattern:
--   * agent_messages_legacy_089 -- the pre-widen agent_messages, renamed
--     away by migration 090 (090_agent_messages_subagent_result_kind.sql,
--     numbered 089 before 09-adopt-goose-migrations's duplicate-051
--     renumbering) when it added 'subagent_result' to agent_messages.kind's
--     CHECK constraint.
--   * todos_legacy_d1 -- the pre-widen todos, renamed away by migration 043
--     (043_scope_project_reminders_pins_todos.sql) when it swapped
--     todos.scope's CHECK enum (dropped 'workspace', added 'turn') and
--     added project_id.
--
-- Both tables were kept around on purpose under the OLD migration runner:
-- with no schema_migrations ledger, 090/043 re-executed on every boot, and
-- their RENAME TO statement's second-boot failure ("already another
-- table") was silently swallowed specifically because the legacy table
-- still existed to rename onto -- see each file's own header comment.
-- Under goose, 090/043 are tracked in goose_db_version and applied exactly
-- once, ever, so that swallow-and-retry mechanism is no longer how re-run
-- safety is achieved -- the legacy tables have lost their only reason to
-- exist. Confirmed zero Go call sites reference either table
-- (docs/engineering/architecture/05-storage-and-migrations.md,
-- "Confirmed dead, cut" -- re-verified directly for this migration).
--
-- DROP TABLE IF EXISTS is the safe form, consistent with existing drop
-- precedent in this codebase (018_rename_a2a_messages.sql's
-- DROP TABLE IF EXISTS a2a_messages). This also drops each legacy table's
-- own indexes/trigger along with it -- inert, since nothing queries these
-- tables (see TASKS/ESCALATIONS.md's 2026-08-18 "Item 19" entry for a
-- related, separately-tracked finding: those index/trigger NAMES were
-- inadvertently never reclaimed by the live agent_messages/todos tables,
-- which is a pre-existing bug this migration does not fix -- fixing it
-- would change the live tables' schema, which this task's Done-means
-- criteria explicitly does not authorize).

DROP TABLE IF EXISTS agent_messages_legacy_089;

DROP TABLE IF EXISTS todos_legacy_d1;

-- +goose Down
-- Recreates both legacy tables with the exact schema they had immediately
-- before this migration dropped them (verified directly against a real
-- backed-up database:
-- ~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726).
-- This restores structure only, not data -- DROP TABLE is inherently lossy,
-- and neither table has a live writer to replay from, so an empty table of
-- the correct historical shape is the honest, achievable Down for this
-- migration.

CREATE TABLE IF NOT EXISTS agent_messages_legacy_089 (
    id              TEXT PRIMARY KEY,
    thread_id       TEXT,
    reply_to        TEXT REFERENCES agent_messages_legacy_089(id),
    type            TEXT NOT NULL DEFAULT 'message' CHECK(type IN ('message','help_request','directive','status_update','handoff')),
    subject         TEXT,
    body            TEXT NOT NULL,
    metadata        TEXT DEFAULT '{}',
    priority        INTEGER DEFAULT 2,
    status          TEXT DEFAULT 'unread' CHECK(status IN ('unread','read','acknowledged','resolved')),
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    read_at         DATETIME,
    resolved_at     DATETIME,
    from_session_id TEXT NOT NULL DEFAULT '',
    from_agent_id   TEXT NOT NULL DEFAULT '',
    to_session_id   TEXT NOT NULL DEFAULT '',
    to_agent_id     TEXT NOT NULL DEFAULT '',
    channel         TEXT NOT NULL DEFAULT 'chat' CHECK(channel IN ('chat','inbox','alert')),
    kind            TEXT NOT NULL DEFAULT 'notification' CHECK(kind IN ('request','reply','notification','handoff')),
    payload_json    TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_thread
  ON agent_messages_legacy_089(thread_id, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to_session_agent
  ON agent_messages_legacy_089(to_session_id, to_agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_messages_from_session_agent
  ON agent_messages_legacy_089(from_session_id, from_agent_id);

CREATE INDEX IF NOT EXISTS idx_agent_messages_channel
  ON agent_messages_legacy_089(to_session_id, to_agent_id, channel, status, created_at DESC);

CREATE TABLE IF NOT EXISTS todos_legacy_d1 (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL CHECK(scope IN ('workspace', 'project', 'session')),
    scope_id TEXT NOT NULL DEFAULT '',
    parent_id TEXT REFERENCES todos_legacy_d1(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'in_progress', 'done', 'blocked')),
    priority TEXT NOT NULL DEFAULT 'medium' CHECK(priority IN ('low', 'medium', 'high', 'critical')),
    labels TEXT NOT NULL DEFAULT '[]',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_todos_scope ON todos_legacy_d1(scope, scope_id);
CREATE INDEX IF NOT EXISTS idx_todos_parent ON todos_legacy_d1(parent_id);
CREATE INDEX IF NOT EXISTS idx_todos_status ON todos_legacy_d1(status);

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS trg_todos_updated_at
AFTER UPDATE ON todos_legacy_d1
FOR EACH ROW
BEGIN
    UPDATE todos_legacy_d1 SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;
-- +goose StatementEnd
