-- +goose Up
-- START CLEAN: there is deliberately no INSERT SELECT, profile conversion,
-- identity enrollment, grant backfill or deletion of retained historical rows.
CREATE TABLE agent_definitions (
    definition_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    semantic_digest TEXT NOT NULL,
    artifact_digest TEXT NOT NULL,
    artifact BLOB NOT NULL,
    installed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (definition_id, revision),
    UNIQUE (definition_id, revision, semantic_digest)
);
CREATE TABLE agent_definition_resources (
    digest TEXT PRIMARY KEY,
    content BLOB NOT NULL
);
CREATE TABLE agent_definition_resource_refs (
    definition_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    uri TEXT NOT NULL,
    digest TEXT NOT NULL REFERENCES agent_definition_resources(digest),
    PRIMARY KEY (definition_id, revision, uri),
    FOREIGN KEY (definition_id,revision) REFERENCES agent_definitions(definition_id,revision)
);
CREATE TABLE agent_host_settings (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    definition_id TEXT NOT NULL,
    definition_revision TEXT NOT NULL,
    semantic_digest TEXT NOT NULL,
    settings_json TEXT NOT NULL CHECK(json_valid(settings_json)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    source TEXT NOT NULL,
    plugin_id TEXT NOT NULL DEFAULT '',
    revision TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (definition_id,definition_revision,semantic_digest)
      REFERENCES agent_definitions(definition_id,revision,semantic_digest)
);
-- Actor rows can only be supplied by an authenticated host binding port. A host
-- settings UUID is not an actor identity. This migration creates no actor rows.
CREATE TABLE agent_actor_bindings (
    actor_uri TEXT PRIMARY KEY,
    host_settings_id TEXT NOT NULL REFERENCES agent_host_settings(id),
    binding_receipt TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1))
);
CREATE TABLE actor_reflex_state (
    actor_uri TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    bundle_digest TEXT NOT NULL REFERENCES agent_definition_resources(digest),
    rule_id TEXT NOT NULL,
    opt_out INTEGER NOT NULL DEFAULT 0 CHECK(opt_out IN (0,1)),
    fired_count INTEGER NOT NULL DEFAULT 0,
    last_fired_at TEXT,
    PRIMARY KEY(actor_uri,bundle_digest,rule_id)
);
-- Labels and resources are immutable, including exact artifact bytes. A new
-- authored revision is required even when a presentation-only edit is made.
-- +goose StatementBegin
CREATE TRIGGER agent_definitions_immutable BEFORE UPDATE ON agent_definitions
BEGIN SELECT RAISE(ABORT,'immutable definition revision'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER agent_definition_resources_immutable BEFORE UPDATE ON agent_definition_resources
BEGIN SELECT RAISE(ABORT,'immutable definition resource'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER agent_definition_resource_refs_immutable BEFORE UPDATE ON agent_definition_resource_refs
BEGIN SELECT RAISE(ABORT,'immutable definition resource ref'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER agent_definitions_no_rebind BEFORE DELETE ON agent_definitions
BEGIN SELECT RAISE(ABORT,'definition revision labels cannot be rebound'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agent_definition_resources_no_delete BEFORE DELETE ON agent_definition_resources
BEGIN SELECT RAISE(ABORT,'immutable definition resource'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER agent_definition_resource_refs_no_delete BEFORE DELETE ON agent_definition_resource_refs
BEGIN SELECT RAISE(ABORT,'immutable definition resource ref'); END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER agent_definition_resource_refs_no_delete;
DROP TRIGGER agent_definition_resources_no_delete;
DROP TRIGGER agent_definitions_no_rebind;
DROP TRIGGER agent_definition_resource_refs_immutable;
DROP TRIGGER agent_definition_resources_immutable;
DROP TRIGGER agent_definitions_immutable;
DROP TABLE actor_reflex_state;
DROP TABLE agent_actor_bindings;
DROP TABLE agent_host_settings;
DROP TABLE agent_definition_resource_refs;
DROP TABLE agent_definition_resources;
DROP TABLE agent_definitions;
