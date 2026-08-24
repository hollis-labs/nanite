package plugin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk"
)

// TestPlugin implements the plugin.Plugin interface for testing
type TestPlugin struct {
	id          string
	name        string
	version     string
	description string
	deps        []string
	loaded      bool
}

func (p *TestPlugin) ID() string             { return p.id }
func (p *TestPlugin) Name() string           { return p.name }
func (p *TestPlugin) Version() string        { return p.version }
func (p *TestPlugin) Description() string    { return p.description }
func (p *TestPlugin) Dependencies() []string { return p.deps }

func (p *TestPlugin) Load(host plugin.Host) error {
	p.loaded = true
	return nil
}

func (p *TestPlugin) Unload() error {
	p.loaded = false
	return nil
}

func (p *TestPlugin) Status() plugin.PluginStatus {
	return plugin.PluginStatus{
		Loaded:   p.loaded,
		Enabled:  p.loaded,
		LoadedAt: time.Now(),
	}
}

// TestEventHook implements plugin.EventHook for testing
type TestEventHook struct {
	eventTypes []string
	lastEvent  plugin.Event
	callCount  int
}

func (h *TestEventHook) Handle(ctx context.Context, event plugin.Event) error {
	h.lastEvent = event
	h.callCount++
	return nil
}

func (h *TestEventHook) EventTypes() []string {
	return h.eventTypes
}

func (h *TestEventHook) PluginID() string { return "test-plugin" }

// TestCRUDHandler implements plugin.CRUDHandler for testing
type TestCRUDHandler struct {
	resources map[string]interface{}
}

func (h *TestCRUDHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
	return resource, nil
}

func (h *TestCRUDHandler) Read(ctx context.Context, id string) (interface{}, error) {
	return h.resources[id], nil
}

func (h *TestCRUDHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
	h.resources[id] = resource
	return resource, nil
}

func (h *TestCRUDHandler) Delete(ctx context.Context, id string) error {
	delete(h.resources, id)
	return nil
}

func (h *TestCRUDHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
	result := make([]interface{}, 0, len(h.resources))
	for _, resource := range h.resources {
		result = append(result, resource)
	}
	return result, nil
}

func TestNewHost(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	if host == nil {
		t.Fatal("NewHost returned nil")
	}

	if host.logger != logger {
		t.Error("Logger not set correctly")
	}

	if host.router != mux {
		t.Error("Router not set correctly")
	}
}

func TestSetRouter(t *testing.T) {
	logger := NewLogger("test")
	host := NewHost(nil, logger)
	mux := http.NewServeMux()

	host.SetRouter(mux)

	if host.router != mux {
		t.Error("SetRouter did not set router correctly")
	}
}

func TestLoadPlugin(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	testPlugin := &TestPlugin{
		id:          "test-plugin",
		name:        "Test Plugin",
		version:     "1.0.0",
		description: "A test plugin",
		deps:        []string{},
		loaded:      false,
	}

	err := host.LoadPlugin(testPlugin)
	if err != nil {
		t.Fatalf("LoadPlugin failed: %v", err)
	}

	if !testPlugin.loaded {
		t.Error("Plugin was not loaded")
	}

	// Test getting the plugin back
	retrieved, exists := host.GetPlugin("test-plugin")
	if !exists {
		t.Error("Plugin not found after loading")
	}

	if retrieved != testPlugin {
		t.Error("Retrieved plugin is different from loaded plugin")
	}
}

// blockingReentrantUnloadPlugin pauses during Unload after re-entering an
// ordinary Host method. It exposes the lifecycle window needed to prove a
// dependent plugin cannot begin loading until removal is complete.
type blockingReentrantUnloadPlugin struct {
	TestPlugin
	host          *Host
	unloadEntered chan struct{}
	allowUnload   chan struct{}
}

func (p *blockingReentrantUnloadPlugin) Unload() error {
	// lifecycleMu may be held here, but h.mu must not be: plugin callbacks
	// are allowed to re-enter ordinary Host operations.
	_ = p.host.ListPlugins()
	close(p.unloadEntered)
	<-p.allowUnload
	p.loaded = false
	return nil
}

type observingLoadPlugin struct {
	TestPlugin
	loadEntered chan struct{}
}

func (p *observingLoadPlugin) Load(plugin.Host) error {
	close(p.loadEntered)
	p.loaded = true
	return nil
}

func TestPluginLifecycle_DependencyCheckSerializedWithUnload(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	a := &blockingReentrantUnloadPlugin{
		TestPlugin:    TestPlugin{id: "a", name: "A", version: "1.0.0"},
		host:          host,
		unloadEntered: make(chan struct{}),
		allowUnload:   make(chan struct{}),
	}
	if err := host.LoadPlugin(a); err != nil {
		t.Fatalf("LoadPlugin(A): %v", err)
	}

	unloadDone := make(chan error, 1)
	go func() { unloadDone <- host.UnloadPlugin("a") }()
	t.Cleanup(func() {
		select {
		case <-a.allowUnload:
		default:
			close(a.allowUnload)
		}
	})
	select {
	case <-a.unloadEntered:
		// Reaching this point also proves A's reentrant ListPlugins callback
		// did not deadlock while the lifecycle transaction was held.
	case <-time.After(time.Second):
		t.Fatal("UnloadPlugin(A) did not enter reentrant callback")
	}

	b := &observingLoadPlugin{
		TestPlugin:  TestPlugin{id: "b", name: "B", version: "1.0.0", deps: []string{"a"}},
		loadEntered: make(chan struct{}),
	}
	loadDone := make(chan error, 1)
	loadAttempted := make(chan struct{})
	go func() {
		close(loadAttempted)
		loadDone <- host.LoadPlugin(b)
	}()
	<-loadAttempted

	select {
	case <-b.loadEntered:
		t.Fatal("dependent B entered Load while A was unloading")
	case err := <-loadDone:
		t.Fatalf("LoadPlugin(B) returned before A completed unload: %v", err)
	case <-time.After(100 * time.Millisecond):
		// Expected: B waits on lifecycleMu for A's full unload transaction.
	}

	close(a.allowUnload)
	if err := <-unloadDone; err != nil {
		t.Fatalf("UnloadPlugin(A): %v", err)
	}
	err := <-loadDone
	if err == nil || !strings.Contains(err.Error(), `depends on "a" which is not loaded`) {
		t.Fatalf("LoadPlugin(B) after A removal = %v, want missing-dependency error", err)
	}
	select {
	case <-b.loadEntered:
		t.Fatal("dependent B entered Load despite failed dependency check")
	default:
	}
}

func TestRegisterEventHook(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	hook := &TestEventHook{
		eventTypes: []string{"test.event", "another.event"},
	}

	err := host.RegisterEventHook([]string{"test.event"}, hook)
	if err != nil {
		t.Fatalf("RegisterEventHook failed: %v", err)
	}

	// Test emitting an event
	event := NewEvent("test.event", "test", EventData{})
	host.EmitEvent(event)

	// Give some time for event processing
	time.Sleep(100 * time.Millisecond)

	if hook.callCount != 1 {
		t.Errorf("Expected hook to be called once, got %d", hook.callCount)
	}

	if hook.lastEvent.Type != "test.event" {
		t.Errorf("Expected event type 'test.event', got '%s'", hook.lastEvent.Type)
	}
}

func TestRegisterCRUDHandler(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	handler := &TestCRUDHandler{
		resources: make(map[string]interface{}),
	}

	err := host.RegisterCRUDHandler("testresource", handler)
	if err != nil {
		t.Fatalf("RegisterCRUDHandler failed: %v", err)
	}

	// Verify the handler was registered
	handlers := host.GetCRUDHandlers()
	if len(handlers) != 1 {
		t.Errorf("Expected 1 CRUD handler, got %d", len(handlers))
	}

	retrievedHandler, exists := handlers["testresource"]
	if !exists {
		t.Error("CRUD handler not found after registration")
	}

	if retrievedHandler != handler {
		t.Error("Retrieved CRUD handler is different from registered handler")
	}
}

func TestRegisterUIComponent(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	component := plugin.UIComponent{
		ID:          "test-widget",
		Type:        plugin.UIComponentTypeWidget,
		Name:        "Test Widget",
		Description: "A test widget",
		Props:       map[string]interface{}{"color": "blue"},
	}

	err := host.RegisterUIComponent(component)
	if err != nil {
		t.Fatalf("RegisterUIComponent failed: %v", err)
	}

	// Verify the component was registered
	components := host.GetUIComponents()
	if len(components) != 1 {
		t.Errorf("Expected 1 UI component, got %d", len(components))
	}

	if components[0].ID != "test-widget" {
		t.Errorf("Expected component ID 'test-widget', got '%s'", components[0].ID)
	}
}

func TestRegisterService(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	testService := "test-service-instance"
	host.RegisterService("test-service", testService)

	// Test getting the service
	retrieved, err := host.GetService("test-service")
	if err != nil {
		t.Fatalf("GetService failed: %v", err)
	}

	if retrieved != testService {
		t.Error("Retrieved service is different from registered service")
	}

	// Test getting non-existent service
	_, err = host.GetService("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent service")
	}
}

func TestEventCatalog(t *testing.T) {
	// Test that all event constants are defined
	events := []string{
		EventSessionStart,
		EventSessionEnd,
		EventAgentSwitched,
		EventAgentLoaded,
		EventMessageSent,
		EventMessageReceived,
		EventMessageDeleted,
		EventScopeChanged,
		EventToolCalled,
		EventToolFailed,
		EventToolComplete,
		EventEnvelopeRendered,
		EventWidgetLoaded,
		EventActionTriggered,
		EventWorkflowStarted,
		EventWorkflowComplete,
		EventWorkflowFailed,
	}

	for _, eventType := range events {
		if eventType == "" {
			t.Errorf("Event constant is empty")
		}
	}

	// Test event creation
	data := EventData{
		SessionID: "test-session",
		AgentID:   "test-agent",
		Mode:      "chat",
	}

	event := NewEvent(EventSessionStart, "nanite", data)
	if event.Type != EventSessionStart {
		t.Errorf("Expected event type '%s', got '%s'", EventSessionStart, event.Type)
	}

	if event.Source != "nanite" {
		t.Errorf("Expected event source 'nanite', got '%s'", event.Source)
	}

	if event.SessionID != "test-session" {
		t.Errorf("Expected session ID 'test-session', got '%s'", event.SessionID)
	}
}

// reentrantUnloadPlugin is a test plugin whose Unload() calls back into a
// host method that takes the host mutex (ListPlugins acquires h.mu.RLock).
// Before the Shutdown deadlock fix, Shutdown held h.mu across Unload, so the
// re-entrant RLock attempt blocked forever on the held write lock.
type reentrantUnloadPlugin struct {
	TestPlugin
	host       *Host
	unloadDone chan struct{}
}

func (p *reentrantUnloadPlugin) Load(host plugin.Host) error {
	p.loaded = true
	return nil
}

func (p *reentrantUnloadPlugin) Unload() error {
	// Re-enter the host from within Unload. If Shutdown is holding h.mu,
	// this RLock acquisition will deadlock.
	_ = p.host.ListPlugins()
	p.loaded = false
	close(p.unloadDone)
	return nil
}

// TestShutdown_UnloadReentrancyDoesNotDeadlock regresses the Host.Shutdown
// deadlock where h.mu was held across p.Unload(). A plugin whose Unload
// re-enters the host (common pattern: unregister routes, query state) would
// hang the process on exit. The fix snapshots the plugin list under lock,
// releases the lock, and iterates Unload() without it held.
//
// Test strategy: register a plugin whose Unload() calls a host method that
// requires h.mu, then run Shutdown in a goroutine with a timeout. Without
// the fix, the goroutine never completes and the test fails the deadline.
func TestShutdown_UnloadReentrancyDoesNotDeadlock(t *testing.T) {
	logger := NewLogger("test")
	mux := http.NewServeMux()
	host := NewHost(mux, logger)

	p := &reentrantUnloadPlugin{
		TestPlugin: TestPlugin{
			id:      "reentrant-unload",
			name:    "Reentrant Unload",
			version: "0.0.1",
		},
		host:       host,
		unloadDone: make(chan struct{}),
	}

	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- host.Shutdown()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown deadlocked (3s timeout) — Unload re-entered host method under h.mu")
	}

	select {
	case <-p.unloadDone:
	default:
		t.Fatal("reentrant Unload did not complete")
	}
}
