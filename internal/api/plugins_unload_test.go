package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	goplugin "github.com/hollis-labs/plugin-sdk"
)

// TASKS/phase-5/12-fix-unload-plugin-wrong-identifier.md regression tests.
//
// unloadPluginFromHost and runPluginLoadIntoHost's manifest-apply rollback
// branch both used to call Host.UnloadPlugin(manifest.Name), but Host's
// registry (internal/plugin/host.go's Host.plugins map) is keyed by
// p.ID() / manifest.Identifier() (the v1 `id:` field when set), not the
// human-readable `name:` field. Existing test fixtures across the repo all
// happen to use id == name (every shipped builtin's plugin.yaml does, and
// most hand-written test fixtures follow suit), so the bug shipped
// unnoticed. These tests deliberately use fixtures where id != name — the
// normal case in practice, and exactly what `nanite plugin new`'s own
// scaffold generates (internal/plugin/scaffold/templates/subprocess/
// plugin.yaml.tmpl: `id: {{.Name}}` kebab-case, `name: {{.DisplayName}}`
// Title Case).

// idMismatchPlugin is a minimal in-process plugin.Plugin whose ID()
// deliberately differs from what a manifest's `name:` field would say, used
// to load a real entry into Host.plugins without needing a real subprocess.
type idMismatchPlugin struct {
	id string
}

func (p *idMismatchPlugin) ID() string             { return p.id }
func (p *idMismatchPlugin) Name() string           { return "Mismatch Display Name" }
func (p *idMismatchPlugin) Version() string        { return "0.1.0" }
func (p *idMismatchPlugin) Description() string    { return "id != name regression fixture" }
func (p *idMismatchPlugin) Dependencies() []string { return nil }
func (p *idMismatchPlugin) Load(h goplugin.Host) error { return nil }
func (p *idMismatchPlugin) Unload() error              { return nil }
func (p *idMismatchPlugin) Status() goplugin.PluginStatus {
	return goplugin.PluginStatus{Loaded: true, Enabled: true}
}

// writeIDMismatchManifest writes a plugin.yaml whose `id:` and `name:`
// fields deliberately differ and returns its path.
func writeIDMismatchManifest(t *testing.T, dir, id, name string, extra string) string {
	t.Helper()
	manifest := "schema_version: 1\n" +
		"id: " + id + "\n" +
		"name: " + name + "\n" +
		"version: 0.1.0\n" +
		"description: id != name regression fixture\n" +
		"runtime: builtin\n" +
		extra
	path := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestUnloadPluginFromHost_UsesIdentifierNotName is the direct regression
// test for the primary bug site (unloadPluginFromHost). Before the fix,
// UnloadPlugin was called with manifest.Name — a value the host's registry
// never used as a key when id != name — so the call silently no-op'd
// (logged as a WARN, swallowed) and the plugin stayed loaded.
func TestUnloadPluginFromHost_UsesIdentifierNotName(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	pms := &pluginManagerState{pluginHost: host}

	const id = "mismatch-id"
	const name = "Mismatch Display Name"

	// Load a plugin directly into the host, keyed by its ID (mirrors what
	// Host.LoadPlugin/runPluginLoadIntoHost's SubprocessPlugin construction
	// do: the stored key is always p.ID(), which for a real load equals
	// manifest.Identifier()).
	p := &idMismatchPlugin{id: id}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if _, ok := host.GetPlugin(id); !ok {
		t.Fatalf("precondition failed: plugin %q not found in host after LoadPlugin", id)
	}

	dir := t.TempDir()
	manifestPath := writeIDMismatchManifest(t, dir, id, name, "")

	if unloaded := pms.unloadPluginFromHost(manifestPath); !unloaded {
		t.Fatal("unloadPluginFromHost returned false — expected true (manifest.Identifier() should match the host's stored key)")
	}
	if _, ok := host.GetPlugin(id); ok {
		t.Fatalf("plugin %q is still present in the host after unloadPluginFromHost reported success — regression: wrong identifier used", id)
	}
}

// TestRunPluginLoadIntoHost_ReloadTwice_IDNameMismatch reproduces the live
// symptom described in TASKS/phase-5/12: "nanite plugin reload <id>
// succeeds the first time, then 500s on every subsequent reload of the same
// plugin with 'already loaded', because the prior instance was never
// actually torn down." handleReload calls unloadPluginFromHost then
// runPluginLoadIntoHost; this test drives that exact pair twice in a row
// against an id != name fixture, the same shape `nanite plugin new`'s own
// scaffold generates.
func TestRunPluginLoadIntoHost_ReloadTwice_IDNameMismatch(t *testing.T) {
	const id = "reload-mismatch-id"
	const name = "Reload Mismatch Display Name"

	// Builtin constructor lookup in runPluginLoadIntoHost keys off
	// manifest.Name (a separate, pre-existing, out-of-scope quirk — see
	// TASKS/phase-5/12's Work Log) — register under that key. The plugin's
	// own ID(), like any real hand-authored builtin, reports the manifest's
	// `id:` field, which is the value that actually matters for this test.
	naniteplugin.RegisterPlugin(name, func() goplugin.Plugin {
		return &idMismatchPlugin{id: id}
	})
	t.Cleanup(func() { naniteplugin.UnregisterPluginForTest(name) })

	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	pms := &pluginManagerState{pluginHost: host}

	dir := t.TempDir()
	manifestPath := writeIDMismatchManifest(t, dir, id, name, "")

	// First load (mirrors a fresh install / first reload).
	if loaded := pms.runPluginLoadIntoHost(manifestPath, dir); !loaded {
		t.Fatal("first runPluginLoadIntoHost returned false, expected true")
	}
	if _, ok := host.GetPlugin(id); !ok {
		t.Fatalf("plugin %q not found in host after first load", id)
	}

	// handleReload's own sequence: unload, then load again. Pre-fix, the
	// unload below silently no-ops (wrong key), so the second LoadPlugin
	// call below fails with "already loaded".
	unloaded := pms.unloadPluginFromHost(manifestPath)
	if !unloaded {
		t.Fatal("unloadPluginFromHost (reload step 1) returned false, expected true")
	}
	if _, ok := host.GetPlugin(id); ok {
		t.Fatalf("plugin %q still present in host after unload — reload's teardown did not actually happen", id)
	}

	if loaded := pms.runPluginLoadIntoHost(manifestPath, dir); !loaded {
		t.Fatal("second runPluginLoadIntoHost (reload step 2) returned false — this is the exact live regression: reload succeeds once, then fails on every subsequent attempt with \"already loaded\" because the prior instance was never torn down")
	}
	if _, ok := host.GetPlugin(id); !ok {
		t.Fatalf("plugin %q not found in host after second load", id)
	}

	// A third reload cycle to make sure this isn't a fluke of exactly two
	// iterations (the live bug report specifically says "every subsequent
	// reload", not just the second one).
	if unloaded := pms.unloadPluginFromHost(manifestPath); !unloaded {
		t.Fatal("unloadPluginFromHost (reload cycle 2, step 1) returned false, expected true")
	}
	if loaded := pms.runPluginLoadIntoHost(manifestPath, dir); !loaded {
		t.Fatal("third runPluginLoadIntoHost (reload cycle 2, step 2) returned false, expected true")
	}
}

// TestRunPluginLoadIntoHost_RollbackUsesPluginID is the direct regression
// test for the second bug site: the ApplyManifestRegistrations-failure
// rollback branch inside runPluginLoadIntoHost. Before the fix, the
// rollback called Host.UnloadPlugin(manifest.Name), which silently no-op'd
// for an id != name manifest — leaving the plugin fully present in
// Host.plugins (LoadPlugin had already succeeded) even though
// runPluginLoadIntoHost reported failure to its caller. That's a worse
// state than a clean failure: the caller believes nothing is loaded, but a
// second load attempt would then fail with "already loaded".
func TestRunPluginLoadIntoHost_RollbackUsesPluginID(t *testing.T) {
	const id = "rollback-mismatch-id"
	const name = "Rollback Mismatch Display Name"

	naniteplugin.RegisterPlugin(name, func() goplugin.Plugin {
		return &idMismatchPlugin{id: id}
	})
	t.Cleanup(func() { naniteplugin.UnregisterPluginForTest(name) })

	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	pms := &pluginManagerState{pluginHost: host}

	dir := t.TempDir()
	// An invalid component `type:` makes host.RegisterUIComponent fail
	// immediately inside applyManifestRegistrations, forcing the
	// LoadPlugin-succeeded-then-ApplyManifestRegistrations-failed rollback
	// path used by runPluginLoadIntoHost.
	badRegisters := "registers:\n" +
		"  components:\n" +
		"    - name: bogus-component\n" +
		"      type: not-a-real-type\n" +
		"      description: deliberately invalid to force ApplyManifestRegistrations to fail\n"
	manifestPath := writeIDMismatchManifest(t, dir, id, name, badRegisters)

	if loaded := pms.runPluginLoadIntoHost(manifestPath, dir); loaded {
		t.Fatal("runPluginLoadIntoHost returned true, expected false (ApplyManifestRegistrations should fail on the invalid component type)")
	}

	// The load must have been fully rolled back — the plugin must not be
	// left half-registered in the host under its real ID.
	if _, ok := host.GetPlugin(id); ok {
		t.Fatalf("plugin %q is still present in the host after a failed load — rollback used the wrong identifier and silently no-op'd", id)
	}

	// A subsequent load attempt must succeed once the bad registration is
	// fixed — proves the rollback actually freed the id, rather than just
	// happening to look clean via GetPlugin while some other internal state
	// still thinks the plugin is loaded.
	fixedRegisters := "registers:\n" +
		"  components:\n" +
		"    - name: bogus-component\n" +
		"      type: widget\n" +
		"      description: fixed, should load cleanly now\n"
	fixedManifestPath := writeIDMismatchManifest(t, dir, id, name, fixedRegisters)
	if loaded := pms.runPluginLoadIntoHost(fixedManifestPath, dir); !loaded {
		t.Fatal("runPluginLoadIntoHost with a corrected manifest returned false, expected true — the earlier failed attempt must not have left the id occupied")
	}
}
