-- 020_remove_seed_temporary_agents.sql
-- CW-20260417-0487
--
-- Remove the placeholder agent_profiles row(s) inserted on 2026-04-17 to
-- silence the "could not load agent for permissions" WARN spam for
-- file-based agents (see broker.go:GetPermissions). The fix lands in this
-- ticket: the toolclient now resolves file-based agent IDs through an
-- in-memory PermissionResolver wired by service.NewContainer, so the
-- placeholder row is no longer needed.
--
-- The 'seed-temporary' source value was chosen specifically to keep
-- adapter-nanite-native's removed-agents sweep away from the row (it only
-- disables 'nanite' / 'agentrc' sources). Because no production code path
-- ever creates rows with this source, a blanket DELETE is safe.
--
-- Idempotent: DELETE on a non-existent row is a no-op.

BEGIN;

DELETE FROM agent_profiles WHERE source = 'seed-temporary';

COMMIT;
