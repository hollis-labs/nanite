-- Extend artifacts with three-tier origin system and source tracking.
ALTER TABLE artifacts ADD COLUMN origin TEXT NOT NULL DEFAULT 'uploaded';
ALTER TABLE artifacts ADD COLUMN source_tool_call_id TEXT;
ALTER TABLE artifacts ADD COLUMN source_agent_id TEXT;
ALTER TABLE artifacts ADD COLUMN source_plugin_id TEXT;
