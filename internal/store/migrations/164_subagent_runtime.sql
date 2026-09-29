-- +goose Up
-- 164_subagent_runtime.sql
--
-- D-38 (CW-20260929-0010): which runtime a subagent spawned from an
-- API-driven parent executes on. 'api' runs it through Nanite's own chat
-- harness; 'cli' boots a CLI process, whose self-tool calls bypass that
-- harness.
--
-- user_settings.subagent_runtime is the app default; '' means unset and is
-- read as 'api'. sessions.subagent_runtime is the per-session override:
-- NULL falls back to the app default. Both are ADD COLUMN only -- a column
-- no running process selects is invisible to it.
--
-- Interim home: this migrates onto D-18 assignment.limits / launch.overrides
-- once agent-contracts-leaf lands.

ALTER TABLE user_settings ADD COLUMN subagent_runtime TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN subagent_runtime TEXT;

-- +goose Down
ALTER TABLE sessions DROP COLUMN subagent_runtime;
ALTER TABLE user_settings DROP COLUMN subagent_runtime;
