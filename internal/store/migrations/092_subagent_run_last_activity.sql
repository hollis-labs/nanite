-- CW-20260816-0004 — reaper activity-reset inactivity timer + hard ceiling.
--
-- Adds subagent_runs.last_activity_at: stamped by emitHeartbeat on every
-- live "still running" tick (internal/subagent/service.go) so the reaper
-- can distinguish a genuinely silent run from one that keeps making
-- progress. Empty string (the default) means "no heartbeat observed yet
-- for this run" — the reaper's inactivity comparison falls back to
-- started_at via COALESCE(NULLIF(last_activity_at, ''), started_at).
--
-- Plain ADD COLUMN (no CHECK constraint involved), so no table-rewrite
-- dance is needed here — see migrations 065/067 for the rewrite pattern
-- this deliberately avoids.

ALTER TABLE subagent_runs ADD COLUMN last_activity_at TEXT NOT NULL DEFAULT '';
