-- +goose Up
-- Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
-- third of the four workspaces-retirement migrations. workspace_role_trust
-- (035_role_trust_model.sql, seeded by 036_role_trust_seed_dogfood.sql) is
-- a live trust-tier override mechanism the task's own decision log and
-- TASKS.md item 20 never mentioned — flagged during planning and
-- operator-confirmed 2026-08-18 (final): full removal, not a collapse to
-- a global (non-workspace-scoped) override table.
--
-- internal/store/trust.go's ResolveTrust reverts to unconditional
-- base-tier resolution: agent_profiles.default_trust_tier, falling back
-- to 'normal' when the agent profile itself isn't found. The override
-- layer this table backed (workspace-scoped promote/demote) is gone —
-- internal/subagent/service.go:772 and internal/mcp/self_tools_panels.go
-- call ResolveTrust's simplified (ctx, agentProfileID) signature now.
--
-- DROP TABLE implicitly drops its own indexes (idx_workspace_role_trust_workspace)
-- — no separate DROP INDEX needed.

DROP TABLE IF EXISTS workspace_role_trust;

-- +goose Down
-- Recreates workspace_role_trust with its original shape
-- (035_role_trust_model.sql). Structure only — the override rows
-- themselves are not recoverable (DROP TABLE is inherently lossy), which
-- is the correct behavior here: an override table Downed back into
-- existence with zero rows is honest (every agent falls back to its
-- profile default, which is the same "no override" state a truly empty
-- table represents) rather than fabricating promotions that never
-- happened.

CREATE TABLE IF NOT EXISTS workspace_role_trust (
  workspace_id      TEXT NOT NULL,
  agent_profile_id  TEXT NOT NULL,
  trust_tier        TEXT NOT NULL CHECK (trust_tier IN ('untrusted','normal','trusted')),
  promoted_at       TEXT NOT NULL DEFAULT (datetime('now')),
  promoted_by       TEXT,
  PRIMARY KEY (workspace_id, agent_profile_id),
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  FOREIGN KEY (agent_profile_id) REFERENCES agent_profiles(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workspace_role_trust_workspace
  ON workspace_role_trust(workspace_id);
