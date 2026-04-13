package subprocess

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/hollis-labs/go-plugin"
)

// fakeHost implements plugin.Host for testing SubprocessPlugin.registerManifest.
type fakeHost struct {
	components   []plugin.UIComponent
	eventHooks   []plugin.EventHook
	crud         map[string]plugin.CRUDHandler
	configSchema []plugin.ConfigFieldDef
	logger       plugin.Logger
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		crud:   make(map[string]plugin.CRUDHandler),
		logger: &nopLogger{},
	}
}

func (h *fakeHost) GetPlugin(id string) (plugin.Plugin, bool)             { return nil, false }
func (h *fakeHost) RegisterCRUDHandler(rt string, hh plugin.CRUDHandler) error {
	h.crud[rt] = hh
	return nil
}
func (h *fakeHost) RegisterEventHook(types []string, hook plugin.EventHook) error {
	h.eventHooks = append(h.eventHooks, hook)
	return nil
}
func (h *fakeHost) RegisterUIComponent(c plugin.UIComponent) error {
	h.components = append(h.components, c)
	return nil
}
func (h *fakeHost) GetService(name string) (interface{}, error) { return nil, nil }
func (h *fakeHost) GetConfig(key string) (string, error)        { return "", nil }
func (h *fakeHost) SetConfig(key, value string) error           { return nil }
func (h *fakeHost) RegisterConfigSchema(fields []plugin.ConfigFieldDef) error {
	h.configSchema = fields
	return nil
}
func (h *fakeHost) RegisterConnector(name string, c plugin.Connector) error { return nil }
func (h *fakeHost) RegisterProvider(name string, p interface{}) error       { return nil }
func (h *fakeHost) RegisterCLIAdapter(name string, a interface{}) error     { return nil }
func (h *fakeHost) RegisterCommand(cmd plugin.SlashCommandDef) error        { return nil }
func (h *fakeHost) RegisterSlot(entry plugin.UISlotEntry) error             { return nil }
func (h *fakeHost) RegisterKeybinding(kb plugin.KeybindingDef) error        { return nil }
func (h *fakeHost) Logger() plugin.Logger        { return h.logger }
func (h *fakeHost) Context() context.Context      { return context.Background() }

type nopLogger struct{}

func (l *nopLogger) Debug(msg string, kv ...interface{}) {}
func (l *nopLogger) Info(msg string, kv ...interface{})  {}
func (l *nopLogger) Warn(msg string, kv ...interface{})  {}
func (l *nopLogger) Error(msg string, kv ...interface{}) {}
func (l *nopLogger) With(kv ...interface{}) plugin.Logger { return l }

// TestSubprocessPlugin_LoadLifecycle tests the full init → load → register → unload
// lifecycle using in-process pipes (no real subprocess).
func TestSubprocessPlugin_LoadLifecycle(t *testing.T) {
	// Set up pipes: host side ↔ plugin side.
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()

	// Mock plugin handlers.
	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		MethodInit: func(params json.RawMessage) (any, *RPCError) {
			return &InitResult{
				ID:          "test-plugin",
				Name:        "Test Plugin",
				Version:     "1.0.0",
				Description: "A test subprocess plugin",
				Protocol:    ProtocolVersion,
			}, nil
		},
		MethodLoad: func(_ json.RawMessage) (any, *RPCError) {
			return &LoadResult{
				Commands: []CommandRegistration{
					{
						Name:        "test-cmd",
						Description: "A test command",
						Category:    "test",
					},
				},
				Slots: []UISlotEntry{
					{
						ID:       "test-slot",
						PluginID: "test-plugin",
						Slot:     "settings-tab",
						Label:    "Test Settings",
					},
				},
				Components: []ComponentRegistration{
					{
						ID:   "test-widget",
						Type: plugin.UIComponentTypeWidget,
						Name: "Test Widget",
					},
				},
				Keybindings: []KeybindingDef{
					{
						ID:          "test.action",
						Key:         "mod+shift+t",
						Action:      "command",
						ActionValue: "test-cmd",
						Label:       "Run Test Command",
					},
				},
				EventSubscriptions: []string{"message.sent", "session.start"},
				CRUDResources:      []string{"test-items"},
			}, nil
		},
		MethodUnload: func(_ json.RawMessage) (any, *RPCError) {
			return map[string]bool{"ok": true}, nil
		},
		MethodHealth: func(_ json.RawMessage) (any, *RPCError) {
			return &HealthResult{OK: true}, nil
		},
		MethodCommandExecute: func(params json.RawMessage) (any, *RPCError) {
			var p CommandExecParams
			json.Unmarshal(params, &p)
			return &CommandExecResult{
				Action:  "message",
				Content: "executed: " + p.Name + " " + p.Args,
			}, nil
		},
	}

	go mockPlugin(hostToPluginR, pluginToHostW, handlers)

	// Create a SubprocessPlugin that uses the pipes directly (bypass Manager).
	sp := &SubprocessPlugin{
		pluginDir: "/tmp/test-plugin",
		config:    map[string]string{"key": "value"},
		status:    plugin.PluginStatus{Enabled: true},
	}

	// Manually set up the transport (normally Manager does this).
	transport := NewTransport(pluginToHostR, hostToPluginW)

	// Perform init handshake.
	ctx := context.Background()
	initResult, err := CallResult[InitResult](transport, ctx, MethodInit, &InitParams{
		PluginDir: sp.pluginDir,
		Config:    sp.config,
		HostInfo:  HostInfo{Version: "test", Protocol: ProtocolVersion},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	sp.id = initResult.ID
	sp.name = initResult.Name
	sp.version = initResult.Version
	sp.description = initResult.Description

	// Verify identity.
	if sp.ID() != "test-plugin" {
		t.Errorf("expected ID 'test-plugin', got %q", sp.ID())
	}
	if sp.Name() != "Test Plugin" {
		t.Errorf("expected Name 'Test Plugin', got %q", sp.Name())
	}

	// Perform load handshake.
	loadResult, err := CallResult[LoadResult](transport, ctx, MethodLoad, &LoadParams{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	sp.manifest = loadResult
	sp.transport = transport

	// Register manifest with a fake host (only generic SDK registrations).
	host := newFakeHost()
	if err := sp.registerManifest(host, loadResult, transport); err != nil {
		t.Fatalf("registerManifest: %v", err)
	}

	// Verify generic SDK registrations.
	if len(host.components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(host.components))
	}
	if host.components[0].ID != "test-widget" {
		t.Errorf("expected component 'test-widget', got %q", host.components[0].ID)
	}

	if len(host.eventHooks) != 1 {
		t.Fatalf("expected 1 event hook, got %d", len(host.eventHooks))
	}
	hookTypes := host.eventHooks[0].EventTypes()
	if len(hookTypes) != 2 || hookTypes[0] != "message.sent" {
		t.Errorf("expected event types [message.sent, session.start], got %v", hookTypes)
	}

	if _, ok := host.crud["test-items"]; !ok {
		t.Error("expected CRUD handler for 'test-items'")
	}

	// Verify Nanite-specific registrations are in the manifest (for parent to process).
	manifest := sp.Manifest()
	if len(manifest.Commands) != 1 || manifest.Commands[0].Name != "test-cmd" {
		t.Errorf("expected command 'test-cmd' in manifest")
	}
	if len(manifest.Slots) != 1 || manifest.Slots[0].ID != "test-slot" {
		t.Errorf("expected slot 'test-slot' in manifest")
	}
	if len(manifest.Keybindings) != 1 || manifest.Keybindings[0].ID != "test.action" {
		t.Errorf("expected keybinding 'test.action' in manifest")
	}

	// Test command execution through MakeCommandHandler.
	cmdHandler := sp.MakeCommandHandler("test-cmd")
	cmdResult, err := cmdHandler(ctx, "session-123", "foo bar")
	if err != nil {
		t.Fatalf("command execute: %v", err)
	}
	if cmdResult["action"] != "message" {
		t.Errorf("expected action 'message', got %v", cmdResult["action"])
	}
	if cmdResult["content"] != "executed: test-cmd foo bar" {
		t.Errorf("expected content 'executed: test-cmd foo bar', got %v", cmdResult["content"])
	}

	// Clean up.
	hostToPluginW.Close()
	pluginToHostW.Close()
}

// TestCheckProtocolVersion verifies the handshake protocol-version gate
// rejects any plugin whose reported version differs from the host's.
func TestCheckProtocolVersion(t *testing.T) {
	// Matching version is accepted.
	if err := checkProtocolVersion(ProtocolVersion); err != nil {
		t.Errorf("expected nil for matching protocol version, got %v", err)
	}

	// Mismatched versions are rejected.
	wrongVersions := []int{0, ProtocolVersion + 1, 999}
	for _, v := range wrongVersions {
		err := checkProtocolVersion(v)
		if err == nil {
			t.Errorf("expected error for protocol version %d, got nil", v)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "protocol version mismatch") {
			t.Errorf("expected 'protocol version mismatch' in error, got %q", msg)
		}
	}
}

// TestSubprocessPlugin_InitRejectsWrongProtocol spawns a mock plugin that
// returns a deliberately-wrong protocol version from plugin/init and asserts
// that the handshake gate rejects it. This covers the regression where Load
// read initResult.Protocol without enforcing it against ProtocolVersion.
func TestSubprocessPlugin_InitRejectsWrongProtocol(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()

	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		MethodInit: func(params json.RawMessage) (any, *RPCError) {
			// Deliberately wrong: one higher than the host's.
			return &InitResult{
				ID:          "test-plugin",
				Name:        "Test Plugin",
				Version:     "1.0.0",
				Description: "Reports mismatched protocol version",
				Protocol:    ProtocolVersion + 1,
			}, nil
		},
	}

	go mockPlugin(hostToPluginR, pluginToHostW, handlers)
	defer func() {
		hostToPluginW.Close()
		pluginToHostW.Close()
	}()

	transport := NewTransport(pluginToHostR, hostToPluginW)
	ctx := context.Background()

	initResult, err := CallResult[InitResult](transport, ctx, MethodInit, &InitParams{
		PluginDir: "/tmp/test-plugin",
		Config:    map[string]string{},
		HostInfo:  HostInfo{Version: "test", Protocol: ProtocolVersion},
	})
	if err != nil {
		t.Fatalf("init RPC failed: %v", err)
	}

	// The gate the fix introduces must reject this.
	checkErr := checkProtocolVersion(initResult.Protocol)
	if checkErr == nil {
		t.Fatalf("expected protocol-version mismatch error, got nil (got=%d want=%d)", initResult.Protocol, ProtocolVersion)
	}
	if !strings.Contains(checkErr.Error(), "protocol version mismatch") {
		t.Errorf("expected error message to mention 'protocol version mismatch', got %q", checkErr.Error())
	}
}

// TestMapRPCError verifies JSON-RPC error codes map to plugin.PluginError types.
func TestMapRPCError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantNil  bool
	}{
		{"nil", nil, 0, true},
		{"not found", &RPCError{Code: ErrCodeNotFound, Message: "gone"}, 404, false},
		{"conflict", &RPCError{Code: ErrCodeConflict, Message: "dup"}, 409, false},
		{"validation", &RPCError{Code: ErrCodeValidation, Message: "bad"}, 422, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapRPCError(tt.err)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			pe, ok := result.(*plugin.PluginError)
			if !ok {
				t.Fatalf("expected *plugin.PluginError, got %T", result)
			}
			if pe.Code != tt.wantCode {
				t.Errorf("expected code %d, got %d", tt.wantCode, pe.Code)
			}
		})
	}
}

// TestRingBuffer verifies the stderr ring buffer.
func TestRingBuffer(t *testing.T) {
	rb := &ringBuffer{buf: make([]byte, 8)}

	// Write less than capacity.
	rb.Write([]byte("hello"))
	if s := rb.String(); s != "hello" {
		t.Errorf("expected 'hello', got %q", s)
	}

	// Write wrapping around.
	rb.Write([]byte("worldXYZ"))
	s := rb.String()
	if len(s) != 8 {
		t.Errorf("expected length 8, got %d: %q", len(s), s)
	}
}
