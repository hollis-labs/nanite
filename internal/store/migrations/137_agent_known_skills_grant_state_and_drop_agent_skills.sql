-- +goose Up
-- TASKS/skills/02-redesign-skills-index-schema-and-extend-agent-known-skills.md:
--
-- 1. Extends agent_known_skills (069_per_agent_state.sql) with the
--    grant-state columns docs/engineering/architecture/20-skills.md's
--    "Security, sandboxing, and trust" section needs: approved_content_hash
--    (the hash a human approved access against -- a changed source requires
--    a new explicit install before it affects anything, and that new
--    install carries a new hash requiring its own approval), granted_at,
--    granted_by (operator/system identifier), capabilities_granted (JSON --
--    what the granted skill's script/materializer is authorized for; shape
--    coordinates with task 09, kept loose/JSON here since that task owns
--    the real vocabulary). All four columns are nullable/defaulted additions
--    only -- no rename, no drop, no PK change -- so the live Agent Builder
--    Wizard / Agent Capabilities Panel frontend (internal/api/
--    agent_capabilities.go's known-skills REST handlers) and any
--    pre-existing row keep working unmodified.
--
-- 2. Drops agent_skills outright. Confirmed zero rows workspace-wide (this
--    task's own Work Log records an independent re-verification against
--    both a real backup copy and the live production DB, not just trust in
--    113_agent_skills_agent_projects_fk.sql's own header comment). No data
--    migration needed. agent_known_skills is the sole per-agent skill
--    attachment/grant table going forward (docs/engineering/architecture/
--    13-memory-and-knowledge-tools.md §4a's "genuine catalog+attachment
--    split... the reference pattern") -- internal/store/skills.go's
--    ListAgentSkills/AssignSkillToAgent/RemoveSkillFromAgent are rewired in
--    this same task to read/write agent_known_skills (keyed by the skill's
--    slug) instead, so the live call sites that depended on agent_skills
--    (chat's buildSkillListForSession, the Wizard's capability-assignment
--    step, internal/plugin/agent_profiles.go's declarative skill grants)
--    keep working against the new table transparently.

ALTER TABLE agent_known_skills ADD COLUMN approved_content_hash TEXT;
ALTER TABLE agent_known_skills ADD COLUMN granted_at TEXT;
ALTER TABLE agent_known_skills ADD COLUMN granted_by TEXT;
ALTER TABLE agent_known_skills ADD COLUMN capabilities_granted TEXT;

DROP TABLE IF EXISTS agent_skills;

-- +goose Down
-- A real, working Down is required here (unlike most pre-cutover migrations
-- in this file set) because this repo has standing goose-DownTo-based
-- round-trip tests for several *older* migrations (102/103/105/112/115/124)
-- that walk all the way down past this migration and back up again as part
-- of exercising their own Down/Up cycle -- confirmed empirically during this
-- task: a no-op Down here breaks 113_agent_skills_agent_projects_fk.sql's own
-- Down section (it reads FROM agent_skills to rebuild it to the pre-113
-- shape; that fails with "no such table: agent_skills" once this migration's
-- Up has already dropped it) for every one of those tests' DownTo walks, and
-- a non-idempotent Up (plain ALTER TABLE ADD COLUMN, no DROP COLUMN
-- counterpart) breaks their subsequent replay back Up ("duplicate column
-- name") once this file is the new head.
--
-- Recreates agent_skills exactly as 113_agent_skills_agent_projects_fk.sql's
-- own Up left it (both FKs, same PK) -- empty, matching reality (this table
-- was confirmed zero rows before Up dropped it) -- so that migration 113's
-- own Down section can still run against it during a DownTo walk that
-- passes through both. Drops the four grant-state columns from
-- agent_known_skills (SQLite's ALTER TABLE ... DROP COLUMN, already used by
-- this same migration set's 134_agent_profiles_protocol_transport.sql).

ALTER TABLE agent_known_skills DROP COLUMN capabilities_granted;
ALTER TABLE agent_known_skills DROP COLUMN granted_by;
ALTER TABLE agent_known_skills DROP COLUMN granted_at;
ALTER TABLE agent_known_skills DROP COLUMN approved_content_hash;

CREATE TABLE IF NOT EXISTS agent_skills (
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    config TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (agent_id, skill_id)
);
