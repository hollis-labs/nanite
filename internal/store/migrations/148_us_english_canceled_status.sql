-- +goose Up
-- +goose NO TRANSACTION
-- 148_us_english_canceled_status.sql
-- Torque CW-20260824-0027 (US-English migration).
--
-- Operator decision, 2026-08-24: US English is the project standard, and the
-- persisted enum moves with it. Every status vocabulary in this schema that
-- spelled the terminal "the caller stopped this" state the British way now
-- spells it the American way: 'cancelled' -> 'canceled'. Pre-release with no
-- external wire consumers is the only cheap moment for an enum rename; after
-- launch it stops being cheap.
--
-- Two distinct kinds of work are bundled here, both required for the rename
-- to be real rather than cosmetic:
--
--   1. CHECK-constraint rebuilds, for the four tables that pin the vocabulary
--      in a CHECK: subagent_runs (068's shape), workflow_runs (144's shape),
--      goals (138's shape) and loop_runs (141's shape). SQLite cannot ALTER a
--      CHECK constraint in place, so each is rebuilt with the same
--      create-copy-drop-rename idiom migrations 130/133/139/144 already use.
--      Column shape for each is copied from its live head shape, read directly
--      out of sqlite_master rather than reconstructed from the migration
--      ledger by hand -- note subagent_runs carries 093's last_activity_at
--      ADD COLUMN on the end of 068's rebuilt shape, and workflow_runs carries
--      143's loop_run_id/loop_iteration on the end of 133's.
--
--   2. Data backfills, for every column that can already hold the old string.
--      That is the four rebuilt status columns above, plus two columns with no
--      CHECK guarding them at all and therefore easy to miss:
--        * nanite_recovery_breadcrumbs.outcome (055) -- the vocabulary lives
--          only in a trailing comment there, never in a constraint, which is
--          exactly why it needs saying out loud here.
--        * envelope_instances.response_status (internal/chat's ResponseStatus:
--          submitted|cancelled|partial), likewise unconstrained.
--
--      For the four CHECK-carrying tables the rewrite happens INSIDE the
--      rebuild's copy, as a CASE over the status column in the INSERT ...
--      SELECT, rather than as a separate UPDATE. That is forced, not stylistic:
--      an UPDATE before the rebuild writes the new spelling into the OLD table,
--      whose CHECK does not permit it yet, and an UPDATE after the rebuild
--      never runs because the copy itself has already failed the NEW table's
--      CHECK on the un-rewritten rows. Translating during the copy is the only
--      ordering where neither constraint is ever violated. (Found the direct
--      way: the first draft of this migration used pre-rebuild UPDATEs and
--      failed against a real backup copy with "CHECK constraint failed:
--      status IN (...,'cancelled',...)".)
--
--      The two unconstrained columns have no such problem and use plain
--      UPDATE ... SET backfills.
--
-- Also rewritten: the machine-generated "[envelope:<type> status:cancelled] "
-- prefix that chat.FormatEnvelopeResponseContent stamps onto envelope_response
-- message rows. That token is never parsed back into a status (structured.go
-- only tests the "[envelope:" prefix), so this is a consistency fix, not a
-- correctness one -- which is why it is deliberately scoped to
-- role='envelope_response' rows matching the exact machine-written token.
-- messages.content is NOT swept generally: it holds arbitrary user and model
-- prose, where the word "cancelled" is ordinary English and rewriting it would
-- be silent corruption of somebody's text, not a schema migration.
--
-- Incoming foreign keys make ordering matter: loop_runs.goal_id references
-- goals, and workflow_runs.loop_run_id / workflow_run_steps.loop_run_id
-- reference loop_runs. The tables are therefore rebuilt in dependency order
-- (goals, then loop_runs, then workflow_runs, then the independent
-- subagent_runs) under PRAGMA foreign_keys = OFF for the whole run -- the same
-- reasoning 133 and 144 both record: nothing is enforced or cascaded
-- mid-rebuild, and by commit time every table exists again under its own name
-- with identical row ids, so referential integrity is intact.

PRAGMA foreign_keys = OFF;

BEGIN;

-- ---------------------------------------------------------------- goals ----

CREATE TABLE IF NOT EXISTS goals_new (
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
                                    'satisfied','failed','canceled','superseded')),
    owner                    TEXT,
    source                   TEXT,
    created_at               TEXT NOT NULL DEFAULT (datetime('now')),
    activated_at             TEXT,
    completed_at             TEXT
);

INSERT INTO goals_new
    (id, parent_goal_id, intent, desired_state_json, constraints_json,
     acceptance_criteria_json, invariants_json, priority, scope, status,
     owner, source, created_at, activated_at, completed_at)
SELECT
    id, parent_goal_id, intent, desired_state_json, constraints_json,
    acceptance_criteria_json, invariants_json, priority, scope,
    CASE WHEN status = 'cancelled' THEN 'canceled' ELSE status END,
    owner, source, created_at, activated_at, completed_at
FROM goals;

DROP TABLE goals;

ALTER TABLE goals_new RENAME TO goals;

CREATE INDEX IF NOT EXISTS idx_goals_parent ON goals(parent_goal_id);

CREATE INDEX IF NOT EXISTS idx_goals_status ON goals(status);

-- ------------------------------------------------------------ loop_runs ----

CREATE TABLE IF NOT EXISTS loop_runs_new (
    id                       TEXT PRIMARY KEY,
    goal_id                  TEXT NOT NULL REFERENCES goals(id),
    definition_name          TEXT NOT NULL,
    status                   TEXT NOT NULL DEFAULT 'running'
                             CHECK (status IN ('running','completed','failed','canceled',
                                    'waiting_on_gate','waiting_on_escalation')),
    current_iteration        INTEGER NOT NULL DEFAULT 0,
    budget_json              TEXT NOT NULL DEFAULT '{}',
    continuation_policy_json TEXT NOT NULL DEFAULT '{}',
    no_progress_streak       INTEGER NOT NULL DEFAULT 0,
    started_at               TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at             TEXT
);

INSERT INTO loop_runs_new
    (id, goal_id, definition_name, status, current_iteration, budget_json,
     continuation_policy_json, no_progress_streak, started_at, updated_at,
     completed_at)
SELECT
    id, goal_id, definition_name,
    CASE WHEN status = 'cancelled' THEN 'canceled' ELSE status END,
    current_iteration, budget_json,
    continuation_policy_json, no_progress_streak, started_at, updated_at,
    completed_at
FROM loop_runs;

DROP TABLE loop_runs;

ALTER TABLE loop_runs_new RENAME TO loop_runs;

CREATE INDEX IF NOT EXISTS idx_loop_runs_goal ON loop_runs(goal_id, status);

CREATE INDEX IF NOT EXISTS idx_loop_runs_status ON loop_runs(status);

-- -------------------------------------------------------- workflow_runs ----

CREATE TABLE IF NOT EXISTS workflow_runs_new (
    id TEXT PRIMARY KEY,
    definition_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','completed','failed','canceled','waiting_on_gate','waiting_on_flex','waiting_on_loop')),
    input_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    loop_run_id TEXT REFERENCES loop_runs(id),
    loop_iteration INTEGER
);

INSERT INTO workflow_runs_new
    (id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration)
SELECT
    id, definition_name,
    CASE WHEN status = 'cancelled' THEN 'canceled' ELSE status END,
    input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration
FROM workflow_runs;

DROP TABLE workflow_runs;

ALTER TABLE workflow_runs_new RENAME TO workflow_runs;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_loop_run
    ON workflow_runs(loop_run_id, loop_iteration);

-- -------------------------------------------------------- subagent_runs ----

CREATE TABLE IF NOT EXISTS subagent_runs_new (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT NOT NULL,
    child_session_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'sync'
        CHECK(mode IN ('sync','async','api','interactive')),
    status TEXT NOT NULL DEFAULT 'requested'
        CHECK(status IN ('requested','approved','running','completed',
                         'failed','canceled','rejected',
                         'over_budget','stalled')),
    inputs_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    parent_agent_id TEXT NOT NULL DEFAULT '',
    envelope_instance_id TEXT NOT NULL DEFAULT '',
    approved_at TEXT NOT NULL DEFAULT '',
    approved_by TEXT NOT NULL DEFAULT '',
    rejected_at TEXT NOT NULL DEFAULT '',
    rejection_reason TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    on_fail TEXT NOT NULL DEFAULT 'retry'
        CHECK(on_fail IN ('retry','block','escalate')),
    attempts_json TEXT NOT NULL DEFAULT '[]',
    last_activity_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO subagent_runs_new
    (id, parent_session_id, child_session_id, role, prompt, mode, status,
     inputs_json, result_json, error, timeout_seconds, created_at, started_at,
     completed_at, parent_agent_id, envelope_instance_id, approved_at,
     approved_by, rejected_at, rejection_reason, provider, retry_count,
     max_retries, on_fail, attempts_json, last_activity_at)
SELECT
    id, parent_session_id, child_session_id, role, prompt, mode,
    CASE WHEN status = 'cancelled' THEN 'canceled' ELSE status END,
    inputs_json, result_json, error, timeout_seconds, created_at, started_at,
    completed_at, parent_agent_id, envelope_instance_id, approved_at,
    approved_by, rejected_at, rejection_reason, provider, retry_count,
    max_retries, on_fail, attempts_json, last_activity_at
FROM subagent_runs;

DROP TABLE subagent_runs;

ALTER TABLE subagent_runs_new RENAME TO subagent_runs;

CREATE INDEX IF NOT EXISTS idx_subagent_runs_parent  ON subagent_runs(parent_session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_subagent_runs_status  ON subagent_runs(status, created_at);

CREATE INDEX IF NOT EXISTS idx_subagent_runs_pending ON subagent_runs(status, created_at)
    WHERE status = 'requested';

-- --------------------------------------- unconstrained status columns ----

UPDATE nanite_recovery_breadcrumbs SET outcome = 'canceled' WHERE outcome = 'cancelled';

UPDATE envelope_instances SET response_status = 'canceled' WHERE response_status = 'cancelled';

UPDATE messages
   SET content = replace(content, ' status:cancelled] ', ' status:canceled] ')
 WHERE role = 'envelope_response'
   AND content LIKE '[envelope:% status:cancelled] %';

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Restores the British spelling in all four CHECK constraints and reverses
-- every backfill, so a downgrade leaves no row stranded against the narrower
-- old vocabulary. Same dependency ordering and same foreign_keys = OFF
-- reasoning as the Up migration, and the same CASE-inside-the-copy ordering:
-- the translation happens in each rebuild's INSERT ... SELECT, never as a
-- separate UPDATE, for exactly the reason the Up direction records. The two
-- unconstrained columns and the message-token rewrite are plain UPDATEs at the
-- end, as they are on the way up.

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE IF NOT EXISTS goals_new (
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

INSERT INTO goals_new
    (id, parent_goal_id, intent, desired_state_json, constraints_json,
     acceptance_criteria_json, invariants_json, priority, scope, status,
     owner, source, created_at, activated_at, completed_at)
SELECT
    id, parent_goal_id, intent, desired_state_json, constraints_json,
    acceptance_criteria_json, invariants_json, priority, scope,
    CASE WHEN status = 'canceled' THEN 'cancelled' ELSE status END,
    owner, source, created_at, activated_at, completed_at
FROM goals;

DROP TABLE goals;

ALTER TABLE goals_new RENAME TO goals;

CREATE INDEX IF NOT EXISTS idx_goals_parent ON goals(parent_goal_id);

CREATE INDEX IF NOT EXISTS idx_goals_status ON goals(status);

CREATE TABLE IF NOT EXISTS loop_runs_new (
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

INSERT INTO loop_runs_new
    (id, goal_id, definition_name, status, current_iteration, budget_json,
     continuation_policy_json, no_progress_streak, started_at, updated_at,
     completed_at)
SELECT
    id, goal_id, definition_name,
    CASE WHEN status = 'canceled' THEN 'cancelled' ELSE status END,
    current_iteration, budget_json,
    continuation_policy_json, no_progress_streak, started_at, updated_at,
    completed_at
FROM loop_runs;

DROP TABLE loop_runs;

ALTER TABLE loop_runs_new RENAME TO loop_runs;

CREATE INDEX IF NOT EXISTS idx_loop_runs_goal ON loop_runs(goal_id, status);

CREATE INDEX IF NOT EXISTS idx_loop_runs_status ON loop_runs(status);

CREATE TABLE IF NOT EXISTS workflow_runs_new (
    id TEXT PRIMARY KEY,
    definition_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','completed','failed','cancelled','waiting_on_gate','waiting_on_flex','waiting_on_loop')),
    input_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    loop_run_id TEXT REFERENCES loop_runs(id),
    loop_iteration INTEGER
);

INSERT INTO workflow_runs_new
    (id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration)
SELECT
    id, definition_name,
    CASE WHEN status = 'canceled' THEN 'cancelled' ELSE status END,
    input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration
FROM workflow_runs;

DROP TABLE workflow_runs;

ALTER TABLE workflow_runs_new RENAME TO workflow_runs;

CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status, started_at);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_loop_run
    ON workflow_runs(loop_run_id, loop_iteration);

CREATE TABLE IF NOT EXISTS subagent_runs_new (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT NOT NULL,
    child_session_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'sync'
        CHECK(mode IN ('sync','async','api','interactive')),
    status TEXT NOT NULL DEFAULT 'requested'
        CHECK(status IN ('requested','approved','running','completed',
                         'failed','cancelled','rejected',
                         'over_budget','stalled')),
    inputs_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    parent_agent_id TEXT NOT NULL DEFAULT '',
    envelope_instance_id TEXT NOT NULL DEFAULT '',
    approved_at TEXT NOT NULL DEFAULT '',
    approved_by TEXT NOT NULL DEFAULT '',
    rejected_at TEXT NOT NULL DEFAULT '',
    rejection_reason TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    on_fail TEXT NOT NULL DEFAULT 'retry'
        CHECK(on_fail IN ('retry','block','escalate')),
    attempts_json TEXT NOT NULL DEFAULT '[]',
    last_activity_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO subagent_runs_new
    (id, parent_session_id, child_session_id, role, prompt, mode, status,
     inputs_json, result_json, error, timeout_seconds, created_at, started_at,
     completed_at, parent_agent_id, envelope_instance_id, approved_at,
     approved_by, rejected_at, rejection_reason, provider, retry_count,
     max_retries, on_fail, attempts_json, last_activity_at)
SELECT
    id, parent_session_id, child_session_id, role, prompt, mode,
    CASE WHEN status = 'canceled' THEN 'cancelled' ELSE status END,
    inputs_json, result_json, error, timeout_seconds, created_at, started_at,
    completed_at, parent_agent_id, envelope_instance_id, approved_at,
    approved_by, rejected_at, rejection_reason, provider, retry_count,
    max_retries, on_fail, attempts_json, last_activity_at
FROM subagent_runs;

DROP TABLE subagent_runs;

ALTER TABLE subagent_runs_new RENAME TO subagent_runs;

CREATE INDEX IF NOT EXISTS idx_subagent_runs_parent  ON subagent_runs(parent_session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_subagent_runs_status  ON subagent_runs(status, created_at);

CREATE INDEX IF NOT EXISTS idx_subagent_runs_pending ON subagent_runs(status, created_at)
    WHERE status = 'requested';

UPDATE nanite_recovery_breadcrumbs SET outcome = 'cancelled' WHERE outcome = 'canceled';

UPDATE envelope_instances SET response_status = 'cancelled' WHERE response_status = 'canceled';

UPDATE messages
   SET content = replace(content, ' status:canceled] ', ' status:cancelled] ')
 WHERE role = 'envelope_response'
   AND content LIKE '[envelope:% status:canceled] %';

END;

PRAGMA foreign_keys = ON;
