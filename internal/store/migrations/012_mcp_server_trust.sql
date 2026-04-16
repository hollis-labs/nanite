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
