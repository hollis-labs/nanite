-- Phase B (CW-20260424-0001): per-spawn provider override.
--
-- Adds a `provider` column to subagent_runs so the caller-supplied
-- provider override is persisted alongside the run for audit and
-- status inspection. Empty string means the agent profile default
-- was used (matches the zero-value / pre-migration rows).
--
-- Idempotent: the migration runner swallows "duplicate column" errors.
ALTER TABLE subagent_runs ADD COLUMN provider TEXT NOT NULL DEFAULT '';
