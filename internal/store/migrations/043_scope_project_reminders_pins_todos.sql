-- D1 (CW-20260428-0014): Scope enum (turn|session|project) on
-- reminders + pinned_content + todos. Adds project-level continuity so
-- reminders/pins/todos created in one session can surface in any
-- subsequent session of the same project.
--
-- Clean break per project policy (no users, no production data):
--   * pinned_content: replace legacy `cross_session` value with `project`
--     by deleting any pre-existing `cross_session` rows. Per project
--     memory feedback_no_compat_shims, no migration of those rows.
--   * todos: drop the legacy `workspace` enum value (workspace scope is
--     out of scope for this dimension) and add `turn`.
--
-- IMPORTANT: no semicolons inside comments (splitSQL naive-split limitation).
--
-- New scope semantics for the three tables:
--   turn     - lifetime is the current turn (ephemeral semantics, but
--              reminders and todos still persist to DB. Pins skip
--              persistence for turn scope per existing engine contract).
--   session  - lifetime is the originating session (default).
--   project  - lifetime is the project. Surfaces in any session of the
--              same project. Requires project_id to be set.

-- ─── reminders ──────────────────────────────────────────────────────────
ALTER TABLE reminders ADD COLUMN scope TEXT NOT NULL DEFAULT 'session';
ALTER TABLE reminders ADD COLUMN project_id TEXT;

CREATE INDEX IF NOT EXISTS idx_reminders_project
    ON reminders (project_id, fired_at);

CREATE INDEX IF NOT EXISTS idx_reminders_scope
    ON reminders (scope, fired_at);

-- ─── pinned_content ─────────────────────────────────────────────────────
-- Drop any pre-existing cross_session rows cleanly before introducing the
-- replacement project tier. The pins tab shows zero rows for that scope
-- on a fresh DB. In-flight rows would have no users to migrate per the
-- clean-break policy.
DELETE FROM pinned_content WHERE scope = 'cross_session';

ALTER TABLE pinned_content ADD COLUMN project_id TEXT;

CREATE INDEX IF NOT EXISTS idx_pinned_content_project
    ON pinned_content (project_id, scope, created_at DESC);

-- ─── todos ──────────────────────────────────────────────────────────────
-- Recreate todos to swap the scope CHECK enum cleanly (drop `workspace`,
-- add `turn`) and add `project_id`. SQLite cannot alter CHECK constraints
-- in-place, so we use rename + recreate + copy.
--
-- Idempotency: the migration runner re-executes every boot (no
-- schema_migrations table). Each statement here must be safe on rerun.
--   * RENAME — second-boot failure ("already another table") is swallowed
--     by the runner (see store.go), so the live `todos` keeps its name.
--   * CREATE TABLE IF NOT EXISTS — no-op when the new schema exists.
--   * INSERT OR IGNORE — PK collisions on rerun skip silently, so rows
--     created at runtime in the new `todos` are NOT touched.
--   * todos_legacy_d1 is intentionally NOT dropped, so subsequent runs
--     find it for the rename-swallow + INSERT-OR-IGNORE no-op path.
--     The legacy table is a small fixed cost preserved only to make
--     this migration safely re-runnable.
--
-- IMPORTANT: no semicolons in any of these comments. splitSQL is naive
-- and splits on the literal character regardless of comment context.
ALTER TABLE todos RENAME TO todos_legacy_d1;

CREATE TABLE IF NOT EXISTS todos (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL DEFAULT 'session' CHECK(scope IN ('turn', 'session', 'project')),
    scope_id TEXT NOT NULL DEFAULT '',
    project_id TEXT,
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

-- Copy rows where the legacy scope is one of the values still allowed.
-- Workspace-scoped rows are dropped per the clean-break policy. Nanite
-- doesn't have any production users at workspace scope today.
-- OR IGNORE makes this safe on rerun: rows already in `todos` (including
-- any `turn`-scoped rows added at runtime) are skipped on PK collision.
INSERT OR IGNORE INTO todos (id, scope, scope_id, project_id, parent_id, title, description,
                             status, priority, labels, metadata, created_by, created_at, updated_at)
SELECT id, scope, scope_id,
       CASE WHEN scope = 'project' THEN scope_id ELSE NULL END,
       parent_id, title, description, status, priority, labels, metadata,
       created_by, created_at, updated_at
FROM todos_legacy_d1
WHERE scope IN ('session', 'project');

CREATE INDEX IF NOT EXISTS idx_todos_scope ON todos(scope, scope_id);
CREATE INDEX IF NOT EXISTS idx_todos_parent ON todos(parent_id);
CREATE INDEX IF NOT EXISTS idx_todos_status ON todos(status);
CREATE INDEX IF NOT EXISTS idx_todos_project ON todos(project_id);

-- Recreate the updated_at trigger that 003 attached to the original todos
-- table. The CREATE TRIGGER from 003 was a no-op against the renamed table.
CREATE TRIGGER IF NOT EXISTS trg_todos_updated_at
AFTER UPDATE ON todos
FOR EACH ROW
BEGIN
    UPDATE todos SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
END;
