-- +goose Up
-- TASKS/teams/05-agent-reflexes-run-scoping.md.
--
-- docs/engineering/architecture/15-teams.md's "Routing: real reuse, and
-- one real gap" section names this precisely: agent_reflexes.agent_id
-- today is only NULL (class-bound/global) or a specific agent_profiles.id
-- -- there is no third scoping dimension for "this reflex exists only for
-- the lifetime of this run, and its candidate set is this run's resolved
-- members." This migration adds that dimension: a nullable
-- agent_reflexes.workflow_run_id column. NULL (the value of every
-- pre-existing row, and every row inserted by any path that doesn't set
-- it explicitly) means global/class-bound, exactly as today -- this
-- codebase's and go-scheduler's existing convention for "unscoped." A
-- non-NULL value scopes the row to one specific workflow_runs.id (a
-- TeamRun, per the design doc's "TeamRun IS a WorkflowRun" decision) --
-- see internal/store/agent_reflexes.go's ListAgentReflexesForWorkflowRun
-- (new, this task) for the read-side query this column feeds, and
-- internal/service/chat_reflex_dispatch.go's attemptReflexDispatch /
-- internal/selftools/self_tools_dispatch.go's matchDispatchToAgentReflex
-- for the two call sites that combine it with the existing global set.
--
-- No table rebuild needed: a plain nullable ADD COLUMN with a REFERENCES
-- clause and no CHECK constraint is exactly the same shape as
-- 112_add_consumers_table.sql's agent_profiles.consumer_id and
-- 117_agent_profiles_composition_columns.sql's agent_profiles.role_id/
-- model_id -- both real, already-shipped precedents for a nullable FK
-- column added via plain ALTER TABLE ADD COLUMN against a table with
-- pre-existing rows, no PRAGMA foreign_keys toggling or rename-recreate-
-- copy dance required (unlike 119/124's CHECK-constraint-driven
-- agent_reflexes rebuilds). Confirmed directly against a real copy of a
-- production backup (~/.local/share/nanite/workspaces/default/backups/)
-- via both the sqlite3 CLI and this project's own goose-driven
-- store.migrate() path -- see this task's Work Log for the exact
-- verification steps.
--
-- Referencing workflow_runs(id) (051_agent_workflows.sql) rather than a
-- Teams-specific table: TeamRun IS a WorkflowRun (no separate run-id
-- namespace), and workflow_runs already exists on every database this
-- migration runs against -- team_run_members (TASKS/teams/
-- 02-team-run-members-table.md's own table) does not need to exist yet
-- for this FK to be valid; it constrains workflow_run_id, not any
-- Teams-specific join table.

ALTER TABLE agent_reflexes ADD COLUMN workflow_run_id TEXT REFERENCES workflow_runs(id);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_workflow_run
    ON agent_reflexes(workflow_run_id, status);

-- +goose Down
-- Structure-only, same precedent as 112/117's Downs for their own nullable
-- FK columns: no pre-migration equivalent existed to restore, and a
-- genuine downgrade is expected to have no rows depending on data this
-- Down discards (no code path in this task inserts a non-NULL
-- workflow_run_id -- that's later Teams work, not yet filed).

DROP INDEX IF EXISTS idx_agent_reflexes_workflow_run;

ALTER TABLE agent_reflexes DROP COLUMN workflow_run_id;
