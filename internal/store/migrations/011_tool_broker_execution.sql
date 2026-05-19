-- Phase 3 S4a (2026-04-15): tool broker execution-path validation.
--
-- Adds per-tool call cap + result cache settings to user_settings,
-- and the tool_result_cache table for the cache-and-pointer pattern
-- that replaces naïve truncation of large tool results.

-- UserSettings columns for execution-path tuning.
-- CW-20260519-0115 raised this default from 10 to 150 (high backstop).
-- Pattern-based runaway detection (consecutive_fail_cap, runaway_fail_cap,
-- detectStuckLoop, idle_timeout) does the catching now -- the count cap
-- is only an absolute "this tool is in a tight infinite loop" failsafe.
-- Existing databases get updated by the UPDATE in migration 066 (ADD
-- COLUMN is idempotent and skipped on re-run, so this default only
-- applies to fresh databases).
ALTER TABLE user_settings ADD COLUMN tool_per_turn_cap INTEGER NOT NULL DEFAULT 150;
ALTER TABLE user_settings ADD COLUMN tool_result_cache_ttl_seconds INTEGER NOT NULL DEFAULT 3600;
-- CW-20260419-0018 (UAT c17) lowered this default from 65536 to 2048 after
-- a 89 KiB clockwork_task_list result bypassed the cache-pointer gate and
-- blew the per-minute rate budget. Existing databases get updated by the
-- UPDATE in migration 021 (ADD COLUMN is idempotent and skipped on re-run,
-- so this default only applies to fresh databases).
ALTER TABLE user_settings ADD COLUMN tool_result_soft_truncate_bytes INTEGER NOT NULL DEFAULT 2048;
ALTER TABLE user_settings ADD COLUMN tool_result_hard_cap_bytes INTEGER NOT NULL DEFAULT 1048576;

-- Session-scoped cache for large tool results. The LLM sees a truncated
-- view + pointer. fetch_tool_result and search_tool_result meta-tools
-- retrieve slices from the cached body.
CREATE TABLE IF NOT EXISTS tool_result_cache (
    id TEXT PRIMARY KEY,                    -- ULID
    session_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,             -- LLM's tool_use_id
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    byte_size INTEGER NOT NULL,
    was_truncated INTEGER NOT NULL DEFAULT 0,
    body TEXT                               -- nullable (null when byte_size > hard cap)
);
CREATE INDEX IF NOT EXISTS idx_tool_result_cache_session ON tool_result_cache(session_id);
CREATE INDEX IF NOT EXISTS idx_tool_result_cache_expires ON tool_result_cache(expires_at);
