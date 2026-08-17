-- migrate:skip-if-column-exists subagent_runs last_activity_at
--
-- CW-20260817: see migration 019's identical header for the full
-- explanation. This migration also recreates subagent_runs from its own
-- (2026-era) column set — once a database already has 092's
-- last_activity_at column, this rebuild is fully superseded and skipped.
--
-- CW-20260519-0075 — subagent run checkpoint/resume + retry lifecycle (audit §P6).
--
-- Adds the lifecycle columns the loop-in-execute retry/resume model needs:
--
--   retry_count    INTEGER  number of additional attempts past the first
--                            0 means "single attempt, no retry happened"
--   max_retries    INTEGER  per-run cap on retries. Mirrors Torque's
--                            MaxRetries (default 3, internal/service/task.go:274
--                            in Torque) — when retry_count == max_retries the
--                            chain terminates regardless of on_fail policy
--   on_fail        TEXT     routing policy on a genuine failure. Mirrors
--                            Torque's lifecycle.handleFailed routing
--                            (lifecycle.go:189-279). retry → loop within
--                            budget. block → terminate, leave row failed for
--                            the parent task to handle. escalate → same as
--                            block at the subagent layer
--                            (escalation belongs to the parent-task
--                            scheduler, not the inline subagent runner)
--   attempts_json  TEXT     JSON array recording per-attempt status, error,
--                            result_json, started_at, completed_at. The
--                            row's top-level status/error/result_json
--                            columns reflect the FINAL attempt. attempts_json
--                            is the audit trail of the prior attempts. Empty
--                            array = "row reflects the only attempt"
--
-- Why columns, not a new subagent_run_attempts table:
--   - The sync dispatch caller (internal/service/dispatch_wiring.go) blocks
--     on a single run ID and reads the final terminal status. Splitting
--     attempts across rows would require chain traversal at the dispatch
--     boundary for no observable benefit — the caller still wants one
--     row's outcome
--   - subagent_runs is already a "lifecycle row" table (status changes
--     in-place across requested → approved → running → terminal). Adding
--     retry_count + attempts_json fits that shape
--   - The reaper sweeps by started_at + timeout_seconds vs now (reaper.go
--     line 262 area). It must continue to find the single canonical row
--
-- Schema rewrite (CHECK on on_fail) follows the same pattern as 019/065 —
-- SQLite cannot ALTER a CHECK constraint, so the table is recreated.
--
-- NOTE on comment style: splitSQL splits on the ASCII semicolon, so no
-- comment line below may contain one — a stray semicolon in prose would
-- be parsed as a statement boundary.

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
    provider TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    on_fail TEXT NOT NULL DEFAULT 'retry'
        CHECK(on_fail IN ('retry','block','escalate')),
    attempts_json TEXT NOT NULL DEFAULT '[]'
);

INSERT INTO subagent_runs_new
    (id, parent_session_id, child_session_id, role, prompt, mode, status,
     inputs_json, result_json, error, timeout_seconds,
     created_at, started_at, completed_at,
     parent_agent_id, envelope_instance_id, approved_at, approved_by,
     rejected_at, rejection_reason, provider,
     retry_count, max_retries, on_fail, attempts_json)
SELECT
    id, parent_session_id, child_session_id, role, prompt, mode, status,
    inputs_json, result_json, error, timeout_seconds,
    created_at, started_at, completed_at,
    parent_agent_id, envelope_instance_id, approved_at, approved_by,
    rejected_at, rejection_reason, provider,
    0, 3, 'retry', '[]'
FROM subagent_runs;

DROP TABLE subagent_runs;
ALTER TABLE subagent_runs_new RENAME TO subagent_runs;

CREATE INDEX IF NOT EXISTS idx_subagent_runs_parent  ON subagent_runs(parent_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_subagent_runs_status  ON subagent_runs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_subagent_runs_pending ON subagent_runs(status, created_at)
    WHERE status = 'requested';

END;

PRAGMA foreign_keys = ON;
