-- migrate:skip-if-column-exists subagent_runs last_activity_at
--
-- CW-20260817: see migration 019's identical header for the full
-- explanation. This migration also recreates subagent_runs from its own
-- (2026-era) column set — once a database already has 092's
-- last_activity_at column, this rebuild is fully superseded and skipped.
--
-- CW-20260519-0074 — subagent run status taxonomy.
--
-- Widens the subagent_runs.status CHECK constraint to admit the new
-- diagnostic terminal states introduced by the run-outcome classifier.
--
-- NOTE on comment style: splitSQL splits the migration on the ASCII
-- semicolon, so no comment line below may contain one — a stray
-- semicolon in prose would be parsed as a statement boundary.
--
--   over_budget  the wall-clock backstop fired but the run was making
--                progress (non-zero tool calls). Not a failure, so it
--                must not burn a retry budget or fire on_fail (mirrors
--                Torque's canceled-vs-failed split).
--   stalled      the provider-stream inactivity watchdog fired with no
--                progress, meaning the run went silent.
--
-- `failed` is now reserved for genuine crashes and the fabrication
-- detector trip. It no longer absorbs every non-success outcome.
--
-- SQLite cannot ALTER a CHECK constraint, so subagent_runs is recreated
-- (same pattern as migration 019). The column set below is migration
-- 019's subagent_runs_new plus the `provider` column added by 025 — the
-- current live schema. No existing rows carry the new statuses, so the
-- INSERT - SELECT copy is value-preserving.
--
-- PRAGMA foreign_keys sits OUTSIDE the BEGIN/END block (SQLite makes the
-- pragma a no-op inside an open transaction). END is SQLite's alias for
-- COMMIT and keeps splitSQL's BEGIN/END depth counter balanced. No table
-- FK-references subagent_runs, but the guard preempts the class of bug
-- caught in CW-20260417-0477. CREATE TABLE IF NOT EXISTS + IF NOT EXISTS
-- indexes make the block idempotent on re-run.

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
        CHECK(status IN ('requested','approved','running','completed',
                         'failed','cancelled','rejected',
                         'over_budget','stalled')),
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
    rejection_reason TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT ''
);

INSERT INTO subagent_runs_new
    (id, parent_session_id, child_session_id, role, prompt, mode, status,
     inputs_json, result_json, error, timeout_seconds,
     created_at, started_at, completed_at,
     parent_agent_id, envelope_instance_id, approved_at, approved_by,
     rejected_at, rejection_reason, provider)
SELECT
    id, parent_session_id, child_session_id, role, prompt, mode, status,
    inputs_json, result_json, error, timeout_seconds,
    created_at, started_at, completed_at,
    parent_agent_id, envelope_instance_id, approved_at, approved_by,
    rejected_at, rejection_reason, provider
FROM subagent_runs;

DROP TABLE subagent_runs;
ALTER TABLE subagent_runs_new RENAME TO subagent_runs;

CREATE INDEX IF NOT EXISTS idx_subagent_runs_parent  ON subagent_runs(parent_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_subagent_runs_status  ON subagent_runs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_subagent_runs_pending ON subagent_runs(status, created_at)
    WHERE status = 'requested';

END;

PRAGMA foreign_keys = ON;
