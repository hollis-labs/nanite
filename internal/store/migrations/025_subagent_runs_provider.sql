-- Phase B (CW-20260424-0001) — per-spawn provider override.
--
-- Adds a `provider` column to subagent_runs so the requested LLM
-- provider override is persisted alongside the run for audit and
-- Status inspection. Empty string = "use agent profile default"
-- (the pre-migration behaviour).
--
-- ALTER TABLE ... ADD COLUMN is idempotent: the migration runner
-- swallows "duplicate column" errors (store.go:147), so re-running
-- on an already-migrated database is safe.

ALTER TABLE subagent_runs ADD COLUMN provider TEXT NOT NULL DEFAULT '';
