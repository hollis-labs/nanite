-- TASKS/loops/03-loop-runs-schema.md.
--
-- Adds the `loop_runs` table -- the storage half of docs/engineering/
-- architecture/21-loops.md's Decision 1: LoopRun is a new PEER entity to
-- WorkflowRun, not a WorkflowRun itself (the opposite of Teams' TeamRun-IS-a-
-- WorkflowRun collapse). A Loop's defining property -- the plan can change
-- shape between iterations (REPLAN/REARCHITECT), and iterations can run
-- genuinely different executors -- is exactly what agentworkflow's DAG-only
-- scope deliberately excludes. loop_runs therefore gets its own
-- `id TEXT PRIMARY KEY`, never keyed by or aliased to workflow_runs.id.
-- LoopRun orchestrates a *sequence* of ordinary WorkflowRuns (task 04's
-- loop_run_iterations records each one) rather than owning a second
-- execution engine.
--
-- goal_id NOT NULL REFERENCES goals(id) -- "a loop_runs row always has
-- exactly one goal_id" (21-loops.md's Decision 2), landed by migration 135
-- (TASKS/loops/01-goals-schema.md).
--
-- status CHECK values (running|completed|failed|cancelled|waiting_on_gate|
-- waiting_on_escalation) match 21-loops.md's illustrative schema exactly.
-- Note this is deliberately narrower than agent_schedules' on_fail vocabulary
-- (retry/disable/notify) -- see budget_json's own comment below for
-- on_exhausted, the analogous but distinct knob this table actually uses.
--
-- budget_json holds { max_iterations, max_failures, max_runtime_seconds,
-- max_no_progress_iterations, on_exhausted } -- on_exhausted (enum:
-- "escalate"|"fail", default "escalate" per this planning session's own
-- decision, task file's Context section) is encoded INSIDE budget_json
-- rather than as a separate column: it's one policy knob among several
-- already JSON-blobbed together (max_iterations/max_failures/max_runtime/
-- max_no_progress_iterations), and splitting just this one field out into
-- its own column while leaving the rest blobbed would be an arbitrary,
-- undocumented asymmetry. internal/store/loop_runs.go's Budget struct +
-- validateBudget enforce the on_exhausted enum and non-negative-int
-- invariants at the Go layer (no DB-level CHECK on JSON contents, matching
-- goals.go's desired_state_json/constraints_json/etc. precedent -- SQLite
-- has no JSON-schema CHECK primitive).
--
-- continuation_policy_json is left an untyped JSON-blob placeholder here --
-- task 07 defines and consumes its real shape without this table needing to
-- be revisited, the same "don't guess a shape another task owns" discipline
-- TASKS/teams/01 applied to authority_json/routing_json.
--
-- One active LoopRun per goal_id at a time is an application-level
-- invariant (task 10's launcher, via this task's ListLoopRuns), not a DB
-- constraint -- a partial unique index on SQLite would need
-- WHERE status IN ('running','waiting_on_gate','waiting_on_escalation'),
-- valid SQLite but brittle against future status additions; a Go-layer
-- check is simpler. See this task's Context section for the full reasoning.
--
-- Brand-new table, nothing to rebuild -- plain transactional CREATE TABLE,
-- matching 128_teams.sql's and 135_goals.sql's own precedent (not the
-- NO TRANSACTION / PRAGMA foreign_keys rebuild dance 124/127/130/133/136
-- needed only to widen a CHECK on an already-populated table).

-- +goose Up
CREATE TABLE IF NOT EXISTS loop_runs (
    id                       TEXT PRIMARY KEY,
    goal_id                  TEXT NOT NULL REFERENCES goals(id),
    definition_name          TEXT NOT NULL,
    status                   TEXT NOT NULL DEFAULT 'running'
                             CHECK (status IN ('running','completed','failed','cancelled',
                                    'waiting_on_gate','waiting_on_escalation')),
    current_iteration        INTEGER NOT NULL DEFAULT 0,
    budget_json              TEXT NOT NULL DEFAULT '{}',
    continuation_policy_json TEXT NOT NULL DEFAULT '{}',
    no_progress_streak       INTEGER NOT NULL DEFAULT 0,
    started_at               TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at             TEXT
);

CREATE INDEX IF NOT EXISTS idx_loop_runs_goal ON loop_runs(goal_id, status);
CREATE INDEX IF NOT EXISTS idx_loop_runs_status ON loop_runs(status);

-- +goose Down
DROP TABLE IF EXISTS loop_runs;
