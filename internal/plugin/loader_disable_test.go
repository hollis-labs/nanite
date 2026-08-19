package plugin

// Phase 5 item 02 (TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md)
// -- acceptance tests: disabling a plugin via the DB-backed `plugins` table
// genuinely stops its registration wiring from running on the next
// reload/restart, for both a builtin (LoadRegisteredBuiltins -- the exact
// path the old file-rename mechanism failed to gate) and a subprocess plugin
// (LoadDiscovered -- verifying the OS process itself never spawns, not just
// that registrations are skipped).

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	goplugin "github.com/hollis-labs/plugin-sdk"
)

// TestLoadRegisteredBuiltins_SkipsDisabledBuiltin reproduces the exact
// confirmed bug this task fixes: a registered builtin constructor not yet
// present in host.plugins used to be loaded unconditionally by
// LoadRegisteredBuiltins, regardless of any disabled state. With a real
// store wired in and the plugin marked disabled beforehand, the builtin must
// now be skipped entirely -- not loaded, no manifest recorded, no
// keybinding/envelope/slot registrations applied.
func TestLoadRegisteredBuiltins_SkipsDisabledBuiltin(t *testing.T) {
	db := newTestPluginStateStore(t)
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	host.SetStore(db)

	const id = "loader-test-disabled-builtin"
	p := &manifestProviderPlugin{id: id, manifest: &PluginManifest{
		Name: id,
		Registers: ManifestRegisters{
			Keybindings: []KeybindingRegistration{
				{ID: id + ".go", Keys: "mod+alt+z", Command: "cmd"},
			},
		},
	}}
	RegisterPlugin(id, func() goplugin.Plugin { return p })
	t.Cleanup(func() { UnregisterPluginForTest(id) })

	// Pre-seed the DB row as disabled, exactly as DisablePlugin would leave
	// it (matches the "GUI/CLI/API disables a builtin" flow, minus the
	// on-disk manifest resolution manage.go layers on top).
	if err := db.SetPluginEnabled(context.Background(), id, "builtin", false); err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}

	loaded, errs := LoadRegisteredBuiltins(host)
	if len(errs) > 0 {
		t.Fatalf("LoadRegisteredBuiltins errs: %v", errs)
	}
	for _, lp := range loaded {
		if lp.ID() == id {
			t.Fatalf("disabled builtin %s was loaded; loaded=%v", id, loaded)
		}
	}
	if _, exists := host.GetPlugin(id); exists {
		t.Fatalf("disabled builtin %s is present in host.plugins", id)
	}
	if host.GetManifest(id) != nil {
		t.Fatalf("disabled builtin %s has a recorded manifest", id)
	}
	for _, kb := range host.GetKeybindings() {
		if kb.ID == id+".go" {
			t.Fatalf("disabled builtin %s's keybinding was registered: %+v", id, kb)
		}
	}

	// Sanity check the opposite direction in the same test: re-enable and
	// confirm the builtin now DOES load and register on a fresh call.
	if err := db.SetPluginEnabled(context.Background(), id, "builtin", true); err != nil {
		t.Fatalf("SetPluginEnabled(enable): %v", err)
	}
	loaded, errs = LoadRegisteredBuiltins(host)
	if len(errs) > 0 {
		t.Fatalf("LoadRegisteredBuiltins (re-enabled) errs: %v", errs)
	}
	found := false
	for _, lp := range loaded {
		if lp.ID() == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("re-enabled builtin %s was not loaded; loaded=%v", id, loaded)
	}
	kbFound := false
	for _, kb := range host.GetKeybindings() {
		if kb.ID == id+".go" {
			kbFound = true
		}
	}
	if !kbFound {
		t.Fatalf("re-enabled builtin %s's keybinding was not registered", id)
	}
}

// TestLoadDiscovered_SkipsDisabledSubprocess verifies that a disabled
// subprocess plugin's OS process never spawns -- not just that its
// registrations are skipped. The manifest's entrypoint points at a
// nonexistent binary; if LoadDiscovered attempted to load this plugin at
// all, newSubprocessPluginFromManifest's exec.LookPath resolution would fail
// and surface an error. A clean "skipped, no errors" result is only possible
// if the disabled check ran BEFORE any subprocess-spawn attempt.
func TestLoadDiscovered_SkipsDisabledSubprocess(t *testing.T) {
	db := newTestPluginStateStore(t)
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	host.SetStore(db)

	pluginsDir := t.TempDir()
	const name = "loader-test-disabled-subprocess"
	dir := filepath.Join(pluginsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version: 1
name: ` + name + `
id: ` + name + `
version: 0.1.0
description: disabled subprocess plugin, never actually spawned
protocol: 1
runtime: subprocess
entrypoint: /nonexistent/path/definitely-not-a-real-binary
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mark disabled in the DB before discovery runs -- matches the "already
	// disabled, then restart/reload" scenario Done means calls for.
	if err := db.SetPluginEnabled(context.Background(), name, "subprocess", false); err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}

	discovered, err := DiscoverPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered plugin, got %d: %+v", len(discovered), discovered)
	}

	loaded, errs := LoadDiscovered(host, discovered)
	if len(errs) > 0 {
		t.Fatalf("LoadDiscovered errs (disabled plugin should be skipped, not attempted): %v", errs)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 loaded plugins, got %d: %v", len(loaded), loaded)
	}
	if _, exists := host.GetPlugin(name); exists {
		t.Fatalf("disabled subprocess plugin %s is present in host.plugins", name)
	}
}

// TestApplyManifestRegistrations_DefenseInDepthSkipsDisabled verifies the
// second, belt-and-suspenders gate directly inside applyManifestRegistrations
// itself -- the shared entrypoint future registration categories (Phase 5
// items 03/05) layer onto. Even called directly (bypassing the loader-level
// gate entirely), a disabled plugin's registrations must not apply.
func TestApplyManifestRegistrations_DefenseInDepthSkipsDisabled(t *testing.T) {
	db := newTestPluginStateStore(t)
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	host.SetStore(db)

	const id = "registrations-test-disabled-plugin"
	if err := db.SetPluginEnabled(context.Background(), id, "builtin", false); err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}

	p := &fakePlugin{id: id}
	m := &PluginManifest{
		Name: id,
		Registers: ManifestRegisters{
			Envelopes: []EnvelopeRegistration{
				{Type: "disabled-plugin-card", Component: "DisabledCard", Version: 1},
			},
		},
	}
	if err := applyManifestRegistrations(host, m, p, ""); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}
	if host.GetManifest(id) != nil {
		t.Fatal("disabled plugin's manifest was recorded")
	}
	for _, e := range host.GetEnvelopes() {
		if e.PluginID == id {
			t.Fatalf("disabled plugin's envelope was registered: %+v", e)
		}
	}
}
