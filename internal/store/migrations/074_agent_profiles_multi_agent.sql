-- +goose Up
-- +goose NO TRANSACTION
-- 073_agent_profiles_multi_agent.sql
-- FU-28 CW-20260520-0043 Phase 3 Stage 3.a multi-agent foundation.
--
-- Adds FU-28 columns to agent_profiles (urn, urn_aliases, activation_mode,
-- class, default_state) and expands the sessions status CHECK to include
-- the three new lifecycle states (sleeping, halted, terminated).
--
-- SQLite ALTER TABLE ADD COLUMN with a CHECK constraint is awkward to
-- make idempotent after the column already exists -- the runner swallows
-- duplicate-column failures but not check-constraint mismatches that
-- would only fire on a row that violates the new check. The cleanest
-- spike-scope trade-off is to skip per-column CHECK constraints in DDL
-- and enforce activation_mode/class/default_state at the Go layer
-- instead (see internal/store/agents.go validation). All three columns
-- are plain TEXT NOT NULL with deterministic defaults.
--
-- The runner splits statements on the semicolon character including
-- inside comments per the 066 finding, so this file uses none in any
-- comment line. Plain ALTER statements without a transaction wrapper
-- avoid leaving a transaction open if the duplicate-column branch fires
-- on a re-boot.

ALTER TABLE agent_profiles ADD COLUMN urn TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_profiles ADD COLUMN urn_aliases TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agent_profiles ADD COLUMN activation_mode TEXT NOT NULL DEFAULT 'singleton';
ALTER TABLE agent_profiles ADD COLUMN class TEXT NOT NULL DEFAULT 'advisor';
ALTER TABLE agent_profiles ADD COLUMN default_state TEXT NOT NULL DEFAULT 'sleeping';
CREATE INDEX IF NOT EXISTS idx_agent_profiles_urn ON agent_profiles(urn);

-- Sessions table rebuild to expand the status CHECK list. The new list
-- adds sleeping, halted, terminated to the original three. SQLite
-- cannot ALTER a CHECK constraint in place so full recreate is the
-- only reliable path (pattern from migration 008). The migration
-- runner swallows ALTER TABLE RENAME TO failures on a re-run pass
-- (no-such-table for sessions_new on the second pass, etc.) so the
-- block stays idempotent without a guard column.
--
-- The full column shape mirrors the live database schema as of FU-21
-- (068_session_halt.sql) including context_prompt, current_mode_id,
-- auto_switch_override, intent CHECK, halted_at and halted_reason. Any
-- future column additions to sessions must be re-mirrored here or in a
-- later table-rebuild migration.
--
-- The PRAGMA foreign_keys = OFF must sit OUTSIDE the BEGIN/END block
-- per migration 008 because SQLite makes the pragma a no-op inside an
-- open transaction. The runner pins every statement to a single conn
-- so the PRAGMA set here carries into the transaction that follows.
-- We use END as the SQLite alias for COMMIT so splitSQL's BEGIN/END
-- depth counter correctly closes the transaction.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS sessions_new (
    id TEXT PRIMARY KEY,
    short_code TEXT NOT NULL UNIQUE,
    title TEXT,
    custom_name TEXT,
    workspace_id TEXT REFERENCES workspaces(id),
    project_id TEXT REFERENCES projects(id),
    context_type TEXT,
    context_id TEXT,
    provider TEXT,
    model TEXT,
    status TEXT DEFAULT 'active' CHECK(status IN ('active','paused','archived','sleeping','halted','terminated')),
    is_pinned BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0,
    message_count INTEGER DEFAULT 0,
    compaction_summary TEXT,
    compacted_at DATETIME,
    tags TEXT DEFAULT '[]',
    metadata TEXT DEFAULT '{}',
    last_activity DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    context_prompt TEXT NOT NULL DEFAULT '',
    current_mode_id TEXT REFERENCES modes(id),
    auto_switch_override INTEGER DEFAULT NULL,
    intent TEXT NULL CHECK (intent IS NULL OR intent IN ('long-running', 'per-turn', 'ephemeral')),
    halted_at DATETIME,
    halted_reason TEXT
);

INSERT INTO sessions_new
    SELECT id, short_code, title, custom_name, workspace_id, project_id,
           context_type, context_id, provider, model, status,
           is_pinned, sort_order, message_count,
           compaction_summary, compacted_at, tags, metadata,
           last_activity, created_at, updated_at,
           context_prompt, current_mode_id, auto_switch_override,
           intent, halted_at, halted_reason
      FROM sessions;

DROP TABLE sessions;

ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_pinned ON sessions(is_pinned, sort_order);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
