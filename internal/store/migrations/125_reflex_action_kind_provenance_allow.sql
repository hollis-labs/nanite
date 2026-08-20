-- TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md.
--
-- Adds the per-action-kind provenance-tier declare allow-list Facet 3
-- (docs/engineering/architecture/10-reflex-action-taxonomy.md, "Facet 3 --
-- Provenance / authority tier") names but leaves un-DDL'd: "A per-kind
-- allow-list (which tiers may declare which kind) gates the two
-- highest-blast-radius kinds -- halt_session and dispatch_to_agent are the
-- obvious candidates to restrict away from plugin-tier, but the specific
-- allow-list values are not locked by [the architecture review] session;
-- the mechanism is."
--
-- reflex_action_kind_provenance_allow (kind_name, tier_name) -- presence of
-- a row means that provenance tier (reflex_provenance_tiers, migration
-- 124_reflex_action_taxonomy.sql) may declare an agent_reflexes row of
-- that action kind (reflex_action_kinds, same migration). Seeded here with
-- every one of the 6 kinds x 3 tiers = 18 combinations except
-- (halt_session, plugin) and (dispatch_to_agent, plugin) -- 16 allowed
-- rows, 2 denied by omission, per this task's own Done-means and the
-- architecture doc's two named "obvious candidates." This default is
-- explicitly adjustable, not locked security policy -- see
-- TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md's Work Log: a
-- future operator decision to loosen or tighten it is a plain
-- INSERT/DELETE against this table, not a code or migration change.
--
-- Enforcement of this allow-list at write time lives in Go
-- (internal/api/reflexes.go's validateReflexDefinition), not in a DB
-- trigger/CHECK -- the two blast-radius kinds this table restricts have no
-- live plugin-tier insert path today (design doc: "no concrete plugin
-- need exists today"), so there is nothing for a DB-level constraint to
-- guard beyond what the Go-side gate already covers, and a DB trigger
-- would duplicate that logic in a second place for no live caller.
--
-- No table rebuild needed here (unlike 124's agent_reflexes rebuild) --
-- this is a brand-new table with no existing rows to migrate, so a plain
-- transactional CREATE TABLE + INSERT is sufficient; no NO TRANSACTION /
-- PRAGMA foreign_keys toggling required.

-- +goose Up
CREATE TABLE IF NOT EXISTS reflex_action_kind_provenance_allow (
    kind_name TEXT NOT NULL REFERENCES reflex_action_kinds(name),
    tier_name TEXT NOT NULL REFERENCES reflex_provenance_tiers(name),
    PRIMARY KEY (kind_name, tier_name)
);

INSERT INTO reflex_action_kind_provenance_allow (kind_name, tier_name) VALUES
    ('inject_reminder',   'system'),
    ('inject_reminder',   'operator'),
    ('inject_reminder',   'plugin'),
    ('force_tool_choice', 'system'),
    ('force_tool_choice', 'operator'),
    ('force_tool_choice', 'plugin'),
    ('send_message',      'system'),
    ('send_message',      'operator'),
    ('send_message',      'plugin'),
    ('add_schedule',      'system'),
    ('add_schedule',      'operator'),
    ('add_schedule',      'plugin'),
    ('halt_session',      'system'),
    ('halt_session',      'operator'),
    -- ('halt_session', 'plugin') deliberately omitted.
    ('dispatch_to_agent', 'system'),
    ('dispatch_to_agent', 'operator');
    -- ('dispatch_to_agent', 'plugin') deliberately omitted.

-- +goose Down
DROP TABLE IF EXISTS reflex_action_kind_provenance_allow;
