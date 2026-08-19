-- Phase 5 item 02 (TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md):
-- a real, DB-backed installed/enabled state model for plugins -- explicitly
-- modeled on WordPress's plugin state (code present = installed, a separate
-- flag = active/enabled), working uniformly for both builtin (compiled-in)
-- and subprocess plugins.
--
-- Replaces the old file-rename mechanism (plugin.yaml <-> plugin.yaml.disabled
-- inside pluginsDir), which was subprocess-only in practice and, worse, did
-- not actually stop a "disabled" builtin from registering: LoadRegisteredBuiltins
-- (internal/plugin/loader.go) loads every constructor in the compiled-in
-- registry that isn't already loaded, independent of any on-disk manifest --
-- so a builtin whose (rare, manually-installed) pluginsDir copy of its
-- plugin.yaml got renamed away was still fully loaded and registered via that
-- fallback path. See TASKS/phase-5/02's Work Log for the full trace of this
-- confirmed bug.
--
-- plugin_id is the canonical plugin identifier -- manifest.Identifier() /
-- p.ID() -- the same key applyManifestRegistrations, host.plugins, and the
-- compiled-in registry (RegisterPlugin/LookupConstructor) already use. It is
-- deliberately NOT the on-disk directory name under pluginsDir, which can
-- differ from the canonical id (e.g. dir "bookmarks" vs id "bookmarks-widget").
--
-- installed tracks "code/package is present" (mirrors WordPress's installed
-- state); enabled is the separate active/inactive flag. A builtin's code is
-- always "installed" the moment it's compiled in, so installed is really only
-- meaningful for subprocess plugins today -- carried as a real column anyway
-- so the model doesn't need a second migration when uninstall-without-disable
-- semantics matter later.
--
-- No row for a given plugin_id is treated as "installed+enabled" by the
-- reading code (internal/plugin/manage.go, internal/plugin/loader.go) --
-- fail-open to today's default-on behavior for any plugin never explicitly
-- toggled, rather than requiring every future builtin to get its own
-- migration just to default to on.

-- +goose Up
CREATE TABLE IF NOT EXISTS plugins (
    plugin_id  TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('builtin', 'subprocess')),
    installed  INTEGER NOT NULL DEFAULT 1,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- +goose Down
DROP TABLE IF EXISTS plugins;
