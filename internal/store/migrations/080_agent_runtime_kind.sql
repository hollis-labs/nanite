-- +goose Up
-- 079_agent_runtime_kind.sql
-- Phase 0 runtime-kind stabilization.
--
-- Keep provider IDs and legacy aliases untouched, but persist the normalized
-- execution substrate so callers can inspect runtime state without decoding
-- overloaded provider strings.

ALTER TABLE agent_runtime
    ADD COLUMN runtime_kind TEXT NOT NULL DEFAULT 'unknown';

UPDATE agent_runtime
   SET runtime_kind = CASE
       WHEN provider IN ('claude', 'claude-code', 'claudecode', 'pty', 'pty-claude', 'sub-claude')
           THEN 'streaming-stdio'
       WHEN provider IN ('codex', 'pty-codex', 'sub-codex', 'opencode', 'pty-opencode', 'sub-opencode')
           THEN 'subprocess'
       ELSE 'unknown'
   END
 WHERE runtime_kind = '' OR runtime_kind = 'unknown';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
