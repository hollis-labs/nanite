-- Phase 3 S4b (2026-04-16): MCP server trust-tier + environment allowlist.
--
-- Closes audit findings 07 (no trust-boundary validation) and 10 (env-var
-- inheritance). Adds two columns to mcp_servers:
--
-- * trust_tier: classifies the server's blast radius. Validators in
--   internal/mcp/validate.go pick per-tier limits (max tool count, schema
--   size, result size, etc.) keyed off this value.
--   Allowed: 'builtin' | 'plugin_stdio' | 'plugin_http' | 'third_party_http'.
--
-- * env_allowlist: JSON array of env-var names that the stdio transport may
--   inherit from the host process when spawning a subprocess. Default '[]'
--   (no inheritance) — closes finding 10.
--
-- D4 (fail-closed): every row in mcp_servers is by definition a third-party
-- (user-added or catalog-added) server — built-in and plugin-registered
-- servers are wired through the runtime Manager, not persisted here. So
-- the third_party_http default is correct for all existing rows.
ALTER TABLE mcp_servers ADD COLUMN trust_tier TEXT NOT NULL DEFAULT 'third_party_http';
ALTER TABLE mcp_servers ADD COLUMN env_allowlist TEXT NOT NULL DEFAULT '[]';

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
