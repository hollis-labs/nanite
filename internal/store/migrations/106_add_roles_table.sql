-- Phase 1 item 01 (TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md):
-- `roles` table -- the one genuinely new facet in the Phase 1 schema.
--
-- Per architecture/01-agent-construction.md: a Role is the reusable
-- persona/behavior template an Agent composition is built from (name,
-- system_prompt, optional default-hint columns for tools/skills/permissions
-- that a new composition can start from). Tools/skills/permissions still
-- bind at the composition (`agent_profiles`) level, not fixed by role --
-- the default_* columns below are seed hints for a new composition to
-- start from, not an authoritative live grant. Mirrors the JSON-array/
-- JSON-object shape of agent_profiles.tools/role_tools/role_skills/
-- tool_permissions (see 001_schema.sql:32-60, 070_agent_profiles_role_tools.sql,
-- 076_agent_profiles_role_skills.sql) so a future backfill/seed flow has a
-- one-to-one column mapping.
--
-- default_class mirrors agent_profiles.class (migration 074): TEXT, no
-- CHECK constraint (Go-layer validated against advisor/process/template/
-- harness when non-empty, matching agent_profiles' own validation
-- approach -- see validateAgentMultiAgentFields in internal/store/agents.go).
-- Empty string means "this role supplies no class default" -- distinct
-- from agent_profiles.class, which always defaults to 'advisor' once a
-- composition exists.
--
-- `agent_profiles.role_id` (the FK that will bind a composition to one of
-- these rows) is added by 02-add-agents-composition-columns.md, not here --
-- that task's own Depends-on note is explicit that it needs this table to
-- exist first as its FK target. This migration only creates the target
-- table plus the CRUD surface (internal/store/roles.go) and the cascade
-- resolver's role-layer logic (internal/service/role_cascade.go) --
-- deliberately no FK column added to agent_profiles yet.
--
-- roles is DB-authoritative from creation -- no file/YAML source is ever
-- re-parsed into it on boot (per the "database is the source of truth"
-- principle and 08-kill-file-reingest-on-boot-pattern.md's scope). The
-- unrelated ~/.nanite/roles/ developer-persona Claude-Code-boot convention
-- (see GLOSSARY.md's Agent entry) is not read by, and does not feed, this
-- table.

-- +goose Up
CREATE TABLE IF NOT EXISTS roles (
    id                  TEXT PRIMARY KEY,
    slug                TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    system_prompt       TEXT NOT NULL DEFAULT '',
    default_class       TEXT NOT NULL DEFAULT '',
    default_model       TEXT NOT NULL DEFAULT '',
    default_provider    TEXT NOT NULL DEFAULT '',
    default_tools       TEXT NOT NULL DEFAULT '[]',
    default_skills      TEXT NOT NULL DEFAULT '[]',
    default_permissions TEXT NOT NULL DEFAULT '{}',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS roles;
