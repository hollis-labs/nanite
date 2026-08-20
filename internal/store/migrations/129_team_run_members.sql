-- TASKS/teams/02-team-run-members-table.md.
--
-- docs/engineering/architecture/15-teams.md's "Slot resolution and the one
-- genuinely new persistence table" section: "The one real new table this
-- design needs is the resolution record itself -- team_run_members
-- (workflow_run_id, slot_name, agent_id, session_id, resolved_at,
-- status)". Everything else a naive reading of the reviewed Teams proposal
-- would turn into new tables (team_run_routes/_messages/_tasks/_decisions)
-- is deliberately NOT built here -- those are filtered views over
-- agent_messages/event_log/agent_reflexes firings by workflow_run_id, not
-- new parallel state (same doc, same section).
--
-- Migration-number note: this task file's dispatch pre-assigned 129 to
-- this task specifically to avoid colliding with three sibling tasks (01,
-- 03, 05) writing their own new migrations in parallel this same batch
-- (128/130/131 respectively). Confirmed 129 was still free via
-- `ls internal/store/migrations/ | sort -t_ -k1 -n | tail -10` immediately
-- before writing this file -- 127_schedule_runs_and_retry_policy.sql was
-- still the latest on disk, so 129 is used as pre-assigned, no renumbering
-- needed.
--
-- team_run_members binds a Team Slot's resolved (agent_id, session_id)
-- tuple to the TeamRun (a workflow_runs row -- 15-teams.md Decision 2:
-- "TeamRun IS a WorkflowRun") that resolved it. The same tuple
-- internal/messaging already addresses agent_messages by, per the design
-- doc's own framing.
--
-- Deliberately NO unique constraint on (workflow_run_id, slot_name): a
-- concurrent-activation-mode slot (design doc's `engineer: min:1, max:4`
-- example) resolves to multiple concrete members, i.e. multiple rows for
-- the same (workflow_run_id, slot_name) pair. This table only needs to
-- store N rows correctly per that pair -- it does not resolve the
-- multi-member-slot addressing question (@engineer route-to-one vs.
-- broadcast vs. address-a-specific-member), which 15-teams.md's own "What
-- this session did not decide" list leaves open and defers to task 09.
--
-- status vocabulary is taken verbatim from this task file's own
-- illustrative DDL -- active/failed/replaced/stopped, no deviation:
--   active   -- the resolved member is the current, live occupant of the
--              slot for this run.
--   failed   -- the resolved member (fresh-spawned or durable-woken)
--              failed to instantiate or crashed mid-run; not replaced yet.
--   replaced -- superseded by a later row resolving the same
--              (workflow_run_id, slot_name) pair (e.g. the orchestrator
--              spawns a second engineer, or a failed member is swapped).
--   stopped  -- the member's participation in this run ended normally
--              (phase closed, run completed, or the member was
--              deliberately stood down).
-- This is storage-only (task 02's own "Done means": "no slot-resolution
-- logic is wired here") -- no code in this batch transitions a row through
-- this vocabulary yet; task 08 (slot-resolution logic) and task 09
-- (routing-failure handling) are the first real readers/writers of
-- `status` and must agree with this vocabulary, not invent their own.
--
-- Brand new table, no existing data to preserve -- a plain CREATE TABLE is
-- sufficient here, matching 126_selftool_reactions.sql's own precedent,
-- not 127's PRAGMA foreign_keys / rename-recreate-copy rebuild dance
-- (needed there only because agent_schedules was an existing table with
-- rows to migrate forward).

-- +goose Up
CREATE TABLE IF NOT EXISTS team_run_members (
    id                TEXT PRIMARY KEY,
    workflow_run_id   TEXT NOT NULL REFERENCES workflow_runs(id),
    slot_name         TEXT NOT NULL,
    agent_id          TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id        TEXT NOT NULL REFERENCES sessions(id),
    resolved_at       TEXT NOT NULL DEFAULT (datetime('now')),
    status            TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','failed','replaced','stopped')
    )
);

CREATE INDEX IF NOT EXISTS idx_team_run_members_run_slot
    ON team_run_members(workflow_run_id, slot_name);

-- +goose Down
DROP TABLE IF EXISTS team_run_members;
