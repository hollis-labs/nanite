-- +goose Up
-- +goose NO TRANSACTION
-- Phase 1 item 05 (TASKS/phase-1/05-fix-agent-skills-and-agent-projects-fks.md):
-- add a real, enforced `agent_id TEXT NOT NULL REFERENCES agent_profiles(id)
-- ON DELETE CASCADE` to both agent_skills and agent_projects. Architecture
-- doc 01-agent-construction.md calls out agent_skills explicitly ("same FK
-- pattern, against the real skills catalog") and agent_projects as "the
-- existing precedent" for Scope; decision log Section 4 states the same
-- intent generally ("tool/skill/model references become real foreign keys
-- against live catalogs instead of free-text strings").
--
-- Both tables were confirmed zero-row workspace-wide against a real
-- production DB backup (main.db.pre-execution-backup-20260818-132726,
-- WAL-checkpointed) immediately before this migration was written, so this
-- is a pure schema change -- no data to preserve or reconcile.
--
-- This task's own precondition-verification step (What to do #1) initially
-- FAILED: a grep of every write path into both tables found two real, live,
-- mounted HTTP routes that could insert an agent_id with no backing
-- agent_profiles row, which this FK would have started silently rejecting:
--
--   - POST /api/agents/{id}/skills (handleAssignAgentSkill,
--     internal/api/skills.go) checked existence via
--     a.Services.Agents.Get, which resolves file-based agents
--     ("file-<slug>" canonical IDs, internal/agent/convert.go) through the
--     in-memory fileDefs slice *before* touching the DB -- exactly the
--     documented AutoIngestAgents per-definition-failure race
--     (internal/service/ingest.go's own doc comment, CW-20260815-0009):
--     a file-discovered agent can be visible via file discovery with no
--     backing agent_profiles row at all.
--   - POST /api/agents/{id}/projects (handleAddAgentProject,
--     internal/api/agents.go) performed *no* agent-existence check
--     whatsoever before inserting.
--
-- Both call sites were fixed (this same change set) to check
-- a.Services.Store.GetAgent(agentID) directly -- a real agent_profiles row
-- lookup, not the file-def-resolving AgentService.Get -- and reject with
-- 404 when no row exists, before this FK was added. See
-- internal/api/skills_test.go (TestHandleAssignAgentSkill_RejectsNonexistentAgent,
-- TestHandleAssignAgentSkill_RejectsFileBasedGhostAgent) and
-- internal/api/agent_projects_test.go
-- (TestHandleAddAgentProject_RejectsNonexistentAgent) for regression pins.
--
-- SQLite cannot ALTER a table to add a FK/CHECK constraint in place, so
-- both tables are rebuilt via the rename-recreate-copy pattern established
-- by 074_agent_profiles_multi_agent.sql (also used most recently by
-- 105_drop_unused_session_status_values.sql). Neither table has any other
-- pending column drift versus 001_schema.sql (verified: no migration
-- between 001 and 105 touches agent_skills or agent_projects), so the
-- rebuilt shape below is identical to the original except for the new FK.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_skills_new (
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    config TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (agent_id, skill_id)
);

INSERT INTO agent_skills_new (agent_id, skill_id, config)
SELECT agent_id, skill_id, config FROM agent_skills;

DROP TABLE agent_skills;

ALTER TABLE agent_skills_new RENAME TO agent_skills;

END;

PRAGMA foreign_keys = ON;

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_projects_new (
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, project_id)
);

INSERT INTO agent_projects_new (agent_id, project_id, created_at)
SELECT agent_id, project_id, created_at FROM agent_projects;

DROP TABLE agent_projects;

ALTER TABLE agent_projects_new RENAME TO agent_projects;

CREATE INDEX IF NOT EXISTS idx_agent_projects_project ON agent_projects(project_id);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds both tables back to their pre-migration, FK-less-on-agent_id
-- shape exactly as 001_schema.sql originally defined them. Structure only --
-- both tables are zero-row in every known database, so no data is at risk
-- either direction, but the copy-through is written for real in case that
-- ever changes.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_skills_new (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    config TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (agent_id, skill_id)
);

INSERT INTO agent_skills_new (agent_id, skill_id, config)
SELECT agent_id, skill_id, config FROM agent_skills;

DROP TABLE agent_skills;

ALTER TABLE agent_skills_new RENAME TO agent_skills;

END;

PRAGMA foreign_keys = ON;

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_projects_new (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    project_id TEXT NOT NULL REFERENCES projects(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, project_id)
);

INSERT INTO agent_projects_new (agent_id, project_id, created_at)
SELECT agent_id, project_id, created_at FROM agent_projects;

DROP TABLE agent_projects;

ALTER TABLE agent_projects_new RENAME TO agent_projects;

CREATE INDEX IF NOT EXISTS idx_agent_projects_project ON agent_projects(project_id);

END;

PRAGMA foreign_keys = ON;
