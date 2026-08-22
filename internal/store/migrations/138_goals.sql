-- TASKS/loops/01-goals-schema.md.
--
-- Adds the `goals` table -- the storage half of docs/engineering/
-- architecture/21-loops.md's Decision 2: Goal is a first-class, persisted
-- entity with its own lifecycle (draft -> defined -> active ->
-- blocked/satisfied/failed/cancelled/superseded), independent of any one
-- execution. A Goal can exist before a Loop launches against it and can
-- outlive a single LoopRun (survives an abandon-and-restart, survives a
-- REPLAN, potentially spans a Team's flex phases and a Loop's iterations
-- under one shared target state). See GLOSSARY.md's existing Goal entry
-- (already landed the same session this design doc was authored -- not
-- duplicated here).
--
-- A loop_runs row always has exactly one goal_id (loop_runs is a later
-- task in this batch, not added here) -- a goals row does not require a
-- loop_runs row. parent_goal_id supports 21-loops.md's §16 decomposition
-- (one goal recomputing its own subgoals as barriers are discovered); the
-- column exists here as a self-referencing FK, but no engine walks it yet
-- (see TASKS/loops' own batch README, "What this batch does NOT do").
--
-- Four JSON sub-structure columns (desired_state_json, constraints_json,
-- acceptance_criteria_json, invariants_json) hold the design doc's
-- illustrative `desired_state`/`constraints`/`acceptance_criteria`/
-- (invariants, implied by the same shape) lists -- internal/store/goals.go's
-- typed []string accessor pairs (DesiredState/SetDesiredState, etc.) work
-- with them. Stored as plain TEXT (JSON-encoded strings), matching this
-- codebase's existing convention for JSON-blob columns (migration 128's own
-- doc comment has the full citation trail for why `string`, not
-- `json.RawMessage`) -- not a distinct Go type, just consistent scan/insert
-- handling with every other JSON-blob column in this package.
--
-- status has both a DB-level CHECK (below) and a Go-layer validateGoalStatus
-- enum check (internal/store/goals.go), mirroring how agent_schedules'
-- schedule_kind/status columns are double-enforced. Real transition-legality
-- enforcement (e.g. rejecting draft -> satisfied directly) is a later task's
-- job once the loop engine is the thing driving transitions -- this
-- migration and its Go layer only enforce enum membership.
--
-- Brand-new table, nothing to rebuild -- plain transactional CREATE TABLE,
-- matching 128_teams.sql's own precedent (not the NO TRANSACTION / PRAGMA
-- foreign_keys rebuild dance 124/127 needed only to preserve existing rows
-- in an already-populated table).

-- +goose Up
CREATE TABLE IF NOT EXISTS goals (
    id                       TEXT PRIMARY KEY,
    parent_goal_id           TEXT REFERENCES goals(id),
    intent                   TEXT NOT NULL,
    desired_state_json       TEXT NOT NULL DEFAULT '[]',
    constraints_json         TEXT NOT NULL DEFAULT '[]',
    acceptance_criteria_json TEXT NOT NULL DEFAULT '[]',
    invariants_json          TEXT NOT NULL DEFAULT '[]',
    priority                 TEXT,
    scope                    TEXT,
    status                   TEXT NOT NULL DEFAULT 'draft'
                             CHECK (status IN ('draft','defined','active','blocked',
                                    'satisfied','failed','cancelled','superseded')),
    owner                    TEXT,
    source                   TEXT,
    created_at               TEXT NOT NULL DEFAULT (datetime('now')),
    activated_at             TEXT,
    completed_at             TEXT
);

CREATE INDEX IF NOT EXISTS idx_goals_parent ON goals(parent_goal_id);
CREATE INDEX IF NOT EXISTS idx_goals_status ON goals(status);

-- +goose Down
DROP TABLE IF EXISTS goals;
