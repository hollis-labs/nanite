-- +goose Up

-- Skill grants for native views (CW-20261024-0089). A definition revision asks
-- for a skill with requirements.skills[{name, content{uri,digest}}]; a grant is
-- the operator's authority for one (host settings revision, definition pin) to
-- receive that skill at exactly that digest. It hangs off the live
-- native_authorities row like a tool grant, so a new host settings revision or
-- definition pin has none until it is reviewed and granted again. As with tool
-- grants the triggers check SHAPE; they are not a trust boundary and a receipt
-- is provenance, never caller authentication. This creates no grants.

CREATE TABLE native_authority_skills (
    authority_id TEXT NOT NULL REFERENCES native_authorities(id),
    skill_name   TEXT NOT NULL CHECK(length(skill_name) BETWEEN 1 AND 64),
    digest       TEXT NOT NULL CHECK(digest GLOB 'sha256:*' AND length(digest)=71),
    receipt      TEXT NOT NULL CHECK(json_valid(receipt)),
    granted_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (authority_id, skill_name)
);

-- +goose StatementBegin
CREATE TRIGGER native_authority_skills_receipt_shape BEFORE INSERT ON native_authority_skills
WHEN json_extract(NEW.receipt,'$.authority') IS NOT 'nanite-local'
  OR length(trim(coalesce(json_extract(NEW.receipt,'$.granted_by'),'')))=0
BEGIN SELECT RAISE(ABORT,'native skill grant receipt must name authority nanite-local and a granted_by'); END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER native_authority_skills_requires_live_authority BEFORE INSERT ON native_authority_skills
WHEN NOT EXISTS (SELECT 1 FROM native_authorities a WHERE a.id=NEW.authority_id AND a.enabled=1 AND a.revoked_at IS NULL)
BEGIN SELECT RAISE(ABORT,'a skill grant requires an enabled, unrevoked native authority'); END;
-- +goose StatementEnd

-- The audit action CHECK lists only authority and tool actions and SQLite
-- cannot alter a CHECK, so rebuild the table with the skill actions and a
-- skill_name column (tool_id stays a tool id). Rows are copied unchanged.
DROP TRIGGER native_authority_audit_no_delete;
DROP TRIGGER native_authority_audit_no_update;
DROP INDEX native_authority_audit_authority;
ALTER TABLE native_authority_audit RENAME TO native_authority_audit_old;

CREATE TABLE native_authority_audit (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    authority_id TEXT NOT NULL,
    action       TEXT NOT NULL CHECK(action IN ('authority_created','authority_revoked','tool_granted','tool_revoked','skill_granted','skill_revoked')),
    tool_id      TEXT NOT NULL DEFAULT '',
    granted_by   TEXT NOT NULL CHECK(length(trim(granted_by))>0),
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    skill_name   TEXT NOT NULL DEFAULT '',
    skill_digest TEXT NOT NULL DEFAULT ''
);
INSERT INTO native_authority_audit(id,authority_id,action,tool_id,granted_by,reason,created_at)
    SELECT id,authority_id,action,tool_id,granted_by,reason,created_at FROM native_authority_audit_old ORDER BY id;
DROP TABLE native_authority_audit_old;
CREATE INDEX native_authority_audit_authority ON native_authority_audit(authority_id, id);

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
DROP INDEX native_authority_audit_authority;
ALTER TABLE native_authority_audit RENAME TO native_authority_audit_new;
CREATE TABLE native_authority_audit (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    authority_id TEXT NOT NULL,
    action       TEXT NOT NULL CHECK(action IN ('authority_created','authority_revoked','tool_granted','tool_revoked')),
    tool_id      TEXT NOT NULL DEFAULT '',
    granted_by   TEXT NOT NULL CHECK(length(trim(granted_by))>0),
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO native_authority_audit(id,authority_id,action,tool_id,granted_by,reason,created_at)
    SELECT id,authority_id,action,tool_id,granted_by,reason,created_at FROM native_authority_audit_new
    WHERE action IN ('authority_created','authority_revoked','tool_granted','tool_revoked') ORDER BY id;
DROP TABLE native_authority_audit_new;
CREATE INDEX native_authority_audit_authority ON native_authority_audit(authority_id, id);
-- +goose StatementBegin
CREATE TRIGGER native_authority_audit_no_update BEFORE UPDATE ON native_authority_audit
BEGIN SELECT RAISE(ABORT,'native authority audit is append-only'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER native_authority_audit_no_delete BEFORE DELETE ON native_authority_audit
BEGIN SELECT RAISE(ABORT,'native authority audit is append-only'); END;
-- +goose StatementEnd
DROP TABLE native_authority_skills;
