-- +goose Up
-- H1 (CW-20260421-0014): retire migration 026's subagent_approval_required=0
-- now that the per-role trust model replaces it. developer_mode=1 stays.
--
-- Migration runner re-runs every boot. All DML below is WHERE-guarded for
-- idempotency.

-- Re-enable subagent approvals globally — trust comes from per-role tier.
-- WHERE guard mirrors migration 026 pattern: only flip rows still at 0.
UPDATE user_settings
   SET subagent_approval_required = 1
 WHERE subagent_approval_required = 0;

-- Seed workspace_role_trust: every workspace × every internal built-in agent
-- profile (worker, planner, hint-selector, mux-orchestrator) → trusted.
-- Chat role stays at default 'normal' (default_trust_tier on agent_profiles).
-- Plugin roles untouched (default 'untrusted' from migration 035).
-- INSERT OR IGNORE is idempotent. Existing overrides are preserved.
INSERT OR IGNORE INTO workspace_role_trust (workspace_id, agent_profile_id, trust_tier, promoted_by)
SELECT w.id, ap.id, 'trusted', 'migration_036_dogfood_seed'
  FROM workspaces w
  CROSS JOIN agent_profiles ap
 WHERE ap.kind = 'internal'
   AND ap.slug IN ('worker','planner','hint-selector','mux-orchestrator');

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
