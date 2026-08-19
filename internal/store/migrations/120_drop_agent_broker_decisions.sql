-- +goose Up
-- TASKS/phase-4/08-export-and-drop-agent-broker-decisions.md: `agent_broker_
-- decisions` (migration 058) was the Agent Broker's own decision/telemetry
-- log. Its writer (Store.InsertAgentBrokerDecision, called from
-- persistAgentBrokerDecision/attemptBrokerDispatch in the now-deleted
-- internal/service/chat_broker_dispatch.go) was retired in full by
-- TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-
-- migration.md, which replaces the Agent Broker with the dispatch_to_agent
-- reflex action kind (internal/service/chat_reflex_dispatch.go). This is
-- the same disposal TASKS/phase-0/23-export-and-drop-decision-tables.md
-- already gave the sibling tables `broker_decisions`/`strategy_decisions`
-- (migration 111) — deferred here only because this table's writer wasn't
-- retired until Phase 4. See TASKS/ESCALATIONS.md's "Item 23" entry and
-- TASKS/phase-0/23's own Work Log for the full history.
--
-- MANDATORY pre-condition: `nanite admin export-decision-tables` must be
-- run against this database BEFORE this migration is applied to it, so
-- every historical row lands in `event_log` (event_type
-- "agent_broker_decision_export", full original row JSON-encoded in
-- metadata) first. That command deliberately does not go through
-- store.New() (which applies every pending goose migration, including this
-- one, unconditionally) — it opens the database file directly so the
-- export can run independently of whether this migration has already
-- landed in the binary. Deploying a build that contains this migration to
-- a database that hasn't been exported yet loses the un-exported rows
-- permanently — DROP TABLE is not recoverable.
--
-- The Down side recreates the table's final pre-drop shape (structure
-- only, per 110_drop_prompt_templates.sql's / 111's precedent — the
-- exported event_log rows are the durable copy of the data, not this
-- Down), including both indexes from migration 058.

DROP TABLE IF EXISTS agent_broker_decisions;

-- +goose Down

CREATE TABLE IF NOT EXISTS agent_broker_decisions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    user_input_hash TEXT NOT NULL,
    mode_signal TEXT NOT NULL DEFAULT '',
    scope_tier TEXT NOT NULL DEFAULT '',
    reflex_id TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_agent_broker_decisions_session
    ON agent_broker_decisions(session_id, id DESC);

CREATE INDEX IF NOT EXISTS idx_agent_broker_decisions_created_at
    ON agent_broker_decisions(created_at DESC);
