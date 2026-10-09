-- +goose Up
-- Each row mutation and its history commit or roll back together, including
-- direct store/import/registry SQL. Grants and child capability tables are
-- intentionally outside the profile snapshot and partial restore scope.
ALTER TABLE agent_profiles ADD COLUMN revision TEXT NOT NULL DEFAULT '';
CREATE TABLE agent_profile_revisions (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL,
    operation TEXT NOT NULL CHECK (operation IN ('baseline','create','update','restore_partial')),
    restored_from TEXT NOT NULL DEFAULT '',
    profile_json TEXT NOT NULL CHECK (json_valid(profile_json)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX agent_profile_revisions_agent_sequence ON agent_profile_revisions(agent_id,sequence DESC);

-- Existing rows acquire a pre-edit baseline on their first mutation, rather
-- than fabricating a migration-time application-data snapshot.
-- +goose StatementBegin
CREATE TRIGGER agent_profile_revision_baseline BEFORE UPDATE ON agent_profiles
WHEN OLD.revision = '' AND NEW.revision = OLD.revision
BEGIN
    INSERT INTO agent_profile_revisions(id,agent_id,operation,profile_json)
    SELECT lower(hex(randomblob(16))),OLD.id,'baseline',json_object(
        'id', COALESCE(OLD.id,''),
        'name', COALESCE(OLD.name,''),
        'slug', COALESCE(OLD.slug,''),
        'avatar', COALESCE(OLD.avatar,''),
        'system_prompt', COALESCE(OLD.system_prompt,''),
        'description', COALESCE(OLD.description,''),
        'modes', COALESCE(OLD.modes,''),
        'default_model', COALESCE(OLD.default_model,''),
        'default_provider', COALESCE(OLD.default_provider,''),
        'mcp_servers', COALESCE(OLD.mcp_servers,''),
        'tool_permissions', COALESCE(OLD.tool_permissions,''),
        'can_execute', json(CASE WHEN OLD.can_execute THEN 'true' ELSE 'false' END),
        'settings', COALESCE(OLD.settings,''),
        'created_at', COALESCE(OLD.created_at,''),
        'updated_at', COALESCE(OLD.updated_at,''),
        'agent_hash', COALESCE(OLD.agent_hash,''),
        'version', COALESCE(OLD.version,0),
        'tools', COALESCE(OLD.tools,''),
        'directories', COALESCE(OLD.directories,''),
        'constraints', COALESCE(OLD.constraints,''),
        'tags', COALESCE(OLD.tags,''),
        'status', COALESCE(OLD.status,''),
        'source', COALESCE(OLD.source,''),
        'source_ref', COALESCE(OLD.source_ref,''),
        'icon', COALESCE(OLD.icon,''),
        'kind', COALESCE(OLD.kind,''),
        'capabilities_json', COALESCE(OLD.capabilities_json,''),
        'limits_json', COALESCE(OLD.limits_json,''),
        'model_strategy', COALESCE(OLD.model_strategy,''),
        'imported_at', COALESCE(OLD.imported_at,''),
        'origin_system', COALESCE(OLD.origin_system,''),
        'format', COALESCE(OLD.format,''),
        'parent_dispatch_allowlist', COALESCE(OLD.parent_dispatch_allowlist,''),
        'role_tools', COALESCE(OLD.role_tools,''),
        'role_skills', COALESCE(OLD.role_skills,''),
        'context_policy', COALESCE(OLD.context_policy,''),
        'durable', json(CASE WHEN OLD.durable THEN 'true' ELSE 'false' END),
        'activation_mode', COALESCE(OLD.activation_mode,''),
        'class', COALESCE(OLD.class,''),
        'default_state', COALESCE(OLD.default_state,''),
        'consumer_id', COALESCE(OLD.consumer_id,''),
        'role_id', COALESCE(OLD.role_id,''),
        'model_id', COALESCE(OLD.model_id,''),
        'runtime_kind', COALESCE(OLD.runtime_kind,''),
        'protocol', COALESCE(OLD.protocol,''),
        'transport', COALESCE(OLD.transport,''),
        'plugin_id', COALESCE(OLD.plugin_id,''),
        'tether_managed', json(CASE WHEN OLD.tether_managed THEN 'true' ELSE 'false' END),
        'tether_urn', COALESCE(OLD.tether_urn,'')
    )
    WHERE NOT EXISTS (SELECT 1 FROM agent_profile_revisions WHERE agent_id = OLD.id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_profile_revision_insert AFTER INSERT ON agent_profiles
BEGIN
    UPDATE agent_profiles SET revision = lower(hex(randomblob(16))) WHERE id = NEW.id;
    INSERT INTO agent_profile_revisions(id,agent_id,operation,profile_json)
    SELECT revision,NEW.id,'create',json_object(
        'id', COALESCE(NEW.id,''),
        'name', COALESCE(NEW.name,''),
        'slug', COALESCE(NEW.slug,''),
        'avatar', COALESCE(NEW.avatar,''),
        'system_prompt', COALESCE(NEW.system_prompt,''),
        'description', COALESCE(NEW.description,''),
        'modes', COALESCE(NEW.modes,''),
        'default_model', COALESCE(NEW.default_model,''),
        'default_provider', COALESCE(NEW.default_provider,''),
        'mcp_servers', COALESCE(NEW.mcp_servers,''),
        'tool_permissions', COALESCE(NEW.tool_permissions,''),
        'can_execute', json(CASE WHEN NEW.can_execute THEN 'true' ELSE 'false' END),
        'settings', COALESCE(NEW.settings,''),
        'created_at', COALESCE(NEW.created_at,''),
        'updated_at', COALESCE(NEW.updated_at,''),
        'agent_hash', COALESCE(NEW.agent_hash,''),
        'version', COALESCE(NEW.version,0),
        'tools', COALESCE(NEW.tools,''),
        'directories', COALESCE(NEW.directories,''),
        'constraints', COALESCE(NEW.constraints,''),
        'tags', COALESCE(NEW.tags,''),
        'status', COALESCE(NEW.status,''),
        'source', COALESCE(NEW.source,''),
        'source_ref', COALESCE(NEW.source_ref,''),
        'icon', COALESCE(NEW.icon,''),
        'kind', COALESCE(NEW.kind,''),
        'capabilities_json', COALESCE(NEW.capabilities_json,''),
        'limits_json', COALESCE(NEW.limits_json,''),
        'model_strategy', COALESCE(NEW.model_strategy,''),
        'imported_at', COALESCE(NEW.imported_at,''),
        'origin_system', COALESCE(NEW.origin_system,''),
        'format', COALESCE(NEW.format,''),
        'parent_dispatch_allowlist', COALESCE(NEW.parent_dispatch_allowlist,''),
        'role_tools', COALESCE(NEW.role_tools,''),
        'role_skills', COALESCE(NEW.role_skills,''),
        'context_policy', COALESCE(NEW.context_policy,''),
        'durable', json(CASE WHEN NEW.durable THEN 'true' ELSE 'false' END),
        'activation_mode', COALESCE(NEW.activation_mode,''),
        'class', COALESCE(NEW.class,''),
        'default_state', COALESCE(NEW.default_state,''),
        'consumer_id', COALESCE(NEW.consumer_id,''),
        'role_id', COALESCE(NEW.role_id,''),
        'model_id', COALESCE(NEW.model_id,''),
        'runtime_kind', COALESCE(NEW.runtime_kind,''),
        'protocol', COALESCE(NEW.protocol,''),
        'transport', COALESCE(NEW.transport,''),
        'plugin_id', COALESCE(NEW.plugin_id,''),
        'tether_managed', json(CASE WHEN NEW.tether_managed THEN 'true' ELSE 'false' END),
        'tether_urn', COALESCE(NEW.tether_urn,'')
    ) FROM agent_profiles WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- Token-only writes from these triggers must not recursively record history.
-- +goose StatementBegin
CREATE TRIGGER agent_profile_revision_update AFTER UPDATE ON agent_profiles
WHEN NEW.revision = OLD.revision
BEGIN
    UPDATE agent_profiles SET revision = lower(hex(randomblob(16))) WHERE id = NEW.id;
    INSERT INTO agent_profile_revisions(id,agent_id,operation,profile_json)
    SELECT revision,NEW.id,'update',json_object(
        'id', COALESCE(NEW.id,''),
        'name', COALESCE(NEW.name,''),
        'slug', COALESCE(NEW.slug,''),
        'avatar', COALESCE(NEW.avatar,''),
        'system_prompt', COALESCE(NEW.system_prompt,''),
        'description', COALESCE(NEW.description,''),
        'modes', COALESCE(NEW.modes,''),
        'default_model', COALESCE(NEW.default_model,''),
        'default_provider', COALESCE(NEW.default_provider,''),
        'mcp_servers', COALESCE(NEW.mcp_servers,''),
        'tool_permissions', COALESCE(NEW.tool_permissions,''),
        'can_execute', json(CASE WHEN NEW.can_execute THEN 'true' ELSE 'false' END),
        'settings', COALESCE(NEW.settings,''),
        'created_at', COALESCE(NEW.created_at,''),
        'updated_at', COALESCE(NEW.updated_at,''),
        'agent_hash', COALESCE(NEW.agent_hash,''),
        'version', COALESCE(NEW.version,0),
        'tools', COALESCE(NEW.tools,''),
        'directories', COALESCE(NEW.directories,''),
        'constraints', COALESCE(NEW.constraints,''),
        'tags', COALESCE(NEW.tags,''),
        'status', COALESCE(NEW.status,''),
        'source', COALESCE(NEW.source,''),
        'source_ref', COALESCE(NEW.source_ref,''),
        'icon', COALESCE(NEW.icon,''),
        'kind', COALESCE(NEW.kind,''),
        'capabilities_json', COALESCE(NEW.capabilities_json,''),
        'limits_json', COALESCE(NEW.limits_json,''),
        'model_strategy', COALESCE(NEW.model_strategy,''),
        'imported_at', COALESCE(NEW.imported_at,''),
        'origin_system', COALESCE(NEW.origin_system,''),
        'format', COALESCE(NEW.format,''),
        'parent_dispatch_allowlist', COALESCE(NEW.parent_dispatch_allowlist,''),
        'role_tools', COALESCE(NEW.role_tools,''),
        'role_skills', COALESCE(NEW.role_skills,''),
        'context_policy', COALESCE(NEW.context_policy,''),
        'durable', json(CASE WHEN NEW.durable THEN 'true' ELSE 'false' END),
        'activation_mode', COALESCE(NEW.activation_mode,''),
        'class', COALESCE(NEW.class,''),
        'default_state', COALESCE(NEW.default_state,''),
        'consumer_id', COALESCE(NEW.consumer_id,''),
        'role_id', COALESCE(NEW.role_id,''),
        'model_id', COALESCE(NEW.model_id,''),
        'runtime_kind', COALESCE(NEW.runtime_kind,''),
        'protocol', COALESCE(NEW.protocol,''),
        'transport', COALESCE(NEW.transport,''),
        'plugin_id', COALESCE(NEW.plugin_id,''),
        'tether_managed', json(CASE WHEN NEW.tether_managed THEN 'true' ELSE 'false' END),
        'tether_urn', COALESCE(NEW.tether_urn,'')
    ) FROM agent_profiles WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER agent_profile_revision_update;
DROP TRIGGER agent_profile_revision_insert;
DROP TRIGGER agent_profile_revision_baseline;
DROP TABLE agent_profile_revisions;
ALTER TABLE agent_profiles DROP COLUMN revision;
