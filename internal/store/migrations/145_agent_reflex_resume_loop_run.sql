-- +goose Up
-- +goose NO TRANSACTION
-- TASKS/loops/11-loop-event-predicate-trigger.md.
--
-- Widens agent_reflexes.action_kind's CHECK constraint (and its
-- REFERENCES reflex_action_kinds(name) FK, added by migration
-- 124_reflex_action_taxonomy.sql) to add a seventh action kind,
-- resume_loop_run -- the realization of docs/engineering/architecture/
-- 21-loops.md's "Event/predicate" trigger surface as a real, named reflex
-- action kind rather than a generic `callback` (explicitly rejected,
-- docs/engineering/architecture/10-reflex-action-taxonomy.md:120 --
-- "an opaque callback target is illegible to the combining-algorithm and
-- provenance-ceiling facets ... callback is at most the underlying
-- execution mechanism for such a row, never a bare escape hatch of its
-- own"). 119_agent_reflex_dispatch_to_agent.sql is the direct precedent
-- for adding a kind this same way.
--
-- A resume_loop_run reflex row is scoped to resuming one specific
-- LoopRun (action_spec = {"loop_run_id": "<id>"}) -- created by a future
-- task when a LoopRun transitions to loop_runs.status =
-- 'waiting_on_escalation' (the one status internal/loop/engine.go's
-- evaluateDecideAndAct persists for BOTH a DecisionWait and a
-- DecisionEscalate decision -- see this task's own Work Log for why
-- "WAIT status" in the design doc's own words is realized as this one
-- collapsed status, not a distinct literal) with an event/predicate
-- resume condition, not authored ad hoc by a human the way most reflexes
-- are. Its own trigger_kind/trigger_spec columns (agent_reflexes already
-- has these) reuse the existing predicate/event/interval trigger-spec
-- AST unchanged (internal/agent/reflexes/evaluator.go's EvaluateTrigger)
-- -- no new column is needed for the trigger spec itself, only this new
-- action kind. See internal/agent/reflexes/executor.go's
-- ReflexActionResumeLoopRun case and internal/service/
-- loop_resume_reflex.go's EvaluateLoopRunResumeReflexes for the real
-- handler + evaluation entry point this migration's new kind feeds.
--
-- Same rename-recreate-copy pattern as 119/124/131's own rebuilds of
-- this exact table (SQLite cannot widen a CHECK constraint or add a
-- REFERENCES clause in place). The column shape below is the full,
-- current live schema as of this migration: 124's provenance_tier/
-- recurrence_override_seconds columns plus 131_agent_reflexes_
-- workflow_run_scoping.sql's workflow_run_id column (that one was a
-- plain ADD COLUMN, no rebuild needed at the time) -- this is the first
-- rebuild since 131 added workflow_run_id, so it is the first migration
-- that must carry that column through a rebuild's own CREATE TABLE.
--
-- reflex_action_kinds (migration 124) gets a new row for
-- resume_loop_run: category='execute_action' (deterministic -- the
-- harness calls LoopEngine.Resume directly, no LLM discretion involved),
-- combining_algorithm='all_applicable' (unlike dispatch_to_agent's
-- first_applicable -- routing one turn to two different agents at once
-- would be a real conflict, but calling LoopEngine.Resume more than once
-- in the same pass, whether from one or several resume_loop_run
-- candidates, is idempotent-safe -- internal/loop/engine.go's own
-- TestLoopEngine_Run_BudgetExhausted_EscalatesThenResumeContinues test
-- already establishes "a second Resume against the still-waiting
-- LoopRun must behave identically" as a load-bearing property; see this
-- task's Work Log for the full reasoning), default_recurrence_seconds=0
-- (no artificial cooldown -- same reasoning as dispatch_to_agent's own
-- kind-level default: the real evaluation cadence this task's Work Log
-- documents -- a scheduled tick re-checking a still-waiting LoopRun --
-- needs every invocation to genuinely re-evaluate the trigger, not be
-- silently suppressed by the 15-minute system default).
--
-- reflex_action_kind_provenance_allow (migration
-- 125_reflex_action_kind_provenance_allow.sql) gets resume_loop_run x
-- {system, operator} -- not plugin, mirroring halt_session/
-- dispatch_to_agent's own precedent for a kind whose blast radius
-- (driving a LoopRun's own execution forward) is a real subsystem side
-- effect, not an advisory nudge.

PRAGMA foreign_keys = OFF;

BEGIN;

INSERT INTO reflex_action_kinds (name, category, combining_algorithm, default_recurrence_seconds) VALUES
    ('resume_loop_run', 'execute_action', 'all_applicable', 0);

INSERT INTO reflex_action_kind_provenance_allow (kind_name, tier_name) VALUES
    ('resume_loop_run', 'system'),
    ('resume_loop_run', 'operator');
    -- ('resume_loop_run', 'plugin') deliberately omitted, matching
    -- halt_session/dispatch_to_agent.

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
        action_kind IN ('inject_reminder','force_tool_choice','send_message','halt_session','add_schedule','dispatch_to_agent','resume_loop_run')
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
    recurrence_override_seconds INTEGER,
    workflow_run_id             TEXT REFERENCES workflow_runs(id)
);

INSERT INTO agent_reflexes_new
    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
     action_kind, action_spec, status, priority, fired_count,
     last_fired_at, created_at, created_by, opt_out_allowed,
     provenance_tier, recurrence_override_seconds, workflow_run_id)
SELECT
    id, agent_id, class_tag, name, trigger_kind, trigger_spec,
    action_kind, action_spec, status, priority, fired_count,
    last_fired_at, created_at, created_by, opt_out_allowed,
    provenance_tier, recurrence_override_seconds, workflow_run_id
FROM agent_reflexes;

DROP TABLE agent_reflexes;

ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_workflow_run
    ON agent_reflexes(workflow_run_id, status);

END;

PRAGMA foreign_keys = ON;

-- +goose Down
-- Rebuilds agent_reflexes back to 131_agent_reflexes_workflow_run_
-- scoping.sql's shape (six-value CHECK, no resume_loop_run), and drops
-- the two new lookup rows this migration's Up added. Lossy for any
-- resume_loop_run row inserted after the Up migration -- same accepted
-- precedent as 119/124's own Downs: a genuine downgrade is expected to
-- have none (new functionality, not a widen-then-narrow round trip over
-- pre-existing data).

PRAGMA foreign_keys = OFF;

BEGIN;

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
    recurrence_override_seconds INTEGER,
    workflow_run_id             TEXT REFERENCES workflow_runs(id)
);

INSERT INTO agent_reflexes_new
    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
     action_kind, action_spec, status, priority, fired_count,
     last_fired_at, created_at, created_by, opt_out_allowed,
     provenance_tier, recurrence_override_seconds, workflow_run_id)
SELECT
    id, agent_id, class_tag, name, trigger_kind, trigger_spec,
    action_kind, action_spec, status, priority, fired_count,
    last_fired_at, created_at, created_by, opt_out_allowed,
    provenance_tier, recurrence_override_seconds, workflow_run_id
FROM agent_reflexes;

DROP TABLE agent_reflexes;

ALTER TABLE agent_reflexes_new RENAME TO agent_reflexes;

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_agent
    ON agent_reflexes(agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_class
    ON agent_reflexes(class_tag, status);

CREATE INDEX IF NOT EXISTS idx_agent_reflexes_workflow_run
    ON agent_reflexes(workflow_run_id, status);

END;

PRAGMA foreign_keys = ON;

DELETE FROM reflex_action_kind_provenance_allow WHERE kind_name = 'resume_loop_run';
DELETE FROM reflex_action_kinds WHERE name = 'resume_loop_run';
