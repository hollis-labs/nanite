-- CW-20260420-0006 — Tool enrichment records (P1 ToolSurface primitive).
-- Schema only. Enrichment records are populated at runtime by admin tooling
-- or the probe agent (CW-20260419-0024) — never seeded by this migration.
-- External app tool names (MCP-provided or plugin-owned) are dynamic and
-- must not appear in nanite schema migrations.

CREATE TABLE IF NOT EXISTS tool_enrichments (
    tool_name   TEXT NOT NULL PRIMARY KEY,
    hints_json  TEXT NOT NULL DEFAULT '{}',
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_enrichments_updated ON tool_enrichments(updated_at DESC);
