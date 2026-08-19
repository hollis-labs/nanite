-- +goose Up
-- 054_recovery_breadcrumbs.sql
-- Persists in-process subagent recovery broker postmortem records.
-- One row per broker-handled failure observation (lib-level OnRestart
-- and terminal OnSessionExit pipeline passes). Used for postmortem
-- queries: which classes recover, which escalate, which remediations
-- succeed, attempt-counts per session.
--
-- See internal/runtime/agent/recovery for the broker that writes here.

CREATE TABLE IF NOT EXISTS nanite_recovery_breadcrumbs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp     TEXT NOT NULL,                  -- RFC3339Nano UTC
    session_id    TEXT NOT NULL,
    class         TEXT NOT NULL,                  -- transient / config_permissions / permanent / unknown
    cause         TEXT NOT NULL DEFAULT '',       -- agentsessions ExitError.Cause verbatim, or empty
    remediation   TEXT NOT NULL,                  -- repopulate_sandbox / refresh_mcp_transport / refresh_credentials / regenerate_claudemd / none
    action        TEXT NOT NULL,                  -- retry_transient / retry_config_fixed / permanent_failure / unknown
    outcome       TEXT NOT NULL,                  -- remediated / transient_retry_succeeded / permanent / cancelled / unknown
    attempt_count INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    reason        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_nanite_recovery_breadcrumbs_session
    ON nanite_recovery_breadcrumbs(session_id, timestamp);

CREATE INDEX IF NOT EXISTS idx_nanite_recovery_breadcrumbs_outcome
    ON nanite_recovery_breadcrumbs(outcome, timestamp);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
