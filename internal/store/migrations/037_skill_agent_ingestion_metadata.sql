-- J7 (CW-20260421-0011): skills/agents DB ingestion metadata columns.
-- Adds source, imported_at, origin_system, format, version, prompt to skills.
-- Adds imported_at, origin_system, format to agent_profiles.
-- (source and version already exist from 001_schema.sql)
-- Migration runner swallows duplicate-column errors for ADD COLUMN.
-- This file re-runs on every boot safely.
-- IMPORTANT: no semicolons inside comments (splitSQL naive-split limitation).

ALTER TABLE skills
  ADD COLUMN source TEXT NOT NULL DEFAULT 'builtin';

ALTER TABLE skills
  ADD COLUMN prompt TEXT NOT NULL DEFAULT '';

ALTER TABLE skills
  ADD COLUMN imported_at TEXT NOT NULL DEFAULT '';

ALTER TABLE skills
  ADD COLUMN origin_system TEXT NOT NULL DEFAULT '';

ALTER TABLE skills
  ADD COLUMN format TEXT NOT NULL DEFAULT 'markdown';

ALTER TABLE skills
  ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE agent_profiles
  ADD COLUMN imported_at TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_profiles
  ADD COLUMN origin_system TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_profiles
  ADD COLUMN format TEXT NOT NULL DEFAULT 'markdown';
