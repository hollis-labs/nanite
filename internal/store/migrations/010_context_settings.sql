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
