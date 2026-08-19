-- +goose Up
-- Phase 1 item 07 (TASKS/phase-1/07-add-reflex-opt-out-field.md): closes the
-- gap architecture doc 01-agent-construction.md's "Reflexes at construction
-- time" section names directly -- agent_reflexes already supports
-- agent_id IS NULL (class-bound/global) vs. a specific agent_id (per-agent),
-- but nothing distinguished "required, cannot opt out" from "default-on,
-- agent may opt out." Phase 3's Steering work (dispatch_to_agent action
-- kind, promptrouter migration) depends on this field existing per
-- TASKS/INDEX.md's Phase 3 summary row.
--
-- Two pieces:
--
--   1. agent_reflexes.opt_out_allowed -- BOOLEAN NOT NULL DEFAULT TRUE.
--      Default permissive so every existing row (all 11 current base-reflex
--      seeds plus any operator/agent-created reflex) keeps its current
--      always-applies-if-listed behavior after this migration runs. Only
--      genuinely safety-critical seeds get opt_out_allowed=0 explicitly, in
--      a follow-up UPDATE below and in seeds.go's Go-side seed data (the
--      three halt_session base seeds: drift_detector_echo's FU-13
--      cache-miss-echo kill switch, and template class's
--      task_complete_self_terminate / task_timeout hard lifecycle caps --
--      all three exist specifically so an agent cannot keep running past a
--      condition the reflex owner decided was non-negotiable; letting an
--      agent suppress its own kill switch defeats the point). Every other
--      seed is an inject_reminder nudge, correctly left opt-out-able.
--
--   2. agent_reflex_opt_outs -- the per-agent override mechanism. No such
--      mechanism existed anywhere in the codebase before this migration
--      (grepped for suppress/opt_out/disabled_reflex/override -- nothing).
--      A row (agent_id, reflex_id) means "this agent has opted out of this
--      specific agent_reflexes row." internal/store's
--      ListAgentReflexesForAgent consults this table: a class-bound reflex
--      with opt_out_allowed=1 is excluded for an agent that has an opt-out
--      row against it; opt_out_allowed=0 reflexes are never excluded by
--      this table regardless of its contents. ON DELETE CASCADE on both FKs
--      (agent_profiles(id), agent_reflexes(id)) since an opt-out row has no
--      independent meaning once either side is gone -- unlike
--      agent_reflexes.agent_id itself, which this codebase's DeleteAgent
--      cleans up via an explicit DELETE (no cascade) alongside half a dozen
--      other per-agent child tables; a plain two-column override marker
--      with no fired_count/last_fired_at history worth preserving doesn't
--      need that same explicit-list treatment, so CASCADE here matches this
--      schema's existing precedent (agent_mode_assignments.mode_id
--      REFERENCES modes(id) ON DELETE CASCADE, 001_schema.sql -- dropped
--      along with the rest of Modes by 104_cut_modes.sql, but still the
--      precedent for this schema using CASCADE where a child row has no
--      independent meaning) rather than the agent_reflexes precedent.

-- No explicit BEGIN/END/NO TRANSACTION here (unlike 105/075's table-rebuild
-- migrations, which must toggle PRAGMA foreign_keys and so cannot run
-- inside SQLite's own transaction) -- this migration only does ADD COLUMN /
-- UPDATE / CREATE TABLE / CREATE INDEX, all transaction-safe, so goose's
-- own default per-migration transaction wrapping (same as 103/104's
-- pattern) is sufficient and correct here.

ALTER TABLE agent_reflexes ADD COLUMN opt_out_allowed BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE agent_reflexes
   SET opt_out_allowed = FALSE
 WHERE agent_id IS NULL
   AND action_kind = 'halt_session'
   AND name IN ('drift_detector_echo', 'task_complete_self_terminate', 'task_timeout');

CREATE TABLE IF NOT EXISTS agent_reflex_opt_outs (
    agent_id   TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    reflex_id  TEXT NOT NULL REFERENCES agent_reflexes(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (agent_id, reflex_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_reflex_opt_outs_reflex
    ON agent_reflex_opt_outs(reflex_id);

-- +goose Down
-- Drops the opt-out table and column. opt_out_allowed's pre-migration
-- world had no such distinction at all (every reflex behaved as if
-- permissive/opt-out-able), so structure-only is the honest Down here --
-- same lossy-drop precedent as 103/102's Downs for this codebase's other
-- post-goose column cuts.

DROP TABLE IF EXISTS agent_reflex_opt_outs;

ALTER TABLE agent_reflexes DROP COLUMN opt_out_allowed;
