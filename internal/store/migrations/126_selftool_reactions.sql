-- TASKS/harness-reactive-self-tools/02-reactive-layer-schema.md.
--
-- Adds the reactive-layer lookup + config tables docs/engineering/
-- architecture/11-harness-reactive-self-tools.md's "Definition & reaction
-- shape" section describes as architecture-level agreement -- this
-- migration is the first implementation step, not a re-derivation of that
-- design.
--
-- Reactive layer only -- this is not a migration of the self-tool catalog
-- itself. The ~70+ existing tool definitions (name/description/input
-- schema) and their dispatch stay exactly where TASKS/harness-reactive-
-- self-tools/01-move-self-tools-to-internal-selftools.md's internal/mcp ->
-- internal/selftools move puts them: static Go. Only self-tools that are
-- actually harness-reactive get a row in the tables below.
--
--   1. selftool_reaction_kinds (lookup) -- category/kind is a lookup table,
--      not a hardcoded enum, same extensibility reasoning as
--      reflex_action_kinds (TASKS/reflex-taxonomy/
--      01-taxonomy-schema-foundation.md). Keyed by `slug TEXT PRIMARY KEY`
--      (not a separate surrogate id + unique slug column) -- matches
--      reflex_action_kinds' own `name TEXT PRIMARY KEY` precedent exactly,
--      and there is no call site in this design that needs a surrogate key
--      for a 4-row lookup table. Seeded here with exactly the four rows
--      docs/engineering/architecture/11-harness-reactive-self-tools.md
--      names: render_card/internal_api_call (implemented=1, built by
--      03-reaction-engine-core.md/04-render-card-construction.md) and
--      external_api_call/callback (implemented=0 -- real, queryable,
--      documented rows before they're executable, deliberately not built
--      in this batch).
--   2. selftool_reactions (config, many rows per tool) -- id, tool_name
--      (plain string matching the Go-defined tool's Name -- no FK to a
--      tool-catalog table, since tool definitions stay in Go, not DB),
--      reaction_kind_id (FK to selftool_reaction_kinds.slug -- named
--      reaction_kind_id per the design doc's own naming despite storing a
--      slug value, not an integer id), config (JSON, kind-specific),
--      enabled, created_at. No rows seeded by this migration --
--      07-worked-example-task-update-report.md seeds the first two real
--      rows. Keyed by `id TEXT PRIMARY KEY`, populated via
--      uuid.New().String() at insert time (internal/store/
--      selftool_reactions.go's InsertSelftoolReaction) -- this codebase's
--      own insert-time-ID-generation convention (internal/store/
--      agents.go:421, internal/store/artifacts.go:144, etc.), not the
--      lookup tables' plain-string-PK convention: this is a real per-tool
--      config table with potentially many rows, not a small closed lookup
--      set.
--
-- Deliberately no combining-algorithm column on selftool_reactions.
-- Reflexes need one because multiple reflexes of the *same kind* can
-- independently fire on the same state and something has to pick a
-- winner. Self-tool reactions don't have that shape -- one tool call looks
-- up its own configured reactions, and firing multiple different kinds
-- together (a card, an internal API call, both) is the normal case, not a
-- conflict to resolve. This is a documented, deliberate omission, not a
-- gap to fill in later.
--
-- Both tables are brand new -- no existing table to rebuild, so a plain
-- transactional CREATE TABLE + INSERT is sufficient here, matching
-- 125_reflex_action_kind_provenance_allow.sql's own precedent (also a
-- brand-new table) rather than 124_reflex_action_taxonomy.sql's NO
-- TRANSACTION / PRAGMA foreign_keys rename-recreate-copy rebuild (needed
-- there only because agent_reflexes was an existing table with rows to
-- preserve).

-- +goose Up
CREATE TABLE IF NOT EXISTS selftool_reaction_kinds (
    slug        TEXT PRIMARY KEY,
    category    TEXT NOT NULL CHECK (category IN ('render', 'execute')),
    implemented INTEGER NOT NULL DEFAULT 0,
    description TEXT NOT NULL DEFAULT ''
);

INSERT INTO selftool_reaction_kinds (slug, category, implemented, description) VALUES
    ('render_card', 'render', 1,
     'Resolves the reaction''s config into an envelope payload; the calling self-tool handler embeds it as an <!--ENVELOPE_DATA:...--> marker in its ToolResult text. See 04-render-card-construction.md.'),
    ('internal_api_call', 'execute', 1,
     'Executes an HTTP call against a trusted, in-process/internal endpoint. See 03-reaction-engine-core.md.'),
    ('external_api_call', 'execute', 0,
     'Reserved for a future trust-gated call to an external endpoint. Not executable in this batch -- see this folder''s README, "What this batch does NOT do."'),
    ('callback', 'execute', 0,
     'Reserved for a future opaque-callback-target reaction. Not executable in this batch.');

CREATE TABLE IF NOT EXISTS selftool_reactions (
    id               TEXT PRIMARY KEY,
    tool_name        TEXT NOT NULL,
    reaction_kind_id TEXT NOT NULL REFERENCES selftool_reaction_kinds(slug),
    config           TEXT NOT NULL DEFAULT '{}',
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_selftool_reactions_tool_name
    ON selftool_reactions(tool_name, enabled);

-- +goose Down
DROP TABLE IF EXISTS selftool_reactions;
DROP TABLE IF EXISTS selftool_reaction_kinds;
