-- TASKS/teams/01-team-definition-schema.md.
--
-- Adds the `teams` table -- the storage half of docs/engineering/
-- architecture/15-teams.md's "Team" concept: a saved, named, reusable
-- organizational shape (Team Slots, authority, routing, phase/gate
-- sequence). Nothing in the design doc's own text names this table
-- explicitly (its "one real new table" claim is about team_run_members, a
-- *run's* resolved members -- TASKS/teams/02-team-run-members-table.md) --
-- this is a planning-session addition filling a gap the design doc's own
-- scope assumes exists but never assigns. See that task file's Context for
-- the full citation trail.
--
-- Four JSON sub-structure columns, one row per saved Team definition:
--   - slots_json      -- []TeamSlotDefinition (internal/store/teams.go).
--                         Team Slot, always spelled out -- see this task's
--                         GLOSSARY.md entries and internal/context/slot.go's
--                         unrelated, already-load-bearing SlotOrder concept
--                         (Context Broker prompt-assembly slots). Not the
--                         same "slot."
--   - authority_json  -- storage placeholder only. TASKS/teams/
--                         04-team-authority-schema.md owns the real
--                         may_spawn/may_message/may_not_review shape (and
--                         may migrate this column into its own normalized
--                         table -- coordinate, don't duplicate).
--   - routing_json    -- storage placeholder only. TASKS/teams/
--                         09-team-routing.md owns the real routing-rule
--                         shape.
--   - phases_json     -- the phase/gate sequence (15-teams.md's
--                         "Illustrative shape": scope_work (flex) ->
--                         review_gate (gate) -> address_feedback (flex) ->
--                         merge_gate (gate)) that TASKS/teams/
--                         07-team-compiler.md compiles into a
--                         WorkflowDefinition at TeamRun launch. Kept as its
--                         own column, never commingled into slots_json, per
--                         15-teams.md's "What this session did not decide"
--                         forward-compat instruction: so a later Team/
--                         Workflow definition split is "accept a phase
--                         sequence from a second source," not a rewrite of
--                         the compiler.
--
-- All four are stored as plain TEXT (JSON-encoded strings), matching this
-- codebase's existing convention for JSON-blob columns (roles.default_tools/
-- default_skills/default_permissions, agent_schedules.job_payload,
-- selftool_reactions.config) rather than a distinct json.RawMessage Go
-- type -- one of the two options this task file's own "What to do" section
-- explicitly offered ("Leave authority_json/routing_json/phases_json as
-- json.RawMessage or minimally-typed placeholders"). internal/store/
-- teams.go's Team.AuthorityJSON/RoutingJSON/PhasesJSON are plain Go
-- `string` fields for this reason -- consistent scan/insert handling with
-- every other JSON-blob column in this package, no behavior difference for
-- tasks 04/07/09 which still just get/set the raw JSON text.
--
-- Brand-new table, nothing to rebuild -- plain transactional CREATE TABLE,
-- matching 126_selftool_reactions.sql's precedent rather than 124/127's
-- NO TRANSACTION / PRAGMA foreign_keys rename-recreate-copy rebuild (only
-- needed there because an existing table with rows had to be preserved).

-- +goose Up
CREATE TABLE IF NOT EXISTS teams (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT,
    slots_json      TEXT NOT NULL DEFAULT '[]',
    authority_json  TEXT NOT NULL DEFAULT '[]',
    routing_json    TEXT NOT NULL DEFAULT '[]',
    phases_json     TEXT NOT NULL DEFAULT '[]',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_teams_name ON teams(name);

-- +goose Down
DROP TABLE IF EXISTS teams;
