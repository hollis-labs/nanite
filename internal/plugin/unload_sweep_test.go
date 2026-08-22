package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
	goplugin "github.com/hollis-labs/plugin-sdk"
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

// stubCommandRegistrar is a minimal CommandRegistrar that records sources
// and implements RemoveByPlugin with the same semantics as
// chat.CommandRegistry. Using a stub keeps this test in-package and avoids
// the (plugin -> chat) dependency that would require moving the test.
type stubCommandRegistrar struct {
	commands map[string]string // name → source (pluginID)
	removed  map[string]int
}

func newStubCommandRegistrar() *stubCommandRegistrar {
	return &stubCommandRegistrar{
		commands: make(map[string]string),
		removed:  make(map[string]int),
	}
}

func (s *stubCommandRegistrar) RegisterPluginCommand(cmd SlashCommandDef, source string) {
	s.commands[cmd.Name] = source
}

func (s *stubCommandRegistrar) RemoveByPlugin(pluginID string) int {
	if pluginID == "" {
		return 0
	}
	n := 0
	for name, src := range s.commands {
		if src == pluginID {
			delete(s.commands, name)
			n++
		}
	}
	s.removed[pluginID] = n
	return n
}

// stubEventHook implements the plugin-sdk EventHook for the sweep test.
type stubEventHook struct{ types []string }

func (s *stubEventHook) Handle(_ context.Context, _ goplugin.Event) error { return nil }
func (s *stubEventHook) EventTypes() []string                             { return s.types }
func (s *stubEventHook) PluginID() string                                 { return "test-plugin" }

// stubCRUDHandler implements the plugin-sdk CRUDHandler for the sweep test.
type stubCRUDHandler struct{}

func (stubCRUDHandler) Create(context.Context, interface{}) (interface{}, error) {
	return nil, nil
}
func (stubCRUDHandler) Read(context.Context, string) (interface{}, error) { return nil, nil }
func (stubCRUDHandler) Update(context.Context, string, interface{}) (interface{}, error) {
	return nil, nil
}
func (stubCRUDHandler) Delete(context.Context, string) error { return nil }
func (stubCRUDHandler) List(context.Context, map[string]interface{}) ([]interface{}, error) {
	return nil, nil
}

// stubTaskService implements taskBackendRegistrar with in-memory ownership
// so the test doesn't need to spin up the full task.Service (which would
// pull in its DB dependency). It exposes the backends map for assertions.
type stubTaskService struct {
	backends map[string]interface{}
}

func newStubTaskService() *stubTaskService {
	return &stubTaskService{backends: map[string]interface{}{"local": struct{}{}}}
}

func (s *stubTaskService) RegisterBackend(name string, backend interface{}) {
	s.backends[name] = backend
}

func (s *stubTaskService) UnregisterBackend(name string) bool {
	if name == "local" {
		return false
	}
	if _, ok := s.backends[name]; !ok {
		return false
	}
	delete(s.backends, name)
	return true
}

// TestUnloadPlugin_FullTeardown wires up a single host with registrations
// across every B.6 hot-unload category that can be swept, unloads the plugin,
// and asserts each registry shows zero entries for the unloaded plugin.
//
// Categories covered (16): envelopes, components, slots, keybindings, filters,
// connectors, services, CLI adapters, MCP servers, HTTP routes, commands,
// task backends, config schemas, event hooks, CRUD handlers, providers.
//
// Providers are exercised via a stub provider-registry service that
// implements the same Register/Unregister contract go-providers v0.1.0+
// exposes. Host-side providerOwners and registry entries are both asserted
// empty after unload.
func TestUnloadPlugin_FullTeardown(t *testing.T) {
	mux := http.NewServeMux()
	host := NewHost(mux, NewLogger("unload-full-teardown"))

	// --- External registrars / services the host sweeps through ---

	mcp := newStubMCPRegistrar()
	host.SetMCPRegistrar(mcp)

	cmds := newStubCommandRegistrar()
	host.SetCommandRegistry(cmds)

	providers := newStubProviderRegistry()
	// Register pre-plugin (activePlugin empty) so it survives the sweep as a
	// core service. Plugin-registered providers get tracked in providerOwners.
	host.RegisterService("provider-registry", providers)

	tasks := newStubTaskService()
	// Register as "tasks" service at core scope (pre-plugin) so it survives
	// the plugin unload — this mirrors how main.go wires task.Service.
	host.RegisterService("tasks", tasks)

	// Real store-backed config schema path so we exercise ClearPluginSchema
	// end-to-end (not just a stub).
	storePath := filepath.Join(t.TempDir(), "nanite-test.db")
	db, err := store.New(context.Background(), storePath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	host.SetStore(db)

	const pluginID = "alpha"
	plug := &TestPlugin{id: pluginID, name: "Alpha", version: "0.1"}

	// Enter plugin-load context so Register* tag ownership.
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

	// 7. Service (non-core; "tasks" stays registered as core).
	host.RegisterService("alpha-svc", struct{}{})

	// 8. CLI adapter.
	if err := host.RegisterCLIAdapter("alpha-cli", struct{}{}); err != nil {
		t.Fatalf("RegisterCLIAdapter: %v", err)
	}

	// 9. MCP server — go through the same hook UnloadPlugin uses.
	if err := mcp.AddPluginServer(pluginID, "alpha-mcp", nil); err != nil {
		t.Fatalf("stub AddPluginServer: %v", err)
	}

	// 10. HTTP route.
	host.RegisterHTTPHandler("GET /api/plugins/alpha/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))

	// 11. CRUD handler (also installs its own HTTP forwarders via pluginMux).
	if err := host.RegisterCRUDHandler("alpha-resource", stubCRUDHandler{}); err != nil {
		t.Fatalf("RegisterCRUDHandler: %v", err)
	}

	// 12. Command.
	if err := host.RegisterCommand(SlashCommandDef{
		Name: "alpha-cmd", Description: "Alpha command",
	}); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}

	// 13. Task backend.
	if err := host.RegisterTaskBackend("alpha-backend", &dummyTaskBackend{}); err != nil {
		t.Fatalf("RegisterTaskBackend: %v", err)
	}

	// 14. Config schema (persisted via store).
	if err := host.RegisterConfigSchema([]goplugin.ConfigFieldDef{
		{Key: "api_key", Type: "secret", Label: "API Key", Required: true},
	}); err != nil {
		t.Fatalf("RegisterConfigSchema: %v", err)
	}

	// 15. Event hook.
	hook := &stubEventHook{types: []string{"alpha.ping"}}
	if err := host.RegisterEventHook([]string{"alpha.ping"}, hook); err != nil {
		t.Fatalf("RegisterEventHook: %v", err)
	}

	// 16. Provider (forward-compatible sweep — host-side owner map + adapter).
	if err := host.RegisterProvider("alpha-provider", struct{}{}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	// Commit the plugin so UnloadPlugin finds it.
	host.mu.Lock()
	host.plugins[pluginID] = plug
	host.activePlugin = ""
	host.mu.Unlock()

	// Sanity checks pre-unload.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/plugins/alpha/ping", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != "pong" {
			t.Fatalf("pre-unload ping: status=%d body=%q", rr.Code, rr.Body.String())
		}
	}
	if cmds.commands["alpha-cmd"] != pluginID {
		t.Fatalf("command source mismatch: want %q got %q", pluginID, cmds.commands["alpha-cmd"])
	}
	if _, ok := tasks.backends["alpha-backend"]; !ok {
		t.Fatalf("task backend not registered")
	}

	// Unload.
	if err := host.UnloadPlugin(pluginID); err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}

	// Post-unload assertions — one per category.
	host.mu.RLock()
	defer host.mu.RUnlock()

	// 1. Envelope.
	if _, ok := host.envelopes["alpha-envelope"]; ok {
		t.Error("envelope survived unload")
	}
	// 2. UI component.
	if host.uiOwners["alpha-widget"] != "" {
		t.Error("ui component owner survived unload")
	}
	for _, c := range host.uiComponents {
		if c.ID == "alpha-widget" {
			t.Error("ui component survived unload")
		}
	}
	// 3. UI slot.
	if entries := host.slots["topbar"]; len(entries) > 0 {
		for _, e := range entries {
			if e.PluginID == pluginID {
				t.Error("slot entry survived unload")
			}
		}
	}
	// 4. Keybinding.
	if _, ok := host.keybindings["alpha-key"]; ok {
		t.Error("keybinding survived unload")
	}
	// 5. Filter.
	if host.filters.Len("alpha-filter") != 0 {
		t.Error("filter survived unload")
	}
	// 6. Connector.
	if _, ok := host.connectors["alpha-conn"]; ok {
		t.Error("connector survived unload")
	}
	// 7. Service.
	if _, ok := host.services["alpha-svc"]; ok {
		t.Error("service survived unload")
	}
	// Core service stays.
	if _, ok := host.services["tasks"]; !ok {
		t.Error("core tasks service was wrongly swept")
	}
	// 8. CLI adapter.
	if _, ok := host.services["cli-adapter:alpha-cli"]; ok {
		t.Error("cli adapter survived unload")
	}
	// 9. MCP.
	if mcp.removed[pluginID] != 1 {
		t.Errorf("mcp remove not called correctly: %+v", mcp.removed)
	}
	// 10. HTTP routes.
	if n := host.pluginMux.CountByPlugin(pluginID); n != 0 {
		t.Errorf("plugin http routes survived unload: %d", n)
	}
	// 11. CRUD handler.
	if _, ok := host.crudHandlers["alpha-resource"]; ok {
		t.Error("crud handler survived unload")
	}
	// 12. Command.
	if _, ok := cmds.commands["alpha-cmd"]; ok {
		t.Error("command survived unload")
	}
	if cmds.removed[pluginID] != 1 {
		t.Errorf("command remove count = %d, want 1", cmds.removed[pluginID])
	}
	// 13. Task backend.
	if _, ok := tasks.backends["alpha-backend"]; ok {
		t.Error("task backend survived unload")
	}
	if _, ok := tasks.backends["local"]; !ok {
		t.Error("local task backend was wrongly swept")
	}
	if _, ok := host.taskBackendOwners["alpha-backend"]; ok {
		t.Error("task backend owner entry survived unload")
	}
	// 14. Config schema (store-backed).
	if _, ok := host.configSchemaOwners[pluginID]; ok {
		t.Error("config schema owner entry survived unload")
	}
	settings, err := db.GetPluginSettings(context.Background(), pluginID)
	if err != nil {
		t.Errorf("GetPluginSettings after unload: %v", err)
	} else if len(settings.Schema) != 0 {
		t.Errorf("config schema survived unload: %+v", settings.Schema)
	}
	// 15. Event hook.
	if hooks := host.eventHooks["alpha.ping"]; len(hooks) > 0 {
		t.Errorf("event hook survived unload: %d entries", len(hooks))
	}
	// 16. Provider — host-side owner map and registry entry both cleared.
	if _, ok := host.providerOwners["alpha-provider"]; ok {
		t.Error("provider owner entry survived unload")
	}
	if _, ok := providers.providers["alpha-provider"]; ok {
		t.Error("provider survived unload")
	}

	// The core mux still has the forwarder; it now returns 404.
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/alpha/ping", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("post-unload ping: status=%d want 404", rr.Code)
	}

	// CRUD forwarder: route stays, handler returns 404 via withHandler fall-through.
	req2 := httptest.NewRequest(http.MethodGet, "/api/plugins/alpha-resource", nil)
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("post-unload crud list: status=%d want 404", rr2.Code)
	}
}

// stubProviderRegistry implements the "provider-registry" service contract
// used by Host.RegisterProvider (Register) plus providerUnregistrar
// (Unregister), mirroring go-providers v0.1.0+ *provider.Registry. Using a
// stub keeps this unit test isolated from the real registry while still
// exercising both the host-side owner map clear and the unregister branch
// of UnloadPlugin.
type stubProviderRegistry struct {
	providers map[string]interface{}
}

func newStubProviderRegistry() *stubProviderRegistry {
	return &stubProviderRegistry{providers: make(map[string]interface{})}
}

func (s *stubProviderRegistry) Register(name string, p interface{}) {
	s.providers[name] = p
}

func (s *stubProviderRegistry) Unregister(name string) bool {
	if _, ok := s.providers[name]; !ok {
		return false
	}
	delete(s.providers, name)
	return true
}

// dummyTaskBackend is a zero-method placeholder. The stubTaskService doesn't
// type-assert the backend (unlike the real task.Service), so any value works —
// including something that would fail the real implements-TaskBackend check.
// For the sweep test we only care that RegisterTaskBackend tags ownership and
// UnregisterBackend is called on unload.
type dummyTaskBackend struct{}
