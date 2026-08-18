-- +goose Up
-- Phase 3 S3a (2026-04-15): user-configurable context-window + summarizer settings.
--
-- The slot-based context pipeline (internal/context) sizes the total budget
-- as ContextWindowTokens * BudgetPct. Compaction Stage 2 calls a summarizer
-- LLM. By default it uses the active chat provider/model, but the user
-- can pin it (e.g., a cheap utility model) by setting summarizer_provider
-- and summarizer_model.
--
-- compaction_strategy is a forward hook for the broker-driven dynamic
-- ordering (D9). The default 3-stage pipeline is the only implementation
-- today. The "broker" value is reserved.
ALTER TABLE user_settings ADD COLUMN context_window_tokens INTEGER NOT NULL DEFAULT 200000;
ALTER TABLE user_settings ADD COLUMN context_budget_pct REAL NOT NULL DEFAULT 0.80;
ALTER TABLE user_settings ADD COLUMN summarizer_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN summarizer_model TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN compaction_strategy TEXT NOT NULL DEFAULT 'default';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
