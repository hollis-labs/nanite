-- G-4 subagent approval envelope.
--
-- Adds: 'interactive' mode, 'rejected' status, parent_agent_id persistence,
-- approval audit columns, user-settings flags. SQLite cannot ALTER CHECK
-- constraints, so subagent_runs is recreated. The partial
-- idx_subagent_runs_pending index supports the lazy stale-scan.
--
-- PRAGMA foreign_keys must sit OUTSIDE the BEGIN/COMMIT block: SQLite makes
-- the pragma a no-op inside an open transaction
-- (https://sqlite.org/pragma.html#pragma_foreign_keys). The migration runner
-- pins every statement to a single *sql.Conn so the PRAGMA set here carries
-- into the transaction that follows. No table currently FK-references
-- subagent_runs, but fixing now preempts the class of bug caught in
-- CW-20260417-0477 on migration 008.
--
-- CREATE TABLE IF NOT EXISTS and IF NOT EXISTS indexes make the block
-- idempotent on re-run.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS subagent_runs_new (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT NOT NULL,
    child_session_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'sync'
        CHECK(mode IN ('sync','async','api','interactive')),
    status TEXT NOT NULL DEFAULT 'requested'
        CHECK(status IN ('requested','approved','running','completed','failed','cancelled','rejected')),
    inputs_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    parent_agent_id TEXT NOT NULL DEFAULT '',
    envelope_instance_id TEXT NOT NULL DEFAULT '',
    approved_at TEXT NOT NULL DEFAULT '',
    approved_by TEXT NOT NULL DEFAULT '',
    rejected_at TEXT NOT NULL DEFAULT '',
    rejection_reason TEXT NOT NULL DEFAULT ''
);

INSERT INTO subagent_runs_new
    (id, parent_session_id, child_session_id, role, prompt, mode, status,
     inputs_json, result_json, error, timeout_seconds,
     created_at, started_at, completed_at,
     parent_agent_id, envelope_instance_id, approved_at, approved_by,
     rejected_at, rejection_reason)
SELECT
    id, parent_session_id, child_session_id, role, prompt, mode, status,
    inputs_json, result_json, error, timeout_seconds,
    created_at, started_at, completed_at,
    '', '', '', '', '', ''
FROM subagent_runs;

DROP TABLE subagent_runs;
ALTER TABLE subagent_runs_new RENAME TO subagent_runs;

CREATE INDEX IF NOT EXISTS idx_subagent_runs_parent  ON subagent_runs(parent_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_subagent_runs_status  ON subagent_runs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_subagent_runs_pending ON subagent_runs(status, created_at)
    WHERE status = 'requested';

COMMIT;

PRAGMA foreign_keys = ON;

-- UserSettings additions (idempotent: runner skips duplicate column errors).
ALTER TABLE user_settings ADD COLUMN subagent_approval_required INTEGER NOT NULL DEFAULT 1;
ALTER TABLE user_settings ADD COLUMN subagent_approval_timeout_seconds INTEGER NOT NULL DEFAULT 86400;
