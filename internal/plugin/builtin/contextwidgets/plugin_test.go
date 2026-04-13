package contextwidgets

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
	if m.ID != "context-widgets" {
		t.Errorf("expected ID context-widgets, got %q", m.ID)
	}
	if len(m.Registers.Components) != 3 {
		t.Fatalf("expected 3 components in manifest, got %d", len(m.Registers.Components))
	}
}

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

func TestManifestPathRegistersWidgets(t *testing.T) {
	host := hostplugin.NewHost(http.NewServeMux(), hostplugin.NewLogger("test"))
	if _, errs := hostplugin.LoadRegisteredBuiltins(host); len(errs) != 0 {
		t.Fatalf("LoadRegisteredBuiltins errs: %v", errs)
	}
	found := map[string]bool{"session-info": false, "context-budget": false, "token-usage": false}
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
