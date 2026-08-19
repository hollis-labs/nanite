-- +goose Up
-- CW-20260512-0019: widen agent_messages.kind to add 'subagent_result'.
--
-- Subagent completions (all modes — sync/async/api/interactive) post a
-- reply message today via Kind=reply, which is indistinguishable from an
-- ordinary agent reply. Layer 1 of the emit->react model (CW-20260512-0019
-- / CW-20260520-0001) needs a durable, filterable marker so:
--   - chat_generate.go can query "pending subagent results" at turn start
--     without false-positiving on unrelated reply traffic.
--   - the harness-reaction layer (CW-20260520-0001) can react specifically
--     to this kind without touching the broader reply/request/notification/
--     handoff vocabulary.
--
-- SQLite cannot ALTER a CHECK constraint in place, so this follows the
-- rename + recreate + copy pattern from 043_scope_project_reminders_pins_todos.sql.
--
-- Idempotency: the migration runner re-executes every boot (no
-- schema_migrations ledger). Each statement here must be safe on rerun:
--   * RENAME — second-boot failure ("already another table") is swallowed
--     by the runner (see store.go), so the live `agent_messages` keeps its
--     name.
--   * CREATE TABLE IF NOT EXISTS — no-op when the new schema exists.
--   * INSERT OR IGNORE — PK collisions on rerun skip silently.
--   * agent_messages_legacy_089 is intentionally NOT dropped, so
--     subsequent runs find it for the rename-swallow + INSERT-OR-IGNORE
--     no-op path.

ALTER TABLE agent_messages RENAME TO agent_messages_legacy_089;

CREATE TABLE IF NOT EXISTS agent_messages (
    id              TEXT PRIMARY KEY,
    from_session_id TEXT NOT NULL,
    from_agent_id   TEXT NOT NULL,
    to_session_id   TEXT NOT NULL,
    to_agent_id     TEXT NOT NULL,
    thread_id       TEXT,
    reply_to        TEXT REFERENCES agent_messages(id),
    type            TEXT NOT NULL DEFAULT 'message' CHECK(type IN ('message','help_request','directive','status_update','handoff')),
    subject         TEXT,
    body            TEXT NOT NULL,
    metadata        TEXT DEFAULT '{}',
    priority        INTEGER DEFAULT 2,
    status          TEXT DEFAULT 'unread' CHECK(status IN ('unread','read','acknowledged','resolved')),
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    read_at         DATETIME,
    resolved_at     DATETIME,
    channel         TEXT NOT NULL DEFAULT 'chat' CHECK(channel IN ('chat','inbox','alert')),
    kind            TEXT NOT NULL DEFAULT 'notification' CHECK(kind IN ('request','reply','notification','handoff','subagent_result')),
    payload_json    TEXT NOT NULL DEFAULT '{}'
);

INSERT OR IGNORE INTO agent_messages (id, from_session_id, from_agent_id, to_session_id, to_agent_id,
                                       thread_id, reply_to, type, subject, body, metadata, priority,
                                       status, created_at, read_at, resolved_at, channel, kind, payload_json)
SELECT id, from_session_id, from_agent_id, to_session_id, to_agent_id,
       thread_id, reply_to, type, subject, body, metadata, priority,
       status, created_at, read_at, resolved_at, channel, kind, payload_json
FROM agent_messages_legacy_089;

CREATE INDEX IF NOT EXISTS idx_agent_messages_thread
  ON agent_messages(thread_id, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to_session_agent
  ON agent_messages(to_session_id, to_agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_messages_from_session_agent
  ON agent_messages(from_session_id, from_agent_id);

CREATE INDEX IF NOT EXISTS idx_agent_messages_channel
  ON agent_messages(to_session_id, to_agent_id, channel, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_messages_kind_unread
  ON agent_messages(to_session_id, to_agent_id, kind, status);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
