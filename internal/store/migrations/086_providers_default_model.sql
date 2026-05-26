-- 086_providers_default_model.sql
-- CW-20260526-0003: kill the Go-literal default-model fallback chain.
--
-- Adds providers.default_model so runtime can resolve a chat model via
-- user_settings.default_model then providers.default_model per-provider,
-- without a Go constant in the chain. Operators change defaults via the
-- DB without a recompile.
--
-- Also one-time-cleans up rows that carry the legacy bare alias
-- "claude-sonnet-4" (Anthropic rejects it with 404 -- it leaked in via
-- internal/service/durable_agents.go:252 and migration 084 before the
-- resolver landed). Empty string means "use the system default" -- the
-- resolver fills it in at request time.
--
-- Idempotent: ADD COLUMN is gated by the migration runner's duplicate-
-- column error swallow. UPDATEs are predicate-scoped to the bad value.
--
-- splitSQL is semicolon-based, so this file deliberately avoids
-- semicolons inside comments.

ALTER TABLE providers ADD COLUMN default_model TEXT NOT NULL DEFAULT '';

UPDATE durable_agent_instances SET model = '' WHERE model = 'claude-sonnet-4';

UPDATE sessions SET model = '' WHERE model = 'claude-sonnet-4';
