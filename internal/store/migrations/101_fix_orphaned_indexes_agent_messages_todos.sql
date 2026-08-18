-- +goose Up
-- Phase 0 item 35 (TASKS/phase-0/35-fix-orphaned-indexes-agent-messages-todos.md):
-- create the 7 indexes + 1 trigger that have been silently orphaned since
-- their originating migrations ran, now that 095_drop_legacy_rename_tables.sql
-- has cleared the name collision that caused the no-op.
--
-- Root cause: SQLite's `ALTER TABLE ... RENAME TO` carries index/trigger
-- names forward onto the renamed (legacy) table. Two rename chains hit this:
--   * 018_rename_a2a_messages.sql creates idx_agent_messages_thread /
--     idx_agent_messages_to_session_agent / idx_agent_messages_from_session_agent
--     / idx_agent_messages_channel on agent_messages. 090_agent_messages_
--     subagent_result_kind.sql later renamed that table to
--     agent_messages_legacy_089, carrying all 4 index names with it — its own
--     `CREATE INDEX IF NOT EXISTS` calls for the same 4 names against the
--     *new* agent_messages table silently no-op'd (name already claimed).
--   * 003_todos_and_plans.sql creates idx_todos_scope / idx_todos_parent /
--     idx_todos_status / trg_todos_updated_at on todos.
--     043_scope_project_reminders_pins_todos.sql later renamed that table to
--     todos_legacy_d1, carrying all 4 names with it — same silent no-op.
--
-- Both legacy tables (and their attached orphaned indexes/trigger) were
-- dropped by 095, which is what finally frees these 8 names to be reclaimed
-- here. See TASKS/phase-0/35-fix-orphaned-indexes-agent-messages-todos.md
-- for the full investigation and TASKS/phase-0/19-cut-legacy-rename-tables.md
-- for the drop that unblocked this.
--
-- Verified against the live schema (backup DB post-095) that every column
-- referenced below still exists on agent_messages/todos with the same
-- meaning as when each index/trigger was first defined — replaying the
-- original definitions verbatim is correct for the 7 indexes.
--
-- trg_todos_updated_at is the one exception: its original body
-- (`SET updated_at = CURRENT_TIMESTAMP`) used SQLite's default
-- space-separated timestamp format ("YYYY-MM-DD HH:MM:SS"), but every
-- application write path that sets todos.updated_at today
-- (internal/store/todos.go's UpdateTodo/UpdateTodoScope, and the initial
-- CreateTodo insert) already writes RFC3339 ("...T...Z") explicitly. Because
-- this trigger fires AFTER UPDATE and is a second, separate UPDATE
-- statement, replaying the original body verbatim would silently overwrite
-- every app-set RFC3339 updated_at with a differently-formatted value on
-- every single todo update — a real behavior regression this table's own
-- callers don't expect (recursive_triggers is off by default, so the
-- trigger's own UPDATE does not refire itself; this is a format bug, not a
-- recursion risk). Recreated below with the same RFC3339 format
-- (`strftime('%Y-%m-%dT%H:%M:%SZ','now')`) internal/store/todos.go's
-- UpdateTodoScope already uses, so the trigger is a same-format backstop
-- consistent with the app's own convention rather than a source of drift.

CREATE INDEX IF NOT EXISTS idx_agent_messages_thread
  ON agent_messages(thread_id, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to_session_agent
  ON agent_messages(to_session_id, to_agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_messages_from_session_agent
  ON agent_messages(from_session_id, from_agent_id);

CREATE INDEX IF NOT EXISTS idx_agent_messages_channel
  ON agent_messages(to_session_id, to_agent_id, channel, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_todos_scope ON todos(scope, scope_id);
CREATE INDEX IF NOT EXISTS idx_todos_parent ON todos(parent_id);
CREATE INDEX IF NOT EXISTS idx_todos_status ON todos(status);

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS trg_todos_updated_at
AFTER UPDATE ON todos
FOR EACH ROW
BEGIN
    UPDATE todos SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS trg_todos_updated_at;

DROP INDEX IF EXISTS idx_todos_status;
DROP INDEX IF EXISTS idx_todos_parent;
DROP INDEX IF EXISTS idx_todos_scope;

DROP INDEX IF EXISTS idx_agent_messages_channel;
DROP INDEX IF EXISTS idx_agent_messages_from_session_agent;
DROP INDEX IF EXISTS idx_agent_messages_to_session_agent;
DROP INDEX IF EXISTS idx_agent_messages_thread;
