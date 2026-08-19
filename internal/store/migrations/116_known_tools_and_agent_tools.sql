-- Phase 1 item 04 (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md):
-- known_tools -- global tool catalog, live-synced against builtins + current
-- MCP discovery at container startup (internal/service/known_tools_sync.go's
-- SyncKnownTools, called from container.go). agent_tools -- the FK-based
-- grant join that replaces agent_profiles.tools/tool_permissions/role_tools'
-- selection semantics, per architecture/01-agent-construction.md's
-- "Relational references replace free-text strings" section.
--
-- ## known_tools vs. agent_known_tools -- do not confuse (GLOSSARY.md-class
-- naming-collision risk, flagged explicitly in this task's own Context)
--
-- agent_known_tools (migration 069) is a live, currently-used PER-AGENT
-- roster/pinning table with real REST CRUD (internal/api/agent_capabilities.go)
-- and a live GUI (AgentCapabilitiesPanel.tsx) -- it tracks activation
-- telemetry (pinned, activation_count, ttl_seconds) for tools an agent has
-- already been exposed to. known_tools (this migration) is a different
-- concept: a GLOBAL catalog, one row per tool that exists in the system at
-- all, independent of any agent. Neither table is renamed, merged, or
-- superseded by the other. agent_tools (also this migration) is the new
-- join between agent_profiles and known_tools -- the real FK-based
-- replacement for the free-text tools:/toolPermissions:/roleTools: columns.
--
-- ## agent_dispatch_tool_allowlist -- deliberately NOT named
-- agent_dispatch_allowlist (naming-collision resolution, see this task's
-- Work Log for the full analysis)
--
-- architecture/01-agent-construction.md names a new "agent_dispatch_allowlist"
-- concept: "which tools this agent may authorize a subagent it dispatches to
-- use." agent_profiles.parent_dispatch_allowlist already exists (migration
-- 060_agent_parent_dispatch_allowlist.sql) and holds a JSON array of ROLE
-- SLUGS a parent agent may dispatch task_execute to -- verified by direct
-- read of that migration and its live consumer
-- (internal/service/tool.go's parseParentDispatchAllowlist /
-- describer.CallerAgent.DispatchAllowlist). These are two genuinely
-- different axes -- target roles vs. grantable tools -- that would collide
-- under near-identical names ("dispatch allowlist") if the new table were
-- literally called agent_dispatch_allowlist. Per this task's own instruction
-- to rename rather than ship two confusingly-similar "dispatch allowlist"
-- concepts, the new table here is agent_dispatch_tool_allowlist --
-- parent_dispatch_allowlist keeps its existing name and column, unchanged.
--
-- ## Shape
--
-- known_tools.status is 'available'/'unavailable' -- never deleted when a
-- server disconnects, per the architecture doc's explicit instruction, so a
-- disconnect doesn't orphan agent_tools/agent_dispatch_tool_allowlist grant
-- rows that reference it.
--
-- known_tools.concurrency_safe is a placeholder column for Phase 3 item 06
-- (TASKS/phase-3/06-tool-concurrency-safety-classification.md), which is
-- expected to populate it from declared tool metadata, replacing the current
-- name-heuristic in internal/service/tool.go's GetToolMeta. Nullable: NULL
-- means "not yet classified" (distinct from an explicit false), matching
-- that task's "not inferred from its name" framing -- this migration only
-- reserves the column, it does not populate or consume it.
--
-- known_tools.always_included is the tool-discovery escape hatch flag the
-- architecture doc calls out by name: "so a new agent can't accidentally
-- ship without ... request_tools/tool_list/tool_describe ... a flag on
-- known_tools ... not a per-creation-flow default that can be silently
-- dropped." Seeded true for those three tool names by this same migration
-- (INSERT OR IGNORE placeholder rows -- the real live-synced row for each
-- is upserted by SyncKnownTools at the next boot, which preserves
-- always_included via its ON CONFLICT clause not touching that column).
--
-- agent_tools / agent_dispatch_tool_allowlist are both plain (agent_id,
-- tool_id) composite-PK join tables, matching the established
-- agent_skills/agent_projects FK pattern (113_agent_skills_agent_projects_fk.sql)
-- -- real ON DELETE CASCADE FKs from the start, since these are brand-new
-- tables (no ALTER-table rebuild dance needed, unlike 113's retrofit).
--
-- agent_tools.granted_via is a free-text provenance tag mirroring
-- agent_known_tools.reason's established precedent (migration 069): 'explicit'
-- (future direct grant, e.g. task 09's picker UI), 'role_seed' (from
-- ingest.go's seedRoleToolsFromIngest, mirroring agent_known_tools'
-- reason='role_seed'), or 'legacy_backfill' (this task's one-time carry-over
-- of agent_profiles.tools/tool_permissions/role_tools' CURRENT values into
-- real grants, run once per agent -- see
-- internal/service/known_tools_backfill.go). Audit/provenance only -- see
-- agent_tools_legacy_backfill below for the actual one-time-per-agent guard.
--
-- agent_tools_legacy_backfill is a separate, minimal marker table: one row
-- per agent once BackfillAgentToolsFromLegacyColumns has considered it, so a
-- later boot never re-derives that agent's grants from the legacy columns
-- again -- regardless of how many grants (including zero, for a
-- deny-everything agent) resulted, and regardless of whether an operator
-- later revokes some or all of the resulting agent_tools rows directly.
-- Deliberately NOT the same row as the grant itself: an early version of
-- this migration tried to reuse agent_tools rows tagged
-- granted_via='legacy_backfill' as the guard, which breaks the moment an
-- operator revokes the only such row for an agent -- the guard disappears
-- along with it, and the next boot silently re-asserts the legacy value
-- over the operator's explicit revoke. That is exactly the "reingest
-- overwrites a GUI customization" anti-pattern
-- 01-agent-construction.md's "What's cut" section names as a real,
-- already-fixed bug for agent profile files generally; this table exists
-- specifically so agent_tools' backfill doesn't reintroduce the same class
-- of bug at the data layer.

-- +goose Up

CREATE TABLE IF NOT EXISTS known_tools (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    source           TEXT NOT NULL DEFAULT 'builtin',
    status           TEXT NOT NULL DEFAULT 'available',
    description      TEXT NOT NULL DEFAULT '',
    concurrency_safe BOOLEAN,
    always_included  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_known_tools_status ON known_tools(status);

-- Seed the three tool-discovery escape-hatch meta-tools as always_included
-- placeholder rows. SyncKnownTools (internal/service/known_tools_sync.go)
-- upserts the real row for each (description, source, status) on every
-- boot via ON CONFLICT(name) DO UPDATE, which deliberately does not touch
-- always_included -- so this seed is what actually establishes the flag;
-- the sync just keeps the rest of the row fresh. INSERT OR IGNORE: if a
-- row with this name already exists (e.g. a prior partial apply), leave it
-- untouched rather than clobbering an operator-adjusted always_included.
INSERT OR IGNORE INTO known_tools (id, name, source, status, description, always_included, created_at, updated_at)
VALUES
    ('seed-request_tools', 'request_tools', 'builtin', 'available', 'Progressive tool discovery meta-tool.', TRUE, datetime('now'), datetime('now')),
    ('seed-tool_list',     'tool_list',     'builtin', 'available', 'Lists available tools.',                  TRUE, datetime('now'), datetime('now')),
    ('seed-tool_describe', 'tool_describe', 'builtin', 'available', 'Describes a tool in detail.',             TRUE, datetime('now'), datetime('now'));

CREATE TABLE IF NOT EXISTS agent_tools (
    agent_id    TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    tool_id     TEXT NOT NULL REFERENCES known_tools(id) ON DELETE CASCADE,
    granted_via TEXT NOT NULL DEFAULT 'explicit',
    created_at  TEXT NOT NULL,
    PRIMARY KEY (agent_id, tool_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_tools_tool ON agent_tools(tool_id);

CREATE TABLE IF NOT EXISTS agent_dispatch_tool_allowlist (
    agent_id   TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    tool_id    TEXT NOT NULL REFERENCES known_tools(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (agent_id, tool_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_dispatch_tool_allowlist_tool ON agent_dispatch_tool_allowlist(tool_id);

CREATE TABLE IF NOT EXISTS agent_tools_legacy_backfill (
    agent_id   TEXT PRIMARY KEY REFERENCES agent_profiles(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS agent_tools_legacy_backfill;
DROP TABLE IF EXISTS agent_dispatch_tool_allowlist;
DROP TABLE IF EXISTS agent_tools;
DROP TABLE IF EXISTS known_tools;
