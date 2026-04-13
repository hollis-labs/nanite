package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	goplugin "github.com/hollis-labs/go-plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// stubMCPRegistrar records per-plugin server tracking without a real
// *mcp.Manager (which would pull in the full subprocess stack).
type stubMCPRegistrar struct {
	added   map[string][]string
	removed map[string]int
}

func newStubMCPRegistrar() *stubMCPRegistrar {
	return &stubMCPRegistrar{
		added:   map[string][]string{},
		removed: map[string]int{},
	}
}

func (s *stubMCPRegistrar) AddPluginServer(pluginID, name string, _ *subprocess.Transport) error {
	s.added[pluginID] = append(s.added[pluginID], name)
	return nil
}

func (s *stubMCPRegistrar) RemoveServersByPlugin(pluginID string) int {
	n := len(s.added[pluginID])
	delete(s.added, pluginID)
	s.removed[pluginID] = n
	return n
}

// TestUnloadPlugin_SweepsAllB6aCategories wires up a single host with
// registrations across every B.6a-covered category, unloads the plugin, and
// asserts each registry shows zero entries for the unloaded plugin. Categories
// deferred to B.6b (commands, event hooks, crud handlers, providers) are not
// exercised here.
func TestUnloadPlugin_SweepsAllB6aCategories(t *testing.T) {
	mux := http.NewServeMux()
	host := NewHost(mux, NewLogger("unload-sweep"))

	// MCP registrar stub captures server removal.
	mcp := newStubMCPRegistrar()
	host.SetMCPRegistrar(mcp)

	const pluginID = "alpha"
	plug := &TestPlugin{id: pluginID, name: "Alpha", version: "0.1"}

	// Simulate the LoadPlugin path: we set activePlugin, invoke the Register*
	// methods directly (bypassing yaml loader which would drive these from the
	// manifest), then commit the plugin to h.plugins.
	host.mu.Lock()
	host.activePlugin = pluginID
	host.mu.Unlock()

	// 1. Envelope.
	if err := host.RegisterEnvelope(EnvelopeRegistryEntry{
		Type: "alpha-envelope", PluginID: pluginID, Component: "AlphaEnvelope", Version: 1,
	}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}

	// 2. UI component.
	if err := host.RegisterUIComponent(goplugin.UIComponent{
		ID: "alpha-widget", Type: goplugin.UIComponentTypeWidget, Name: "Alpha Widget",
	}); err != nil {
		t.Fatalf("RegisterUIComponent: %v", err)
	}

	// 3. UI slot.
	if err := host.RegisterSlot(UISlotEntry{
		ID: "alpha-slot", Slot: "topbar", Label: "Alpha",
	}); err != nil {
		t.Fatalf("RegisterSlot: %v", err)
	}

	// 4. Keybinding.
	if err := host.RegisterKeybinding(KeybindingDef{
		ID: "alpha-key", Key: "mod+shift+a", Label: "Alpha Action",
	}); err != nil {
		t.Fatalf("RegisterKeybinding: %v", err)
	}

	// 5. Filter.
	if err := host.RegisterFilter("alpha-filter", 50, func(_ interface{}, _ FilterContext) (interface{}, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("RegisterFilter: %v", err)
	}

	// 6. Connector.
	if err := host.RegisterConnector("alpha-conn", nil); err != nil {
		t.Fatalf("RegisterConnector: %v", err)
	}

	// 7. Service + 8. CLI adapter.
	host.RegisterService("alpha-svc", struct{}{})
	if err := host.RegisterCLIAdapter("alpha-cli", struct{}{}); err != nil {
		t.Fatalf("RegisterCLIAdapter: %v", err)
	}

	// 9. MCP — go through the same hook UnloadPlugin uses to clean up.
	// We don't need a real subprocess; the stub only tracks ids/names.
	if err := mcp.AddPluginServer(pluginID, "alpha-mcp", nil); err != nil {
		t.Fatalf("stub AddPluginServer: %v", err)
	}

	// 10. HTTP route.
	host.RegisterHTTPHandler("GET /api/plugins/alpha/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))

	// Commit the plugin so UnloadPlugin finds it.
	host.mu.Lock()
	host.plugins[pluginID] = plug
	host.activePlugin = ""
	host.mu.Unlock()

	// Sanity: forwarder installed, route answers.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/plugins/alpha/ping", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != "pong" {
			t.Fatalf("pre-unload ping: status=%d body=%q", rr.Code, rr.Body.String())
		}
	}

	// Unload.
	if err := host.UnloadPlugin(pluginID); err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}

	// Assertions — one per category.
	host.mu.RLock()
	defer host.mu.RUnlock()

	if _, ok := host.envelopes["alpha-envelope"]; ok {
		t.Error("envelope survived unload")
	}
	if host.uiOwners["alpha-widget"] != "" {
		t.Error("ui component owner survived unload")
	}
	for _, c := range host.uiComponents {
		if c.ID == "alpha-widget" {
			t.Error("ui component survived unload")
		}
	}
	if entries := host.slots["topbar"]; len(entries) > 0 {
		for _, e := range entries {
			if e.PluginID == pluginID {
				t.Error("slot entry survived unload")
			}
		}
	}
	if _, ok := host.keybindings["alpha-key"]; ok {
		t.Error("keybinding survived unload")
	}
	if host.filters.Len("alpha-filter") != 0 {
		t.Error("filter survived unload")
	}
	if _, ok := host.connectors["alpha-conn"]; ok {
		t.Error("connector survived unload")
	}
	if _, ok := host.services["alpha-svc"]; ok {
		t.Error("service survived unload")
	}
	if _, ok := host.services["cli-adapter:alpha-cli"]; ok {
		t.Error("cli adapter survived unload")
	}
	if n := host.pluginMux.CountByPlugin(pluginID); n != 0 {
		t.Errorf("plugin http routes survived unload: %d", n)
	}
	if mcp.removed[pluginID] != 1 {
		t.Errorf("mcp remove not called correctly: %+v", mcp.removed)
	}

	// The core mux still has the forwarder; it now returns 404.
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/alpha/ping", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("post-unload ping: status=%d want 404", rr.Code)
	}
	_ = context.Background()
}
