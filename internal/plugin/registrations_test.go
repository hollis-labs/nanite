package plugin

import (
	"net/http"
	"testing"
	"time"

	goplugin "github.com/hollis-labs/go-plugin"
)

// fakePlugin is a minimal plugin.Plugin used for registration tests.
type fakePlugin struct {
	id string
}

func (f *fakePlugin) ID() string             { return f.id }
func (f *fakePlugin) Name() string           { return f.id }
func (f *fakePlugin) Version() string        { return "0.0.1" }
func (f *fakePlugin) Description() string    { return "" }
func (f *fakePlugin) Dependencies() []string { return nil }
func (f *fakePlugin) Load(h goplugin.Host) error {
	return nil
}
func (f *fakePlugin) Unload() error { return nil }
func (f *fakePlugin) Status() goplugin.PluginStatus {
	return goplugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
}

func TestApplyManifestRegistrations_Envelopes(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	p := &fakePlugin{id: "env-plug"}

	// Track envelope registrar invocations via the shared hook.
	seen := map[string]bool{}
	SetEnvelopeTypeRegistrar(func(t string) { seen[t] = true })
	defer SetEnvelopeTypeRegistrar(nil)

	m := &PluginManifest{
		Name: p.id,
		Registers: ManifestRegisters{
			Envelopes: []EnvelopeRegistration{
				{Type: "env-plug/card", Component: "EnvPlugCard", Version: 1, Schema: "schemas/card.json"},
			},
		},
	}
	if err := applyManifestRegistrations(host, m, p); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	entries := host.GetEnvelopes()
	if len(entries) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(entries))
	}
	e := entries[0]
	if e.Type != "env-plug/card" || e.PluginID != "env-plug" || e.Component != "EnvPlugCard" || e.Version != 1 || e.SchemaPath != "schemas/card.json" {
		t.Errorf("unexpected envelope entry: %+v", e)
	}
	if !seen["env-plug/card"] {
		t.Error("envelope registrar hook was not called")
	}
}

func TestApplyManifestRegistrations_Components(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	p := &fakePlugin{id: "comp-plug"}

	m := &PluginManifest{
		Name: p.id,
		Registers: ManifestRegisters{
			Components: []ComponentRegistration{
				{Name: "bookmarks", Type: "widget", Description: "Bookmarks widget"},
			},
		},
	}
	if err := applyManifestRegistrations(host, m, p); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	comps := host.GetUIComponents()
	if len(comps) != 1 || comps[0].ID != "bookmarks" || comps[0].Type != goplugin.UIComponentTypeWidget {
		t.Fatalf("expected bookmarks widget component, got %+v", comps)
	}
	owners := host.GetUIComponentsWithOwners()
	if len(owners) != 1 || owners[0].PluginID != "comp-plug" {
		t.Errorf("expected plugin ownership to be comp-plug, got %+v", owners)
	}
}

func TestApplyManifestRegistrations_Slots(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	p := &fakePlugin{id: "slot-plug"}

	m := &PluginManifest{
		Name: p.id,
		Registers: ManifestRegisters{
			Slots: []SlotRegistration{
				{Slot: "composer-toolbar", ID: "slot-a", Component: "SlotA", Priority: 5},
			},
		},
	}
	if err := applyManifestRegistrations(host, m, p); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	entries := host.GetSlotEntries("composer-toolbar")
	if len(entries) != 1 || entries[0].ID != "slot-a" || entries[0].PluginID != "slot-plug" || entries[0].Priority != 5 {
		t.Fatalf("unexpected slot entries: %+v", entries)
	}
}

func TestApplyManifestRegistrations_Keybindings(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	p := &fakePlugin{id: "kb-plug"}

	m := &PluginManifest{
		Name: p.id,
		Registers: ManifestRegisters{
			Keybindings: []KeybindingRegistration{
				{ID: "kb-plug.greet", Keys: "mod+shift+g", Command: "greet", Description: "Greet"},
			},
		},
	}
	if err := applyManifestRegistrations(host, m, p); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	kbs := host.GetKeybindings()
	if len(kbs) != 1 || kbs[0].ID != "kb-plug.greet" || kbs[0].Key != "mod+shift+g" {
		t.Fatalf("unexpected keybindings: %+v", kbs)
	}
}

func TestApplyManifestRegistrations_DeferredCategoriesNoOp(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	p := &fakePlugin{id: "deferred-plug"}

	// All categories that still require proxy scaffolding (B.5/B.6) should be
	// logged and skipped rather than erroring.
	m := &PluginManifest{
		Name: p.id,
		Registers: ManifestRegisters{
			Commands:      []CommandRegistration{{Name: "cmd"}},
			Events:        []EventRegistration{{Types: []string{"message.sent"}, Handler: "on_sent"}},
			Crud:          []CRUDRegistration{{Resource: "things"}},
			HttpRoutes:    []HTTPRouteRegistration{{Pattern: "/api/foo", Method: "GET", Handler: "foo"}},
			McpServers:    []MCPServerRegistration{{Name: "mcp-foo"}},
			AgentProfiles: []AgentProfileRegistration{{ID: "agent-foo", File: "agents/foo.yaml"}},
		},
	}
	if err := applyManifestRegistrations(host, m, p); err != nil {
		t.Fatalf("applyManifestRegistrations with deferred categories returned error: %v", err)
	}
}

func TestApplyManifestRegistrations_EnvelopeOwnershipCollision(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	SetEnvelopeTypeRegistrar(nil)

	p1 := &fakePlugin{id: "plug-a"}
	p2 := &fakePlugin{id: "plug-b"}

	m1 := &PluginManifest{Name: p1.id, Registers: ManifestRegisters{Envelopes: []EnvelopeRegistration{{Type: "shared", Component: "A", Version: 1}}}}
	if err := applyManifestRegistrations(host, m1, p1); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	m2 := &PluginManifest{Name: p2.id, Registers: ManifestRegisters{Envelopes: []EnvelopeRegistration{{Type: "shared", Component: "B", Version: 1}}}}
	if err := applyManifestRegistrations(host, m2, p2); err == nil {
		t.Fatal("expected envelope collision error for second plugin, got nil")
	}
}

func TestLoadRegisteredBuiltins_AppliesManifest(t *testing.T) {
	// Use a plugin that implements ManifestProvider and verify the loader
	// invokes applyManifestRegistrations for it (B.4 acceptance).
	host := NewHost(http.NewServeMux(), NewLogger("test"))

	// Register a fake builtin via the registry so LoadRegisteredBuiltins picks it up.
	const id = "manifest-builtin-test"
	p := &manifestProviderPlugin{id: id, manifest: &PluginManifest{
		Name: id,
		Registers: ManifestRegisters{
			Keybindings: []KeybindingRegistration{
				{ID: id + ".go", Keys: "mod+alt+b", Command: "cmd"},
			},
		},
	}}
	RegisterPlugin(id, func() goplugin.Plugin { return p })
	t.Cleanup(func() { UnregisterPluginForTest(id) })

	loaded, errs := LoadRegisteredBuiltins(host)
	if len(errs) > 0 {
		t.Fatalf("LoadRegisteredBuiltins errs: %v", errs)
	}
	found := false
	for _, lp := range loaded {
		if lp.ID() == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("builtin %s not loaded; loaded=%v", id, loaded)
	}
	kbs := host.GetKeybindings()
	found = false
	for _, kb := range kbs {
		if kb.ID == id+".go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected keybinding from manifest, got %+v", kbs)
	}
}

// manifestProviderPlugin is a test builtin implementing ManifestProvider.
type manifestProviderPlugin struct {
	id       string
	manifest *PluginManifest
	loaded   bool
}

func (p *manifestProviderPlugin) ID() string             { return p.id }
func (p *manifestProviderPlugin) Name() string           { return p.id }
func (p *manifestProviderPlugin) Version() string        { return "0.0.1" }
func (p *manifestProviderPlugin) Description() string    { return "" }
func (p *manifestProviderPlugin) Dependencies() []string { return nil }
func (p *manifestProviderPlugin) Load(h goplugin.Host) error {
	p.loaded = true
	return nil
}
func (p *manifestProviderPlugin) Unload() error                { p.loaded = false; return nil }
func (p *manifestProviderPlugin) Status() goplugin.PluginStatus { return goplugin.PluginStatus{Loaded: p.loaded, Enabled: true} }
func (p *manifestProviderPlugin) Manifest() *PluginManifest    { return p.manifest }
