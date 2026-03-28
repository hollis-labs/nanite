-- Agent Schema v2: framework-agnostic agent definitions with plugin extensibility.
ALTER TABLE agent_profiles ADD COLUMN agent_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_profiles ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE agent_profiles ADD COLUMN tools TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agent_profiles ADD COLUMN directories TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agent_profiles ADD COLUMN constraints TEXT NOT NULL DEFAULT '{}';
ALTER TABLE agent_profiles ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agent_profiles ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE agent_profiles ADD COLUMN source TEXT NOT NULL DEFAULT 'seed';
ALTER TABLE agent_profiles ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';
