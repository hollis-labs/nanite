-- +goose Up
-- +goose NO TRANSACTION
-- TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md.
--
-- Adds the taxonomy lookup tables docs/engineering/architecture/
-- 10-reflex-action-taxonomy.md's "Facet 1" through "Facet 4" describe as
-- architecture-level agreement (operator-signed-off 2026-08-19, see
-- TASKS/phase-4/10-reflex-architecture-review.md's Work Log for the
-- decision trail) -- this migration is the first implementation step, not
-- a re-derivation of that design.
--
--   1. reflex_action_kinds.category (Facet 1) -- system_message (advisory,
--      the LLM keeps discretion) vs execute_action (deterministic, the
--      harness performs the effect directly) -- modeled as an extensible
--      lookup table (reflex_action_categories), not a hardcoded two-value
--      CHECK, so a third category can be added later without a schema
--      rewrite.
--   2. reflex_action_kinds.combining_algorithm (Facet 2) -- deny_overrides /
--      first_applicable / all_applicable, one row per existing action kind,
--      reclassified per the design doc's table. Seeded here; consumed by
--      the shared decision engine TASKS/reflex-taxonomy/
--      03-shared-decision-engine.md builds (not this task).
--   3. reflex_provenance_tiers + agent_reflexes.provenance_tier (Facet 3) --
--      a real FK-backed authority tier (system/operator/plugin) replacing
--      the free-text created_by string as the thing a future per-kind
--      allow-list gates against. agent_proposed is deliberately not a
--      fourth tier here: store.ApprovePendingReflex (internal/store/
--      agent_reflexes.go) already collapses an approved pending reflex's
--      created_by to "operator:"+reviewedBy on approval, so "active in
--      agent_reflexes => operator-approved" is already true by
--      construction -- formalizing provenance just needs to preserve that
--      collapse, not invent new behavior.
--   4. agent_reflexes.recurrence_override_seconds (Facet 4) -- the
--      per-reflex end of the system-default -> per-kind -> per-reflex
--      cascade. NULL means "inherit the kind-level default"
--      (reflex_action_kinds.default_recurrence_seconds, itself NULL =
--      "inherit the system default", a Go constant defined by
--      TASKS/reflex-taxonomy/02-recurrence-cascade.md, not a DB row).
--
-- Explicit call, per this task's own instruction (not a mandate -- either
-- option was acceptable): agent_reflexes.action_kind stays a plain TEXT
-- column, not an integer FK, because dozens of Go call sites (executor.go's
-- switch reflex.ActionKind, chat_reflexes.go's formatReflexReminder, etc.)
-- string-compare it directly against the store.ReflexAction* constants --
-- rewriting the column type would ripple through every one of them for no
-- behavioral gain. This migration does add a `REFERENCES
-- reflex_action_kinds(name)` clause to action_kind's existing CHECK-
-- constrained TEXT definition, though: real SQLite-enforced referential
-- integrity, string-keyed (not the column's storage type), free to add
-- during this same table-rebuild, and it materially helps once the seed
-- rows are the enforced source of truth for "what action kinds exist" —
-- see this file's own Work Log entry in TASKS/reflex-taxonomy/
-- 01-taxonomy-schema-foundation.md for the full reasoning.
--
-- agent_reflexes itself must be rebuilt (SQLite cannot ALTER a CHECK
-- constraint or add a real FK to an existing column in place) -- same
-- rename-recreate-copy pattern as 119_agent_reflex_dispatch_to_agent.sql
-- and 115_agent_reflex_opt_out.sql's precedent before it. The rebuild's
-- INSERT...SELECT step also performs this migration's one real data
-- change: backfilling provenance_tier from each row's existing created_by
-- value. Confirmed conventions as of this migration's authoring (see
-- internal/agent/reflexes/seeds.go:617, loom_pilot_seeds.go:229,
-- internal/api/reflexes.go:108, and store.ApprovePendingReflex's
-- "operator:"+reviewedBy collapse): created_by = 'system' is the only
-- convention that should map to provenance_tier = 'system'; every other
-- value in use today (a bare 'operator', or an approved pending reflex's
-- 'operator:<reviewer>') maps to 'operator'. No existing row backfills to
-- 'plugin' -- no live plugin-authored reflex path exists yet (design doc:
-- "no concrete plugin need exists today").

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS reflex_action_categories (
    name TEXT PRIMARY KEY
);

INSERT INTO reflex_action_categories (name) VALUES
    ('system_message'),
    ('execute_action');

CREATE TABLE IF NOT EXISTS reflex_action_kinds (
    name                         TEXT PRIMARY KEY,
    category                     TEXT NOT NULL REFERENCES reflex_action_categories(name),
    combining_algorithm          TEXT NOT NULL CHECK (
        combining_algorithm IN ('deny_overrides','first_applicable','all_applicable')
    ),
    default_recurrence_seconds   INTEGER
);

INSERT INTO reflex_action_kinds (name, category, combining_algorithm, default_recurrence_seconds) VALUES
    ('inject_reminder',   'system_message', 'all_applicable',  NULL),
    ('force_tool_choice', 'system_message', 'first_applicable', NULL),
    ('send_message',      'execute_action', 'all_applicable',  NULL),
    ('add_schedule',      'execute_action', 'all_applicable',  NULL),
    ('halt_session',      'execute_action', 'deny_overrides',  NULL),
    ('dispatch_to_agent', 'execute_action', 'first_applicable', 0);

CREATE TABLE IF NOT EXISTS reflex_provenance_tiers (
    name TEXT PRIMARY KEY
);

INSERT INTO reflex_provenance_tiers (name) VALUES
    ('system'),
    ('operator'),
    ('plugin');

CREATE TABLE IF NOT EXISTS agent_reflexes_new (
    id                          TEXT PRIMARY KEY,
    agent_id                    TEXT REFERENCES agent_profiles(id),
    class_tag                   TEXT,
    name                        TEXT NOT NULL,
    trigger_kind                TEXT NOT NULL CHECK (
        trigger_kind IN ('predicate','event','interval')
    ),
    trigger_spec                TEXT NOT NULL,
    action_kind                 TEXT NOT NULL REFERENCES reflex_action_kinds(name) CHECK (
        action_kind IN ('inject_reminder','force_tool_choice','send_message','halt_session','add_schedule','dispatch_to_agent')
    ),
    action_spec                 TEXT NOT NULL,
    status                      TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    priority                    INTEGER NOT NULL DEFAULT 0,
    fired_count                 INTEGER NOT NULL DEFAULT 0,
    last_fired_at               TEXT,
    created_at                  TEXT NOT NULL DEFAULT (datetime('now')),
    created_by                  TEXT NOT NULL,
    opt_out_allowed             BOOLEAN NOT NULL DEFAULT TRUE,
    provenance_tier             TEXT NOT NULL DEFAULT 'operator' REFERENCES reflex_provenance_tiers(name),
    recurrence_override_seconds INTEGER
);

INSERT INTO agent_reflexes_new
    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
     action_kind, action_spec, status, priority, fired_count,
     last_fired_at, created_at, created_by, opt_out_allowed,
     provenance_tier, recurrence_override_seconds)
SELECT
    id, agent_id, class_tag, name, trigger_kind, trigger_spec,
    action_kind, action_spec, status, priority, fired_count,
    last_fired_at, created_at, created_by, opt_out_allowed,
    CASE WHEN created_by = 'system' THEN 'system' ELSE 'operator' END,
    NULL
FROM agent_reflexes;

DROP TABLE agent_reflexes;

ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Drops the three new lookup tables and rebuilds agent_reflexes back to
-- 119_agent_reflex_dispatch_to_agent.sql's shape (six-value CHECK, no FK
-- against reflex_action_kinds, no provenance_tier/recurrence_override_seconds
-- columns). Lossy, structure-only, same precedent as 115's Down for
-- opt_out_allowed: provenance_tier/recurrence_override_seconds had no
-- pre-migration equivalent to restore, so there is nothing to preserve by
-- reconstructing them -- a genuine downgrade is expected to have no rows
-- depending on data this Down discards.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS agent_reflexes_new (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT REFERENCES agent_profiles(id),
    class_tag       TEXT,
    name            TEXT NOT NULL,
    trigger_kind    TEXT NOT NULL CHECK (
        trigger_kind IN ('predicate','event','interval')
    ),
    trigger_spec    TEXT NOT NULL,
    action_kind     TEXT NOT NULL CHECK (
        action_kind IN ('inject_reminder','force_tool_choice','send_message','halt_session','add_schedule','dispatch_to_agent')
    ),
    action_spec     TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    priority        INTEGER NOT NULL DEFAULT 0,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL,
    opt_out_allowed BOOLEAN NOT NULL DEFAULT TRUE
);

INSERT INTO agent_reflexes_new
    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
     action_kind, action_spec, status, priority, fired_count,
     last_fired_at, created_at, created_by, opt_out_allowed)
SELECT
    id, agent_id, class_tag, name, trigger_kind, trigger_spec,
    action_kind, action_spec, status, priority, fired_count,
    last_fired_at, created_at, created_by, opt_out_allowed
FROM agent_reflexes;

DROP TABLE agent_reflexes;

ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

END;

PRAGMA foreign_keys = ON;

DROP TABLE IF EXISTS reflex_provenance_tiers;
DROP TABLE IF EXISTS reflex_action_kinds;
DROP TABLE IF EXISTS reflex_action_categories;
