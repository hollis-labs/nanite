-- +goose Up
-- 159_instance_actor_urn_and_profile_urn_retirement.sql
-- CW-20260912-0017. Decided by Chrispian 2026-09-12.
--
-- Moves actor identity from the profile to the durable instance, and retires
-- the profile URN. One migration rather than two on purpose: where identity is
-- minted is the decision that costs the most to change later (Tether's
-- messaging-integration.md §1 — "changing it later means migrating every
-- message already addressed to the old URN"), so re-minting and re-homing pay
-- that cost once instead of leaving a window where neither scheme is
-- authoritative.
--
-- THREE THINGS, and why each is here.
--
-- 1. durable_agent_instances gets `urn`. The architecture is explicit that a
--    definition is not a recipient and that "sharing a definition does not
--    share identity". The live data already violated that: 18 instances across
--    17 profiles, so profile d500a7c4-2053-4a30-b49f-831d1d780bb1 carries two
--    instances that shared one profile URN. After this they hold two distinct
--    actor URNs, which is the point.
--
-- 2. The 18 existing instances are backfilled DETERMINISTICALLY from their own
--    id rather than randomly. A restore-then-re-migrate then reproduces the
--    same URNs instead of minting a second set of actors for the same
--    instances — which for an identity column is worth more than matching the
--    random shape a2a.GenerateAgentURN produces for new rows. Backfilled ids
--    are therefore visibly derived (agt_ + the instance uuid, dashes stripped)
--    and new ones are agt_ + 10 base32 chars. go-messaging's ParseURN
--    validates non-empty parts and the kind, not the id charset, so both
--    parse.
--
-- 3. agent_profiles.urn / urn_aliases are RENAMED to legacy_*, not dropped and
--    not blanked. Stated because the task asked for one reading and a reason:
--
--      - Renaming makes non-deliverability structural rather than advisory. Go
--        will not compile against a field that moved, so every reference is
--        found at build time instead of becoming a silent miss.
--      - Dropping would destroy 70 strings (35 urn + 35 urn_aliases, exactly
--        one alias each) while the question of what, if anything, was ever
--        registered against them is still settling. Tether's registry holds no
--        Nanite profiles today, but keeping the values costs nothing and
--        reconciling without them is impossible.
--      - Blanking keeps a column that looks addressable and is empty, which is
--        the worst of the three.
--
--    SQLite rewrites dependent index definitions on RENAME COLUMN, verified
--    against a copy of the live database: idx_agent_profiles_urn follows to
--    legacy_urn and PRAGMA integrity_check stays ok. This repo has no prior
--    RENAME COLUMN, but it is lower risk here than the INSERT ... SELECT
--    rebuild, which requires restating the whole column list by hand.
--
-- SAFE TO RETIRE, derived rather than assumed. Nothing in Nanite routes on a
-- profile URN:
--   - GetAgentByURN (store/agents.go) has zero production callers; only its
--     own test file references it.
--   - whoami does not read the column at all — it builds an address live via
--     a2a.NewAgentAddress, already under the `nanite` authority.
--   - A2A classifyTarget parses the URN, uses addr.ID, and ignores authority.
--   - Nanite's own mailbox addresses by (to_session_id, to_agent_id) in
--     agent_messages, not by URN.
--   - messaging_envelopes and agent_mailbox_view do not exist: created by 065
--     and 077, dropped by 097, and referenced by no Go file.
--   - The UI declares urn/urn_aliases in types.ts and reads neither.
--
-- FUTURE DEPENDENCY, named here because it is cheap now and expensive to
-- discover later. Tether's ADR 0040 makes the authority the routing key.
-- Federation is currently off, which is why minting into `agent-mux` was inert
-- rather than actively misrouting. When Tether enables federation, `nanite`
-- becomes a foreign authority and Tether will need a peer entry
-- {authority: nanite, base_url: ...} or it cannot address a Nanite agent at
-- all. This migration does not create that entry and does not need to.

ALTER TABLE durable_agent_instances ADD COLUMN urn TEXT NOT NULL DEFAULT '';

UPDATE durable_agent_instances
   SET urn = 'msg://agent/nanite/agt_' || lower(replace(id, '-', ''))
 WHERE urn = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_durable_agent_instances_urn
    ON durable_agent_instances(urn)
 WHERE urn != '';

ALTER TABLE agent_profiles RENAME COLUMN urn TO legacy_urn;
ALTER TABLE agent_profiles RENAME COLUMN urn_aliases TO legacy_urn_aliases;

-- +goose Down
-- Restores the profile URN columns to their addressable names and removes the
-- instance actor URN. The 70 legacy strings survive the round trip because Up
-- renamed rather than dropped them; the instance URNs do not, since Up minted
-- them. That asymmetry is the reason Up is deterministic — re-applying it
-- after a downgrade reproduces the same instance URNs rather than a second set
-- of actors.

DROP INDEX IF EXISTS idx_durable_agent_instances_urn;

ALTER TABLE agent_profiles RENAME COLUMN legacy_urn_aliases TO urn_aliases;
ALTER TABLE agent_profiles RENAME COLUMN legacy_urn TO urn;

ALTER TABLE durable_agent_instances DROP COLUMN urn;
