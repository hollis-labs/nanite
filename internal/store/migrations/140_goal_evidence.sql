-- TASKS/loops/02-goal-evidence-schema.md.
--
-- Adds goal_evidence -- the storage half of docs/engineering/architecture/
-- 21-loops.md's Decision 2 "Goal evidence is a thin pointer table, not a
-- duplicate content store" claim, quoted directly in this task's own
-- Context section: "It points into workflow_run_steps.verify_json, a
-- gate's resolution record, or an event_log row -- the same
-- 'derived/observability, not new persistent state' discipline Teams
-- applied to routing provenance." Goal satisfaction (goal_met =
-- acceptance_criteria_satisfied AND constraints_satisfied AND
-- invariants_preserved AND required_evidence_present, design doc's own §7
-- formula) is computed by walking these pointers -- see
-- internal/store/goal_evidence.go's EvaluateGoalEvidence/
-- EvidenceSatisfiesGoal, this task's real "query" deliverable.
--
-- Migration-number note: this worker was explicitly dispatch-assigned
-- migration number 137 (since renumbered to 140; see
-- TASKS/loops/HANDOFF.md's 2026-08-22 renumbering note -- not re-derived by
-- this worker's own re-verify procedure) because a sibling worker was
-- concurrently implementing task
-- 03 (loop_runs) off the same base in an isolated worktree, and both
-- workers independently re-verifying against their own worktree's
-- only-134-visible state is exactly what produced Loops Wave 1's earlier
-- 135 (now 138) collision (see TASKS/ESCALATIONS.md). Confirmed via `ls
-- internal/store/migrations/ | sort -t_ -k1 -n | tail -8` immediately
-- before writing this file that 137_*.sql did not already exist in this
-- worktree (the latest migration on disk was 136_workflow_run_steps_
-- loop_kind.sql, now 139_workflow_run_steps_loop_kind.sql) -- no anomaly,
-- 137 was free as assigned.
--
-- This session's own decision on the design doc's open "free-text
-- evidence" question (21-loops.md's "What this session did not decide" /
-- this batch's own README "What this session decided"): no -- every row
-- points at something structured. ref_table/ref_id are both NOT NULL,
-- never a free-text-only row. A human's written acceptance note is
-- captured as a real event_log row first (ref_table='event_log', ref_id =
-- that row's id) and pointed at from here -- event_log already exists
-- (migration 001_schema.sql) as an append-only durable log and is the
-- natural home for a one-off text note, not a new nullable free-text
-- column on this table.
--
-- loop_run_id is deliberately a plain nullable TEXT column with NO FK
-- constraint here, unconditionally -- not a placeholder pending
-- resolution once task 03's landing order against this task becomes
-- known. Both this task (02) and task 03 (loop_runs) depend only on task
-- 01 (goals), not on each other, per this batch's README Wave 2 grouping
-- -- this worker cannot reliably know whether loop_runs exists yet at the
-- time this migration runs in any given merge order, and picking a
-- guessed FK now would make this migration's correctness depend on merge
-- order, which is exactly the kind of cross-worktree race this task's own
-- dispatch instructions call out to avoid. Goal evidence can legitimately
-- exist with loop_run_id IS NULL regardless -- a human-authored acceptance
-- note against a DEFINED goal with no loop launched yet is a real, valid
-- row, not just a transitional state. Tightening this to a hard
-- REFERENCES loop_runs(id) FK, once both tables are known to coexist, is
-- a real follow-up for whichever later task/reviewer notices it -- not
-- this task's job.
--
-- Precedent template for this table's shape --
-- 132_team_authority_grants.sql (the most recent "normalized sub-table
-- pointing at a parent definition row" migration): plain CREATE TABLE,
-- composite indexes, no rebuild dance. Brand-new table, nothing to
-- rebuild.

-- +goose Up
CREATE TABLE IF NOT EXISTS goal_evidence (
    id               TEXT PRIMARY KEY,
    goal_id          TEXT NOT NULL REFERENCES goals(id),
    loop_run_id      TEXT,
    iteration_number INTEGER,
    evidence_type    TEXT NOT NULL
                     CHECK (evidence_type IN ('test_suite','verify_result','gate_approval',
                            'human_acceptance','artifact')),
    ref_table        TEXT NOT NULL,
    ref_id           TEXT NOT NULL,
    result           TEXT,
    summary          TEXT,
    recorded_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_goal_evidence_goal ON goal_evidence(goal_id, evidence_type);
CREATE INDEX IF NOT EXISTS idx_goal_evidence_loop_run ON goal_evidence(loop_run_id, iteration_number);

-- +goose Down
DROP TABLE IF EXISTS goal_evidence;
