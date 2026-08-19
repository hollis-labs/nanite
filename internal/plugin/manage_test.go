package plugin

// Phase 5 item 02 (TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md)
// -- tests for the DB-backed installed/enabled state model that replaces the
// old plugin.yaml <-> plugin.yaml.disabled file-rename mechanism.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	goplugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/nanite/internal/store"
)

// newTestPluginStateStore opens a temp-dir-backed store, installs it as the
// package-level plugin state store for the duration of the test, and resets
// the global back to nil on cleanup so later tests in this package (which
// run in the same test binary and therefore share package-level state)
// never see a stale or closed store.
func newTestPluginStateStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "nanite-test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	SetPluginStateStore(s)
	t.Cleanup(func() {
		SetPluginStateStore(nil)
		_ = s.Close()
	})
	return s
}

// noopBuiltinPlugin is a minimal goplugin.Plugin used to register a fake
// builtin constructor for manage.go identity-resolution tests.
type noopBuiltinPlugin struct{ id string }

func (p *noopBuiltinPlugin) ID() string             { return p.id }
func (p *noopBuiltinPlugin) Name() string           { return p.id }
func (p *noopBuiltinPlugin) Version() string        { return "0.0.1" }
func (p *noopBuiltinPlugin) Description() string    { return "" }
func (p *noopBuiltinPlugin) Dependencies() []string { return nil }
func (p *noopBuiltinPlugin) Load(h goplugin.Host) error { return nil }
func (p *noopBuiltinPlugin) Unload() error              { return nil }
func (p *noopBuiltinPlugin) Status() goplugin.PluginStatus {
	return goplugin.PluginStatus{Loaded: true, Enabled: true}
}

// TestManage_BuiltinDisableEnable_NoOnDiskManifest exercises the default
// deployment shape for all 12 real builtins: no copy of plugin.yaml under
// pluginsDir at all (their real manifest is embedded into the binary via
// //go:embed and lives under internal/plugin/builtin/*/plugin.yaml, a
// completely different directory tree than the runtime pluginsDir). manage.go
// must still be able to resolve, disable, and enable such a plugin purely via
// its registered constructor id.
func TestManage_BuiltinDisableEnable_NoOnDiskManifest(t *testing.T) {
	newTestPluginStateStore(t)

	const id = "manage-test-builtin"
	RegisterPlugin(id, func() goplugin.Plugin { return &noopBuiltinPlugin{id: id} })
	t.Cleanup(func() { UnregisterPluginForTest(id) })

	pluginsDir := t.TempDir() // empty -- no pluginsDir/manage-test-builtin at all

	if status := PluginStatus(pluginsDir, id); status != "active" {
		t.Fatalf("PluginStatus before disable = %q, want active", status)
	}
	if IsDisabled(pluginsDir, id) {
		t.Fatal("IsDisabled = true before any disable call")
	}

	if err := DisablePlugin(pluginsDir, id); err != nil {
		t.Fatalf("DisablePlugin: %v", err)
	}
	if status := PluginStatus(pluginsDir, id); status != "disabled" {
		t.Fatalf("PluginStatus after disable = %q, want disabled", status)
	}
	if !IsDisabled(pluginsDir, id) {
		t.Fatal("IsDisabled = false after DisablePlugin")
	}
	// No file should have been created or renamed -- the file-rename
	// mechanism is fully retired.
	if _, err := os.Stat(filepath.Join(pluginsDir, id)); !os.IsNotExist(err) {
		t.Fatalf("expected no pluginsDir/%s directory to exist, stat err = %v", id, err)
	}

	if err := DisablePlugin(pluginsDir, id); err == nil {
		t.Fatal("expected error disabling an already-disabled plugin")
	}

	if err := EnablePlugin(pluginsDir, id); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}
	if status := PluginStatus(pluginsDir, id); status != "active" {
		t.Fatalf("PluginStatus after enable = %q, want active", status)
	}
	if IsDisabled(pluginsDir, id) {
		t.Fatal("IsDisabled = true after EnablePlugin")
	}
	if err := EnablePlugin(pluginsDir, id); err == nil {
		t.Fatal("expected error enabling an already-enabled plugin")
	}
}

// TestManage_SubprocessDisableEnable exercises a plugin with a real on-disk
// pluginsDir/<name>/plugin.yaml (the normal shape for subprocess plugins).
// PluginStatus for a valid, enabled subprocess plugin must report "active",
// not "no-binary" -- subprocess plugins never have a compiled-in constructor,
// so a naive constructor-lookup check would misreport every one of them.
func TestManage_SubprocessDisableEnable(t *testing.T) {
	newTestPluginStateStore(t)

	pluginsDir := t.TempDir()
	const name = "manage-test-subprocess"
	dir := filepath.Join(pluginsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version: 1
name: ` + name + `
id: ` + name + `
version: 0.1.0
description: test subprocess plugin
protocol: 1
runtime: subprocess
entrypoint: ./fake-entrypoint
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if status := PluginStatus(pluginsDir, name); status != "active" {
		t.Fatalf("PluginStatus for valid subprocess plugin = %q, want active", status)
	}

	if err := DisablePlugin(pluginsDir, name); err != nil {
		t.Fatalf("DisablePlugin: %v", err)
	}
	if status := PluginStatus(pluginsDir, name); status != "disabled" {
		t.Fatalf("PluginStatus after disable = %q, want disabled", status)
	}
	// plugin.yaml must still be present under its normal name -- no rename.
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
		t.Fatalf("plugin.yaml should still exist after disable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml.disabled")); !os.IsNotExist(err) {
		t.Fatalf("plugin.yaml.disabled should NOT exist -- file-rename mechanism is retired (stat err = %v)", err)
	}

	if err := EnablePlugin(pluginsDir, name); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}
	if status := PluginStatus(pluginsDir, name); status != "active" {
		t.Fatalf("PluginStatus after enable = %q, want active", status)
	}
}

// TestManage_LegacyDisabledManifestMigration verifies that a directory left
// over from the old file-rename mechanism (plugin.yaml.disabled, no
// plugin.yaml) is transparently migrated on first read: the file is restored
// to plugin.yaml and the plugin's disabled state is preserved in the DB
// instead of silently reverting to enabled.
func TestManage_LegacyDisabledManifestMigration(t *testing.T) {
	newTestPluginStateStore(t)

	pluginsDir := t.TempDir()
	const name = "manage-test-legacy-disabled"
	dir := filepath.Join(pluginsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version: 1
name: ` + name + `
id: ` + name + `
version: 0.1.0
description: legacy disabled test plugin
protocol: 1
runtime: subprocess
entrypoint: ./fake-entrypoint
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml.disabled"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	status := PluginStatus(pluginsDir, name)
	if status != "disabled" {
		t.Fatalf("PluginStatus for legacy-disabled plugin = %q, want disabled", status)
	}
	// The rename must have happened: plugin.yaml now present, .disabled gone.
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
		t.Fatalf("plugin.yaml should exist after migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml.disabled")); !os.IsNotExist(err) {
		t.Fatalf("plugin.yaml.disabled should be gone after migration (stat err = %v)", err)
	}
	if !IsDisabled(pluginsDir, name) {
		t.Fatal("IsDisabled = false for a migrated legacy-disabled plugin")
	}
}

// TestManage_PluginStatus_NotInstalled verifies the not-installed case for a
// name that resolves to neither an on-disk manifest nor a registered
// constructor.
func TestManage_PluginStatus_NotInstalled(t *testing.T) {
	newTestPluginStateStore(t)
	pluginsDir := t.TempDir()
	if status := PluginStatus(pluginsDir, "does-not-exist-anywhere"); status != "not-installed" {
		t.Fatalf("PluginStatus for nonexistent plugin = %q, want not-installed", status)
	}
	if err := DisablePlugin(pluginsDir, "does-not-exist-anywhere"); err == nil {
		t.Fatal("expected error disabling a nonexistent plugin")
	}
}
