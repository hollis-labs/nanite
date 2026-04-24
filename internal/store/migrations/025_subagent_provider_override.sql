-- Phase B — SpawnRequest.Provider override (CW-20260424-0004).
-- Adds a provider column to subagent_runs so the per-spawn provider
-- override is persisted and visible via Status / inspection.
-- Empty string means "used agent profile default" (backward-compatible).
-- Idempotent: runner swallows duplicate-column errors on re-run.
ALTER TABLE subagent_runs ADD COLUMN provider TEXT NOT NULL DEFAULT '';
