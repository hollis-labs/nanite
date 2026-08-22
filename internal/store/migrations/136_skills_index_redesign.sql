-- +goose Up
-- +goose NO TRANSACTION
-- TASKS/skills/02-redesign-skills-index-schema-and-extend-agent-known-skills.md:
-- docs/engineering/architecture/20-skills.md's "The model: DB is an index,
-- a vendored store is content" section -- the skills table becomes an
-- index-only row (vendored-store addressing, version, source tier,
-- enablement, declared dependencies), never SKILL.md body text, script
-- contents, or asset bytes.
--
-- Drop-and-recreate is safe here: TASKS/skills/01 already cut every writer
-- into this table (mcp.Manager.AutoDiscover's per-tool rows, the file-based
-- builtin-skill seed) and this task's own planning session re-verified --
-- independently of that prior migration's own comments -- that no other
-- production data exists in this table at this point in the batch (see this
-- task's Work Log for the direct backup-copy row-count check performed
-- before this migration was written). No carried-forward content, per
-- 20-skills.md's "Migration: clean slate" section.
--
-- agent_skills.skill_id carries `REFERENCES skills(id) ON DELETE CASCADE`
-- (migration 113) -- agent_skills itself is dropped by the next migration
-- (137), but it still exists as of this migration, so PRAGMA foreign_keys
-- is toggled off around the drop/recreate the same way 113's own
-- rename-recreate-copy pattern does, rather than relying on migration
-- ordering to avoid the FK.

PRAGMA foreign_keys = OFF;

BEGIN;

DROP TABLE IF EXISTS skills;

CREATE TABLE skills (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    slug                  TEXT NOT NULL UNIQUE,
    description           TEXT NOT NULL DEFAULT '',
    category              TEXT NOT NULL DEFAULT '',
    icon                  TEXT,
    input_schema          TEXT NOT NULL DEFAULT '{}',
    -- Source tier taxonomy (builtin/user/project/plugin/claude-ecosystem,
    -- or whatever task 04's real package parser settles on) is owned by
    -- that task -- left as a free-form string here rather than a CHECK
    -- constraint pre-committing to an enum ahead of that parser landing.
    source_tier           TEXT NOT NULL DEFAULT 'user',
    -- Addressing key into the content-addressed vendored store
    -- (internal/skillvendor, TASKS/skills/03), e.g. "skl-vendor-<hash>".
    -- NULL until an install/sync (task 04/05) actually vendors a package.
    content_hash          TEXT,
    version               INTEGER NOT NULL DEFAULT 1,
    enabled               INTEGER NOT NULL DEFAULT 1,
    -- JSON array of skill slugs -- the install-time dependency graph task
    -- 07's cycle detection walks.
    declared_dependencies TEXT NOT NULL DEFAULT '[]',
    installed_at          TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- No down migration -- matches this codebase's standing pre-cutover
-- convention (see 069_per_agent_state.sql's Down section): the table this
-- reconstructs held zero rows of any value at migration time, and a
-- hand-derived rollback to the pre-redesign shape has no operational value.
