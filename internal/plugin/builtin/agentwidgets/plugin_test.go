package agentwidgets

import (
	"net/http"
	"testing"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
)

func TestManifestYAMLParses(t *testing.T) {
	m := New().Manifest()
	if m == nil {
		t.Fatal("Manifest() returned nil")
	}
	if m.ID != "agent-widgets" {
		t.Errorf("expected ID agent-widgets, got %q", m.ID)
	}
	if len(m.Registers.Components) != 2 {
		t.Fatalf("expected 2 components in manifest, got %d", len(m.Registers.Components))
	}
	want := map[string]bool{"agent-status": false, "tools": false}
	for _, c := range m.Registers.Components {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected component %q in manifest", name)
		}
	}
}

// TestLoadDoesNotRegisterComponentsDirectly verifies Load() no longer calls
// host.RegisterUIComponent — widget registration now flows through the
// yaml-authoritative loader via Manifest().
func TestLoadDoesNotRegisterComponentsDirectly(t *testing.T) {
	host := hostplugin.NewHost(http.NewServeMux(), hostplugin.NewLogger("test"))
	p := New()
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if comps := host.GetUIComponents(); len(comps) != 0 {
		t.Fatalf("expected Load() to register no components directly, got %+v", comps)
	}
}

// TestManifestPathRegistersWidgets proves LoadRegisteredBuiltins applies the
// yaml-authoritative manifest path, populating the host UI component registry.
func TestManifestPathRegistersWidgets(t *testing.T) {
	host := hostplugin.NewHost(http.NewServeMux(), hostplugin.NewLogger("test"))
	loaded, errs := hostplugin.LoadRegisteredBuiltins(host)
	if len(errs) != 0 {
		t.Fatalf("LoadRegisteredBuiltins errs: %v", errs)
	}
	if len(loaded) == 0 {
		t.Fatal("LoadRegisteredBuiltins returned empty list")
	}
	found := map[string]bool{"agent-status": false, "tools": false}
	for _, c := range host.GetUIComponents() {
		if _, ok := found[c.ID]; ok {
			found[c.ID] = true
		}
	}
	for id, ok := range found {
		if !ok {
			t.Errorf("expected component %q registered via manifest path", id)
		}
	}
}
