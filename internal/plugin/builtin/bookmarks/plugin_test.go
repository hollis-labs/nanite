package bookmarks

import (
	"net/http"
	"testing"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	goplugin "github.com/hollis-labs/plugin-sdk"
)

// TestManifestYAMLParses verifies the embedded plugin.yaml parses cleanly
// and carries the expected declarative component registration (B.4 proof
// that at least one builtin flows through the yaml-authoritative path).
func TestManifestYAMLParses(t *testing.T) {
	p := New()
	m := p.Manifest()
	if m == nil {
		t.Fatal("Manifest() returned nil — embedded plugin.yaml failed to parse")
	}
	if m.ID != "bookmarks-widget" {
		t.Errorf("expected ID bookmarks-widget, got %q", m.ID)
	}
	if len(m.Registers.Components) != 1 || m.Registers.Components[0].Name != "bookmarks" {
		t.Errorf("expected 1 bookmarks component in manifest, got %+v", m.Registers.Components)
	}
}

// TestLoadRegistersComponentViaManifest proves Load() no longer registers the
// component directly, and that feeding this plugin through the same loader
// path (applyManifestRegistrations via ManifestProvider) populates the host
// UI component registry.
func TestLoadRegistersComponentViaManifest(t *testing.T) {
	host := hostplugin.NewHost(http.NewServeMux(), hostplugin.NewLogger("test"))
	p := New()

	// Simulate the loader: LoadPlugin, then applyManifestRegistrations via
	// the ManifestProvider interface.  Use the host-public LoadPlugin and
	// assert the UI component arrived through the manifest path.
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	// Load() alone must not register the component any more.
	if comps := host.GetUIComponents(); len(comps) != 0 {
		t.Fatalf("expected Load() to not register components directly, got %+v", comps)
	}

	// Apply the manifest as the loader would.
	mp, ok := goplugin.Plugin(p).(hostplugin.ManifestProvider)
	if !ok {
		t.Fatal("bookmarks plugin must implement ManifestProvider after B.4 migration")
	}
	// Can't call applyManifestRegistrations from outside the package. Instead
	// invoke LoadRegisteredBuiltins which covers the full path (builtins already
	// loaded are skipped, so reset).
	_ = mp

	// Reload via registry path by unloading first, then using LoadRegisteredBuiltins.
	if err := host.UnloadPlugin(p.ID()); err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}
	loaded, errs := hostplugin.LoadRegisteredBuiltins(host)
	if len(errs) > 0 {
		t.Fatalf("LoadRegisteredBuiltins errs: %v", errs)
	}
	if len(loaded) == 0 {
		t.Fatal("LoadRegisteredBuiltins returned empty list; bookmarks-widget not reloaded")
	}

	found := false
	for _, c := range host.GetUIComponents() {
		if c.ID == "bookmarks" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bookmarks UI component not registered via manifest path: %+v", host.GetUIComponents())
	}
}
