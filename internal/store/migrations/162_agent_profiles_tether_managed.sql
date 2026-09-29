-- +goose Up
-- 162_agent_profiles_tether_managed.sql
--
-- tether_managed is the per-agent opt-in for Tether registration: default
-- false, so every existing and future agent is unaffected until someone
-- explicitly flips it. tether_urn holds the URN once something mints one
-- (via tether_registry_register, an ordinary agent tool call -- see
-- docs/adding-an-agent.md, "Materialize, then boot") and is the value
-- tether_identity.go plants into a CLI session's boot dir.
--
-- Deliberately NOT named urn/legacy_urn: those names are retired by
-- migration 159 for the now-superseded profile-URN scheme it replaced with
-- durable_agent_instances.urn. tether_urn is a distinct, new concept (an
-- externally-registered Tether directory entry for the agent PROFILE
-- itself, independent of any durable instance) and gets its own name
-- rather than reoccupying a retired one.

ALTER TABLE agent_profiles ADD COLUMN tether_managed BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE agent_profiles ADD COLUMN tether_urn TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_profiles DROP COLUMN tether_urn;
ALTER TABLE agent_profiles DROP COLUMN tether_managed;
