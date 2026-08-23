package plugin

// Phase 5 item 02 (TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md)
// -- DisablePlugin/EnablePlugin/IsDisabled/PluginStatus used to be a pure
// filesystem operation: rename plugin.yaml <-> plugin.yaml.disabled inside
// pluginsDir. That mechanism is now fully retired. State lives in the DB
// (the `plugins` table, internal/store/plugins.go) and is read by
// applyManifestRegistrations's callers (internal/plugin/loader.go) to decide
// whether to load a plugin at all -- see loader.go's doc comments for why the
// gate lives there rather than only inside applyManifestRegistrations.
//
// Confirmed bug this replaces (see TASKS/phase-5/02's Work Log for the full
// trace): the file-rename mechanism operated on pluginsDir (the runtime
// ./plugins/ directory, gitignored and normally EMPTY of the 12 compiled-in
// builtins -- their real plugin.yaml files live under
// internal/plugin/builtin/*/plugin.yaml, embedded into the binary, not
// copied into pluginsDir). So for the default deployment, "disabling" a
// builtin via the old DisablePlugin(pluginsDir, name) call couldn't even
// find a manifest to rename -- it errored "not installed" outright. Worse,
// in the one scenario where a builtin's manifest DOES get copied into
// pluginsDir (an operator running `install-local` against a builtin's own
// source dir), renaming that copy away did NOT stop the builtin from
// loading: LoadRegisteredBuiltins loads every compiled-in constructor not
// already present in host.plugins, completely independent of pluginsDir's
// contents. A disabled-via-file-rename builtin skipped by DiscoverPlugins
// (no plugin.yaml to find) was then loaded anyway by LoadRegisteredBuiltins's
// "load any registered builtin not yet loaded" fallback. That fallback is
// exactly what loader.go's new DB-backed gate closes.
//
// Function signatures are unchanged (DisablePlugin/EnablePlugin/IsDisabled/
// PluginStatus still take (pluginsDir, name string)) so the existing GUI/CLI/
// API call sites (internal/api/plugins.go, cmd/nanite/plugin_cmd.go) don't
// need to change beyond this implementation swap -- neither of those files
// is touched by this task.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/store"
)

var (
	pluginStateStoreMu sync.RWMutex
	pluginStateStore   *store.Store
)

// SetPluginStateStore installs the shared *store.Store used by DisablePlugin/
// EnablePlugin/IsDisabled/PluginStatus for their DB-backed installed/enabled
// reads and writes. The running server (cmd/nanite's `serve` command) calls
// this once at startup, right alongside plugin.Host's own SetStore, so these
// package-level functions and the server's plugin host read/write the exact
// same store instance instead of opening a second connection to the same
// database file.
//
// CLI-only invocations (`nanite plugin disable/enable/list`, which run with
// no server process and therefore never call this) fall back to lazily
// opening and caching their own store connection against the
// default-resolved DB path the first time one of the functions in this file
// needs it -- see pluginStateDB.
func SetPluginStateStore(s *store.Store) {
	pluginStateStoreMu.Lock()
	pluginStateStore = s
	pluginStateStoreMu.Unlock()
}

// pluginStateDB returns the store to use for plugin state reads/writes. If
// the server process already called SetPluginStateStore, that instance is
// reused. Otherwise (a standalone CLI invocation) a connection is opened
// against the default-resolved DB path (the same go-apppaths resolution
// cmd/nanite's own resolveDBPathWith("") performs with no --db flag) and
// cached for the remainder of the process.
func pluginStateDB() (*store.Store, error) {
	pluginStateStoreMu.RLock()
	s := pluginStateStore
	pluginStateStoreMu.RUnlock()
	if s != nil {
		return s, nil
	}

	pluginStateStoreMu.Lock()
	defer pluginStateStoreMu.Unlock()
	if pluginStateStore != nil {
		return pluginStateStore, nil
	}
	layout, err := config.ResolveLayout()
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	opened, err := store.New(context.Background(), layout.MainDB())
	if err != nil {
		return nil, fmt.Errorf("open plugin state store: %w", err)
	}
	pluginStateStore = opened
	return opened, nil
}

// pluginIdentity is the canonical (slug, kind) pair resolved from a
// caller-supplied pluginsDir directory name (or, for a compiled-in builtin
// with no on-disk copy under pluginsDir, the builtin's own canonical id --
// see resolvePluginIdentity).
type pluginIdentity struct {
	// Slug is the canonical plugin identifier -- manifest.Identifier() /
	// p.ID() -- the same key applyManifestRegistrations, host.plugins, and
	// the compiled-in registry (RegisterPlugin/LookupConstructor) use. NOT
	// necessarily equal to the caller-supplied pluginsDir directory name
	// (e.g. dir "bookmarks" vs id "bookmarks-widget").
	Slug string
	Kind string // store.PluginKindBuiltin | store.PluginKindSubprocess
}

// resolvePluginIdentity resolves name to its canonical (slug, kind) pair.
// Two paths:
//
//  1. name matches a pluginsDir/<name> directory with a plugin.yaml (or a
//     legacy plugin.yaml.disabled left by the now-retired file-rename
//     mechanism -- restored to plugin.yaml in place, since presence/absence
//     of the file no longer carries any meaning). Kind and Slug are read
//     from the parsed manifest.
//  2. No on-disk directory -- the default case for the 12 compiled-in
//     builtins, which normally have no copy under pluginsDir at all (their
//     real plugin.yaml lives under internal/plugin/builtin/*/plugin.yaml,
//     embedded into the binary). If name matches a registered constructor
//     directly, name itself is the canonical slug, kind "builtin".
//
// migratedFromDisabled reports whether a legacy plugin.yaml.disabled was
// found and restored -- callers use this to seed the DB row as disabled (not
// the default enabled) so the plugin's pre-migration disabled state survives
// the transition to the DB-backed model instead of silently reverting to
// enabled.
func resolvePluginIdentity(pluginsDir, name string) (id pluginIdentity, migratedFromDisabled bool, err error) {
	// This is the common sink for API and CLI enable/disable/status calls.
	// Validate before any path construction or legacy-manifest rename so a
	// caller can never use a traversal, nested path, or absolute path to make
	// plugin management mutate outside pluginsDir.
	if err := ValidatePluginID(name); err != nil {
		return pluginIdentity{}, false, fmt.Errorf("invalid plugin name %q: %w", name, err)
	}
	dir := filepath.Join(pluginsDir, name)
	active := filepath.Join(dir, "plugin.yaml")
	legacyDisabled := filepath.Join(dir, "plugin.yaml.disabled")

	manifestPath := ""
	if _, statErr := os.Stat(active); statErr == nil {
		manifestPath = active
	} else if _, statErr := os.Stat(legacyDisabled); statErr == nil {
		if renameErr := os.Rename(legacyDisabled, active); renameErr != nil {
			return pluginIdentity{}, false, fmt.Errorf("migrate legacy disabled manifest for %q: %w", name, renameErr)
		}
		manifestPath = active
		migratedFromDisabled = true
	}

	if manifestPath != "" {
		manifest, parseErr := ParseManifest(manifestPath)
		if parseErr != nil {
			return pluginIdentity{}, false, fmt.Errorf("parse manifest for %q: %w", name, parseErr)
		}
		kind := store.PluginKindBuiltin
		if manifest.Runtime == "subprocess" {
			kind = store.PluginKindSubprocess
		}
		return pluginIdentity{Slug: manifest.Identifier(), Kind: kind}, migratedFromDisabled, nil
	}

	if _, ok := LookupConstructor(name); ok {
		return pluginIdentity{Slug: name, Kind: store.PluginKindBuiltin}, false, nil
	}

	return pluginIdentity{}, false, fmt.Errorf("plugin %q is not installed (no plugin.yaml found and no compiled-in constructor)", name)
}

// DisablePlugin marks a plugin (builtin or subprocess) disabled in the DB.
// See loader.go for how the enabled flag actually stops registration wiring
// on the next reload/restart.
func DisablePlugin(pluginsDir, name string) error {
	id, migrated, err := resolvePluginIdentity(pluginsDir, name)
	if err != nil {
		return fmt.Errorf("disable plugin %q: %w", name, err)
	}
	db, err := pluginStateDB()
	if err != nil {
		return fmt.Errorf("disable plugin %q: %w", name, err)
	}
	ctx := context.Background()
	if migrated {
		if err := db.EnsurePluginSeeded(ctx, id.Slug, id.Kind, false); err != nil {
			return fmt.Errorf("disable plugin %q: %w", name, err)
		}
	}
	enabled, hasRow, err := db.IsPluginEnabled(ctx, id.Slug)
	if err != nil {
		return fmt.Errorf("disable plugin %q: %w", name, err)
	}
	if hasRow && !enabled {
		return fmt.Errorf("plugin %q is already disabled", name)
	}
	if err := db.SetPluginEnabled(ctx, id.Slug, id.Kind, false); err != nil {
		return fmt.Errorf("disable plugin %q: %w", name, err)
	}
	return nil
}

// EnablePlugin marks a previously disabled plugin (builtin or subprocess)
// enabled again in the DB.
func EnablePlugin(pluginsDir, name string) error {
	id, migrated, err := resolvePluginIdentity(pluginsDir, name)
	if err != nil {
		return fmt.Errorf("enable plugin %q: %w", name, err)
	}
	db, err := pluginStateDB()
	if err != nil {
		return fmt.Errorf("enable plugin %q: %w", name, err)
	}
	ctx := context.Background()
	if migrated {
		// Seed as disabled first so the pre-migration state is what
		// "already enabled?" gets checked against below, not silently
		// skipped.
		if err := db.EnsurePluginSeeded(ctx, id.Slug, id.Kind, false); err != nil {
			return fmt.Errorf("enable plugin %q: %w", name, err)
		}
	}
	enabled, hasRow, err := db.IsPluginEnabled(ctx, id.Slug)
	if err != nil {
		return fmt.Errorf("enable plugin %q: %w", name, err)
	}
	if hasRow && enabled {
		return fmt.Errorf("plugin %q is already enabled", name)
	}
	if err := db.SetPluginEnabled(ctx, id.Slug, id.Kind, true); err != nil {
		return fmt.Errorf("enable plugin %q: %w", name, err)
	}
	return nil
}

// IsDisabled returns true if the plugin is marked disabled in the DB.
// Returns false (not disabled) if the plugin can't be resolved at all or the
// state store is unavailable -- fail open rather than report a nonexistent
// plugin as "disabled".
func IsDisabled(pluginsDir, name string) bool {
	id, migrated, err := resolvePluginIdentity(pluginsDir, name)
	if err != nil {
		return false
	}
	db, err := pluginStateDB()
	if err != nil {
		return false
	}
	ctx := context.Background()
	if migrated {
		_ = db.EnsurePluginSeeded(ctx, id.Slug, id.Kind, false)
	}
	enabled, _, err := db.IsPluginEnabled(ctx, id.Slug)
	if err != nil {
		return false
	}
	return !enabled
}

// PluginStatus determines the runtime status of an installed plugin: one of
// "not-installed", "disabled", "no-binary", or "active". Same vocabulary as
// the old file-rename implementation so existing callers' string switches
// keep working unchanged.
func PluginStatus(pluginsDir, name string) string {
	id, migrated, err := resolvePluginIdentity(pluginsDir, name)
	if err != nil {
		return "not-installed"
	}
	db, err := pluginStateDB()
	if err != nil {
		// State store unavailable -- fail open to "active" rather than
		// misreport a plugin that clearly resolved above as not installed.
		return "active"
	}
	ctx := context.Background()
	if migrated {
		_ = db.EnsurePluginSeeded(ctx, id.Slug, id.Kind, false)
	}
	enabled, _, err := db.IsPluginEnabled(ctx, id.Slug)
	if err != nil {
		return "active"
	}
	if !enabled {
		return "disabled"
	}
	// Subprocess plugins are never compiled in -- no constructor lookup
	// applies to them (the old implementation's fallback lookup against
	// manifest.Name incorrectly reported every subprocess plugin as
	// "no-binary"; fixed here as part of this rewrite).
	if id.Kind == store.PluginKindBuiltin {
		if _, ok := LookupConstructor(id.Slug); !ok {
			return "no-binary"
		}
	}
	return "active"
}
