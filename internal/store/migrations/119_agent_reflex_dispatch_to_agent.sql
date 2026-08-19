-- +goose Up
-- +goose NO TRANSACTION
-- Phase 4 item 02
-- (TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md):
-- adds `dispatch_to_agent` to agent_reflexes.action_kind's CHECK constraint.
--
-- dispatch_to_agent is the reflex absorption of the agent broker's real
-- intent (architecture doc 03-steering.md, "Reflexes are the single
-- steering primitive") — a reflex whose trigger fires routes the current
-- turn to a different agent profile instead of (or alongside) the chat
-- agent handling it directly. See internal/store/agent_reflexes.go's
-- ReflexActionDispatchToAgent doc comment for the action_spec shape and
-- internal/service/chat_reflex_dispatch.go for the executor call site
-- that replaces the retired internal/service/chat_broker_dispatch.go.
--
-- SQLite cannot narrow/widen a CHECK constraint in place, so the table is
-- rebuilt via the rename-recreate-copy pattern established by
-- 074_agent_profiles_multi_agent.sql and most recently used by
-- 105_drop_unused_session_status_values.sql. The column shape below
-- mirrors 075_agent_reflexes.sql's original CREATE TABLE plus the
-- opt_out_allowed column 115_agent_reflex_opt_out.sql added — the live
-- schema as of this migration. agent_reflex_opt_outs' FK to
-- agent_reflexes(id) (ON DELETE CASCADE) is not affected: with
-- foreign_keys OFF during the DROP+RENAME, SQLite does not enforce/cascade
-- the constraint mid-migration, and by commit time agent_reflexes exists
-- again with the same row ids, so referential integrity is intact.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_reflexes_new (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT REFERENCES agent_profiles(id),
    class_tag       TEXT,
    name            TEXT NOT NULL,
    trigger_kind    TEXT NOT NULL CHECK (
        trigger_kind IN ('predicate','event','interval')
    ),
    trigger_spec    TEXT NOT NULL,
    action_kind     TEXT NOT NULL CHECK (
        action_kind IN ('inject_reminder','force_tool_choice','send_message','halt_session','add_schedule','dispatch_to_agent')
    ),
    action_spec     TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    priority        INTEGER NOT NULL DEFAULT 0,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL,
    opt_out_allowed BOOLEAN NOT NULL DEFAULT TRUE
);

INSERT INTO agent_reflexes_new
    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
     action_kind, action_spec, status, priority, fired_count,
     last_fired_at, created_at, created_by, opt_out_allowed)
SELECT
    id, agent_id, class_tag, name, trigger_kind, trigger_spec,
    action_kind, action_spec, status, priority, fired_count,
    last_fired_at, created_at, created_by, opt_out_allowed
FROM agent_reflexes;

DROP TABLE agent_reflexes;

ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds agent_reflexes with the pre-dispatch_to_agent 5-value CHECK.
-- Structure-only, matching 105's precedent -- any dispatch_to_agent rows
-- inserted after the Up migration would violate this narrower constraint
-- on re-insert; a genuine downgrade scenario is expected to have none
-- (this is new functionality, not a widen-then-narrow round trip over
-- pre-existing data).

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_reflexes_new (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT REFERENCES agent_profiles(id),
    class_tag       TEXT,
    name            TEXT NOT NULL,
    trigger_kind    TEXT NOT NULL CHECK (
        trigger_kind IN ('predicate','event','interval')
    ),
    trigger_spec    TEXT NOT NULL,
    action_kind     TEXT NOT NULL CHECK (
        action_kind IN ('inject_reminder','force_tool_choice','send_message','halt_session','add_schedule')
    ),
    action_spec     TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    priority        INTEGER NOT NULL DEFAULT 0,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL,
    opt_out_allowed BOOLEAN NOT NULL DEFAULT TRUE
);

INSERT INTO agent_reflexes_new
    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
     action_kind, action_spec, status, priority, fired_count,
     last_fired_at, created_at, created_by, opt_out_allowed)
SELECT
    id, agent_id, class_tag, name, trigger_kind, trigger_spec,
    action_kind, action_spec, status, priority, fired_count,
    last_fired_at, created_at, created_by, opt_out_allowed
FROM agent_reflexes;

DROP TABLE agent_reflexes;

ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

END;

PRAGMA foreign_keys = ON;
