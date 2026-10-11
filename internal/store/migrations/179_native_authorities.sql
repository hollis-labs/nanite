-- +goose Up

-- Local authority for native views. A native view is hosted by Nanite and is not
-- a fabric actor: nothing here mints an actor URI or touches
-- agent_actor_bindings. Rows are written only by operator-authenticated Nanite
-- writers (store.LocalAuthority); the triggers below check SHAPE, they are not a
-- trust boundary, and a receipt is provenance, never caller authentication.
-- This migration creates no authority rows and no grants.

CREATE TABLE native_authorities (
    id                     TEXT PRIMARY KEY,
    host_settings_id       TEXT NOT NULL REFERENCES agent_host_settings(id),
    host_settings_revision TEXT NOT NULL,
    definition_id          TEXT NOT NULL,
    definition_revision    TEXT NOT NULL,
    semantic_digest        TEXT NOT NULL,
    enabled                INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    receipt                TEXT NOT NULL CHECK(json_valid(receipt)),
    created_at             TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    revoked_at             TEXT,
    FOREIGN KEY (definition_id,definition_revision,semantic_digest)
      REFERENCES agent_definitions(definition_id,revision,semantic_digest)
);

-- One live authority per reviewed (host settings revision, definition pin).
CREATE UNIQUE INDEX native_authorities_one_live ON native_authorities(
    host_settings_id, host_settings_revision, definition_id, definition_revision, semantic_digest
) WHERE revoked_at IS NULL;

CREATE TABLE native_authority_tools (
    authority_id TEXT NOT NULL REFERENCES native_authorities(id),
    tool_id      TEXT NOT NULL REFERENCES known_tools(id),
    receipt      TEXT NOT NULL CHECK(json_valid(receipt)),
    granted_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (authority_id, tool_id)
);

-- Append-only record of every change, committed in the same transaction as the
-- change itself. authority_id is deliberately not a foreign key so the trail
-- outlives anything it describes.
CREATE TABLE native_authority_audit (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    authority_id TEXT NOT NULL,
    action       TEXT NOT NULL CHECK(action IN ('authority_created','authority_revoked','tool_granted','tool_revoked')),
    tool_id      TEXT NOT NULL DEFAULT '',
    granted_by   TEXT NOT NULL CHECK(length(trim(granted_by))>0),
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX native_authority_audit_authority ON native_authority_audit(authority_id, id);

-- +goose StatementBegin
CREATE TRIGGER native_authorities_receipt_shape BEFORE INSERT ON native_authorities
WHEN json_extract(NEW.receipt,'$.authority') IS NOT 'nanite-local'
  OR length(trim(coalesce(json_extract(NEW.receipt,'$.granted_by'),'')))=0
BEGIN SELECT RAISE(ABORT,'native authority receipt must name authority nanite-local and a granted_by'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER native_authority_tools_receipt_shape BEFORE INSERT ON native_authority_tools
WHEN json_extract(NEW.receipt,'$.authority') IS NOT 'nanite-local'
  OR length(trim(coalesce(json_extract(NEW.receipt,'$.granted_by'),'')))=0
BEGIN SELECT RAISE(ABORT,'native tool grant receipt must name authority nanite-local and a granted_by'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER native_authority_tools_requires_live_authority BEFORE INSERT ON native_authority_tools
WHEN NOT EXISTS (SELECT 1 FROM native_authorities a WHERE a.id=NEW.authority_id AND a.enabled=1 AND a.revoked_at IS NULL)
BEGIN SELECT RAISE(ABORT,'a tool grant requires an enabled, unrevoked native authority'); END;
-- +goose StatementEnd

-- A revoked authority never comes back; a new review creates a new row.
-- +goose StatementBegin
CREATE TRIGGER native_authorities_revoked_is_final BEFORE UPDATE ON native_authorities
WHEN OLD.revoked_at IS NOT NULL
BEGIN SELECT RAISE(ABORT,'a revoked native authority is final'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER native_authorities_pin_immutable BEFORE UPDATE ON native_authorities
WHEN NEW.host_settings_id IS NOT OLD.host_settings_id
  OR NEW.host_settings_revision IS NOT OLD.host_settings_revision
  OR NEW.definition_id IS NOT OLD.definition_id
  OR NEW.definition_revision IS NOT OLD.definition_revision
  OR NEW.semantic_digest IS NOT OLD.semantic_digest
  OR NEW.receipt IS NOT OLD.receipt
BEGIN SELECT RAISE(ABORT,'a native authority''s reviewed pin and receipt are immutable'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER native_authority_audit_no_update BEFORE UPDATE ON native_authority_audit
BEGIN SELECT RAISE(ABORT,'native authority audit is append-only'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER native_authority_audit_no_delete BEFORE DELETE ON native_authority_audit
BEGIN SELECT RAISE(ABORT,'native authority audit is append-only'); END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER native_authority_audit_no_delete;
DROP TRIGGER native_authority_audit_no_update;
DROP TRIGGER native_authorities_pin_immutable;
DROP TRIGGER native_authorities_revoked_is_final;
DROP TRIGGER native_authority_tools_requires_live_authority;
DROP TRIGGER native_authority_tools_receipt_shape;
DROP TRIGGER native_authorities_receipt_shape;
DROP TABLE native_authority_audit;
DROP TABLE native_authority_tools;
DROP INDEX native_authorities_one_live;
DROP TABLE native_authorities;
