-- +goose Up
-- TASKS/phase-0/23-export-and-drop-decision-tables.md: `strategy_decisions`
-- (the strategy planner's own decision log, migration 034; writer removed
-- by TASKS/phase-0/11-cut-strategy-planner.md) and `broker_decisions` (the
-- tool broker's own decision log, migrations 001 + 031; writer removed as
-- part of this same task — see internal/service/tool.go/internal/store/
-- broker.go git history for TASKS/phase-0/23) are both retired.
--
-- Does NOT touch `agent_broker_decisions` (migration 058). That table
-- belongs to a separate, still-live system (the Agent Broker,
-- github.com/hollis-labs/agentkit/broker, wired in
-- internal/service/chat_broker_dispatch.go) that is not retired by any
-- currently-scheduled Phase 0 task — see this task's own file (Context
-- section) and TASKS/ESCALATIONS.md's "Item 23" entry. It gets its own
-- export+drop once the Agent Broker is retired into reflexes (Phase 3).
--
-- MANDATORY pre-condition: `nanite admin export-decision-tables` must be
-- run against this database BEFORE this migration is applied to it, so
-- every historical row lands in `event_log` (event_type
-- "strategy_decision_export" / "broker_decision_export", full original row
-- JSON-encoded in metadata) first. That command deliberately does not go
-- through store.New() (which applies every pending goose migration,
-- including this one, unconditionally) — it opens the database file
-- directly so the export can run independently of whether this migration
-- has already landed in the binary. Deploying a build that contains this
-- migration to a database that hasn't been exported yet loses the
-- un-exported rows permanently — DROP TABLE is not recoverable.
--
-- IF NOT EXISTS on the Down side: neither table has ever had an ALTER
-- TABLE change its shape after creation (031 added columns to
-- broker_decisions once, before this migration; 034 created
-- strategy_decisions with its final shape directly) — the shapes below
-- are the true final pre-drop shapes.

DROP TABLE IF EXISTS broker_decisions;
DROP TABLE IF EXISTS strategy_decisions;

-- +goose Down
-- Recreates both tables with their final pre-drop shape (structure only,
-- per 110_drop_prompt_templates.sql's precedent — DROP TABLE is inherently
-- lossy either way; the exported event_log rows are the durable copy of
-- the data, not this Down).

CREATE TABLE IF NOT EXISTS broker_decisions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    intent TEXT,
    layer_reached TEXT,
    selected_tools TEXT,
    signals TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    consecutive_empty INTEGER NOT NULL DEFAULT 0,
    total_calls INTEGER NOT NULL DEFAULT 0,
    outcome TEXT NOT NULL DEFAULT 'selected',
    loaded_count INTEGER NOT NULL DEFAULT 0,
    reflection_query TEXT
);

CREATE INDEX IF NOT EXISTS idx_broker_decisions_session ON broker_decisions(session_id);
CREATE INDEX IF NOT EXISTS idx_broker_decisions_outcome ON broker_decisions(outcome);

CREATE TABLE IF NOT EXISTS strategy_decisions (
    id                         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id                 TEXT    NOT NULL,
    turn_id                    TEXT,
    approach                   TEXT    NOT NULL DEFAULT 'direct_chain',
    rationale                  TEXT    NOT NULL DEFAULT '',
    max_turns                  INTEGER NOT NULL DEFAULT 0,
    escalation_budget          INTEGER NOT NULL DEFAULT 0,
    reflex_match_id            TEXT,
    playbook_hit               TEXT,
    grounding_consultation_ids TEXT    NOT NULL DEFAULT '[]', -- JSON array
    created_at                 DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_strategy_decisions_session ON strategy_decisions(session_id);
CREATE INDEX IF NOT EXISTS idx_strategy_decisions_approach ON strategy_decisions(approach);
