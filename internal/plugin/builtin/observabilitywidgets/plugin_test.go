package observabilitywidgets

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
	if m.ID != "observability-widgets" {
		t.Errorf("expected ID observability-widgets, got %q", m.ID)
	}
	if len(m.Registers.Components) != 1 || m.Registers.Components[0].Name != "observability" {
		t.Errorf("expected 1 observability component, got %+v", m.Registers.Components)
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

func TestManifestPathRegistersWidget(t *testing.T) {
	host := hostplugin.NewHost(http.NewServeMux(), hostplugin.NewLogger("test"))
	if _, errs := hostplugin.LoadRegisteredBuiltins(host); len(errs) != 0 {
		t.Fatalf("LoadRegisteredBuiltins errs: %v", errs)
	}
	found := false
	for _, c := range host.GetUIComponents() {
		if c.ID == "observability" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected observability component registered via manifest path, got %+v", host.GetUIComponents())
	}
}
