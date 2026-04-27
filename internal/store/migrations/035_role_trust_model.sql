-- H1 (CW-20260421-0014): role trust model — 3-tier, workspace-scoped.
-- Adds default_trust_tier to agent_profiles. Adds workspace_role_trust
-- override table.
--
-- Migration runner already swallows "duplicate column" errors for ADD COLUMN,
-- so this file is safe to re-run on every boot (no schema_migrations table).

-- Add default_trust_tier to agent_profiles.
-- Built-in (kind='internal') defaults 'normal'.
-- External/plugin kinds default 'untrusted'.
ALTER TABLE agent_profiles
  ADD COLUMN default_trust_tier TEXT NOT NULL DEFAULT 'normal'
    CHECK (default_trust_tier IN ('untrusted','normal','trusted'));

-- Repair existing rows: external/cli kinds get 'untrusted' default.
-- WHERE guard makes this idempotent.
UPDATE agent_profiles
   SET default_trust_tier = 'untrusted'
 WHERE kind IN ('external','cli')
   AND default_trust_tier = 'normal';

-- Per-workspace overrides. PK keeps lookups O(1).
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
