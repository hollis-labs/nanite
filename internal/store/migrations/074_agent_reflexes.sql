-- FU-30 Phase 3 Stage 3.c — reflex system substrate.
--
-- A reflex is an automatic action that fires when a predicate over the
-- agent's recent state evaluates true. Reflexes absorb the FU-21 drift
-- detector (echo / cache-miss conjunctions) and extend the pattern to
-- general predicate AND/OR over windowed signals plus event and interval
-- triggers. The monitor-loop driver calls driftguard.Engine.Evaluate per
-- tick before delivering the tick to the agent.
--
-- agent_reflexes: live registered reflexes.
--   agent_id IS NULL means a class-bound base reflex (seeded at startup
--   per class). agent-specific overrides bind to a single agent_id.
--   trigger_spec / action_spec are JSON blobs interpreted by the
--   evaluator and executor in internal/agent/driftguard.
--
-- pending_reflexes: proposed-by-agent reflexes awaiting operator review.
--   The agent_reflex_propose self-tool writes here. The operator review
--   API approves into agent_reflexes or rejects with a reason.
--
-- The migration runner has no schema_migrations ledger and splits on the
-- semicolon character including inside comments, so this file avoids
-- semicolons in comment prose and wraps the CREATE TABLE statements in
-- one BEGIN/END block for atomicity. CREATE TABLE IF NOT EXISTS makes
-- the block idempotent across re-boots.

BEGIN;

CREATE TABLE IF NOT EXISTS agent_reflexes (
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
    created_by      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

CREATE TABLE IF NOT EXISTS pending_reflexes (
    id              TEXT PRIMARY KEY,
    proposed_by     TEXT NOT NULL,
    proposed_at     TEXT NOT NULL DEFAULT (datetime('now')),
    target_agent_id TEXT REFERENCES agent_profiles(id),
    name            TEXT NOT NULL,
    trigger_kind    TEXT NOT NULL,
    trigger_spec    TEXT NOT NULL,
    action_kind     TEXT NOT NULL,
    action_spec     TEXT NOT NULL,
    rationale       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending','approved','rejected')
    ),
    reviewed_at     TEXT,
    reviewed_by     TEXT
);

CREATE INDEX IF NOT EXISTS idx_pending_reflexes_status
    ON pending_reflexes(status, proposed_at);

END;
