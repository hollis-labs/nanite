-- Phase 3 S3b (2026-04-16): Tools-slot cache-and-pointer settings.
--
-- Adds per-user configuration for the intent-triggered tool hydration
-- pipeline (cache, classifier mode, provider/model, timeout) plus the
-- T9 flag that toggles synchronous compaction+retry when a provider
-- returns a context-overflow error mid-generation (folded from
-- BLG-20260410-003).
--
-- Defaults preserve S3a semantics when tool_cache_enabled = 0: callers
-- should fall back to "always hydrate full defs" in that case. The
-- default here (enabled=1) flips to pointer-by-default.

ALTER TABLE user_settings ADD COLUMN tool_cache_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE user_settings ADD COLUMN tool_classifier_mode TEXT NOT NULL DEFAULT 'broker';
ALTER TABLE user_settings ADD COLUMN tool_classifier_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN tool_classifier_model TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN tool_classifier_timeout_ms INTEGER NOT NULL DEFAULT 500;
ALTER TABLE user_settings ADD COLUMN context_overflow_recovery INTEGER NOT NULL DEFAULT 1;
