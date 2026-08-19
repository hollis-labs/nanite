-- +goose Up
-- Internal todo and plan system (workspace/project/session scoped).

CREATE TABLE IF NOT EXISTS todos (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL CHECK(scope IN ('workspace', 'project', 'session')),
    scope_id TEXT NOT NULL DEFAULT '',
    parent_id TEXT REFERENCES todos(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'in_progress', 'done', 'blocked')),
    priority TEXT NOT NULL DEFAULT 'medium' CHECK(priority IN ('low', 'medium', 'high', 'critical')),
    labels TEXT NOT NULL DEFAULT '[]',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_todos_scope ON todos(scope, scope_id);
CREATE INDEX IF NOT EXISTS idx_todos_parent ON todos(parent_id);
CREATE INDEX IF NOT EXISTS idx_todos_status ON todos(status);

CREATE TABLE IF NOT EXISTS plans (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL CHECK(scope IN ('workspace', 'project', 'session')),
    scope_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed', 'approved', 'in_progress', 'complete', 'abandoned')),
    steps TEXT NOT NULL DEFAULT '[]',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_plans_scope ON plans(scope, scope_id);
CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status);

-- Auto-update updated_at on row modification.
--
-- goose's default SQL parser splits statements on a trailing semicolon per
-- line, which would otherwise chop this trigger's BEGIN...END body into
-- fragments (see internal/pressly/goose's sqlparser). StatementBegin/End
-- brackets the whole CREATE TRIGGER as one atomic statement.
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS trg_todos_updated_at
AFTER UPDATE ON todos
FOR EACH ROW
BEGIN
    UPDATE todos SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS trg_plans_updated_at
AFTER UPDATE ON plans
FOR EACH ROW
BEGIN
    UPDATE plans SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
