-- CW-20260420-0006 — Tool enrichment records (P1 ToolSurface primitive)

CREATE TABLE IF NOT EXISTS tool_enrichments (
    tool_name   TEXT NOT NULL PRIMARY KEY,
    hints_json  TEXT NOT NULL DEFAULT '{}',
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_enrichments_updated ON tool_enrichments(updated_at DESC);

-- Seed: one enrichment for end-to-end validation. clockwork_task_list is the canonical
-- list tool the chat loop hits most, and these hints come from CW-20260419-0024 thread-E pre-work.
INSERT OR IGNORE INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (
    'clockwork_task_list',
    '{"preconditions":["Call with explicit filters (project_id/status/priority) rather than filtering rows in memory after the fact"],"anti_patterns":["IDs returned are CW-YYYYMMDD-NNNN — do NOT pattern-match a different shape (TASK-1, T-123) and retry"],"chains_with":["clockwork_task_get","clockwork_task_transition"],"output_shape":"Paginated array of brief task records — full records via clockwork_task_get"}',
    '2026-04-20T00:00:00Z'
);
