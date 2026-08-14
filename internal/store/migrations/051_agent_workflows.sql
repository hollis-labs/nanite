-- 051_agent_workflows.sql
-- Agent Workflows built-in engine persistence (CW-20260813-0010).
--
-- workflow_runs: one row per builtin-engine Run()/Resume() invocation.
-- workflow_run_steps: one row per step within a run, keyed by
-- (workflow_run_id, step_id). This is the fix for Hadron's two gaps the
-- design doc calls out: not crash-durable (a restarted process reloads
-- this table and resumes from the last completed step) and no typed
-- inter-step state (a later step's template references read a prior
-- step's `output` column directly, not log-line scraping).
--
-- Status transitions on workflow_run_steps.status:
--   pending -> running -> completed|failed|waiting_on_gate
--   pending -> skipped   (a dependency failed or was itself skipped)
-- waiting_on_gate blocks that branch of the DAG until externally resolved
-- (out of scope for this ticket: the resolution trigger, see
-- CW-20260813-0014).
--
-- workflow_runs.status mirrors agentworkflow.RunStatus plus the engine's
-- own in-flight 'running' value (RunStatus only models terminal-for-this-
-- call states).
--
-- IMPORTANT: no semicolons inside comments -- splitSQL in
-- internal/store/store.go splits migration scripts on the SQL statement
-- terminator and does not parse comments.

CREATE TABLE IF NOT EXISTS workflow_runs (
    id TEXT PRIMARY KEY,
    definition_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','completed','failed','cancelled','waiting_on_gate')),
    input_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE TABLE IF NOT EXISTS workflow_run_steps (
    id TEXT PRIMARY KEY,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK(kind IN ('llm','tool','gate')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','running','completed','failed','waiting_on_gate','skipped')),
    output TEXT NOT NULL DEFAULT '',
    is_error INTEGER NOT NULL DEFAULT 0,
    tool_calls_json TEXT NOT NULL DEFAULT '[]',
    verify_json TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_steps_run_step
    ON workflow_run_steps(workflow_run_id, step_id);

CREATE INDEX IF NOT EXISTS idx_workflow_run_steps_status
    ON workflow_run_steps(workflow_run_id, status);
