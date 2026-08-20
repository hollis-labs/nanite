-- TASKS/teams/04-team-authority-schema.md.
--
-- Adds team_authority_grants -- the storage for docs/engineering/
-- architecture/15-teams.md's Team authority mechanism (may_spawn/
-- may_message/may_not_review): "who may [verb] whom" between two Team
-- Slots within one saved Team.
--
-- Migration-number note: the latest migration on disk as of this task's
-- dispatch was 131_agent_reflexes_workflow_run_scoping.sql (tasks 01/02/
-- 03/05 already landed as 128/129/130/131 respectively). Re-confirmed via
-- `ls internal/store/migrations/ | sort -t_ -k1 -n | tail -3` immediately
-- before writing this file -- 132 was still free, used as the task file's
-- own numbering note anticipated.
--
-- Correcting the design doc's own load-bearing misreading (see this task
-- file's Context section for the full citation trail, verified directly
-- against the code, not assumed): 15-teams.md's "Authority: generalize,
-- don't invent" section says this should generalize
-- agent_parent_dispatch_allowlist (migration 060). That mechanism is
-- advisory-only -- its sole consumer renders it into an LLM-facing tool
-- description (internal/service/tool.go's parseParentDispatchAllowlist),
-- never enforced by a hard gate anywhere. This table is NOT built as a
-- copy of that pattern. It is a real, load-bearing grant table modeled on
-- dispatch.TrustTier/ErrUntrustedRole's genuine enforcement shape
-- (internal/dispatch/trust.go, internal/subagent/service.go:785-787) --
-- resolve, then a hard `if`-gate at the real call site. This task does not
-- wire that gate in (that's tasks 08/09's job) -- it builds the storage
-- and the pure, directly-testable check function
-- (internal/store/team_authority.go's AuthorizedForVerb) those tasks will
-- call.
--
-- verb CHECK deliberately NOT widened beyond the three verbs
-- 15-teams.md's own illustrative example names (may_spawn/may_message/
-- may_not_review). The design doc is explicit that the verb set is not
-- locked ("may_delegate"/"may_approve"/"may_signal" are named as plausible
-- future additions, "No verb set is locked by this session") -- but it is
-- equally explicit this is a warning against premature ossification, not
-- an instruction to speculatively add one now. No real, documented need
-- for a fourth verb exists yet in this batch (tasks 08/09, the only
-- consumers, only ever check may_spawn/may_message/may_not_review). Adding
-- one unused would be exactly the kind of premature, undriven-by-real-
-- usage addition 15-teams.md warns against elsewhere ("Multi-member slot
-- addressing... no syntax or default is chosen here, deliberately, since
-- real usage should inform it"). Widening the CHECK later (verb IN (...,
-- 'may_delegate')) is a one-line migration when a real consumer needs it --
-- exactly the "CHECK-constraint widening, not a new column/migration per
-- verb" shape this task's own instructions call for.
--
-- to_slot = 'self' is a legitimate stored value (15-teams.md's
-- `reviewer.may_not_review: self` example) -- a sentinel meaning "excludes
-- the granting slot itself as a valid target," not a literal Team Slot
-- named "self". Resolved by AuthorizedForVerb (team_authority.go), not by
-- a DB-level CHECK -- see that file's doc comment for the exact match
-- semantics.
--
-- No FK from from_slot/to_slot to a normalized slots table, per this task
-- file's own "Depends on" note: Team Slots live inside teams.slots_json
-- (a JSON blob, decoded via TeamSlotDefinition), not a normalized table --
-- there is nothing to reference. team_id does reference teams(id), the one
-- real normalized parent this table has.
--
-- Brand-new table, nothing to rebuild -- plain transactional CREATE TABLE,
-- matching 126_selftool_reactions.sql's / 128_teams.sql's / 129_team_run_
-- members.sql's precedent (no existing rows to preserve, so no PRAGMA
-- foreign_keys / rename-recreate-copy rebuild dance is needed).

-- +goose Up
CREATE TABLE IF NOT EXISTS team_authority_grants (
    id            TEXT PRIMARY KEY,
    team_id       TEXT NOT NULL REFERENCES teams(id),
    from_slot     TEXT NOT NULL,
    verb          TEXT NOT NULL CHECK (verb IN ('may_spawn','may_message','may_not_review')),
    to_slot       TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_team_authority_grants_team_slot
    ON team_authority_grants(team_id, from_slot, verb);

-- +goose Down
DROP TABLE IF EXISTS team_authority_grants;
