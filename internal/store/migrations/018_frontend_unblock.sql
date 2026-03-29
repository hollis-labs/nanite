-- 018: Frontend unblock batch — archive filtering, agent-project links, icon fields, seed→system rename.

-- Item 5: Agent ↔ Project many-to-many join table.
CREATE TABLE IF NOT EXISTS agent_projects (
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_projects_project ON agent_projects(project_id);

-- Item 6: Rename source "seed" → "system" on existing agent profiles.
UPDATE agent_profiles SET source = 'system' WHERE source = 'seed';

-- Item 7: Add icon column to agents, skills, prompt_templates, plugin_settings.
ALTER TABLE agent_profiles ADD COLUMN icon TEXT;
ALTER TABLE skills ADD COLUMN icon TEXT;
ALTER TABLE prompt_templates ADD COLUMN icon TEXT;
ALTER TABLE plugin_settings ADD COLUMN icon TEXT;

-- Index for archive filtering (item 3) and message search (item 2).
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_messages_content ON messages(session_id, created_at);
