package subprocess

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-plugin"
	_ "github.com/hollis-labs/plugin-sdk"
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
			// Post-B.10 LoadResult carries only SkippedRegistrations; every
			// declarative registration (commands, slots, components, etc.)
			// is yaml-authoritative and applied by the host from plugin.yaml.
			// The lifecycle assertion below verifies the runtime opt-out
			// payload round-trips.
			return &LoadResult{
				SkippedRegistrations: []SkippedRegistration{
					{Kind: "command", ID: "test-cmd", Reason: "missing api_key"},
				},
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

	// Verify the ack-only LoadResult round-tripped SkippedRegistrations.
	skipped := sp.SkippedRegistrations()
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped registration, got %d", len(skipped))
	}
	if skipped[0].Kind != "command" || skipped[0].ID != "test-cmd" {
		t.Errorf("skipped[0] = %+v", skipped[0])
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

// TestBuildInitParams verifies that the host-side InitParams assembly
// populates the v0.1.2 DataDir/CacheDir/LogLevel fields with absolute
// paths rooted under the user's brand directory and creates those
// directories on disk. HOME is redirected to a t.TempDir to keep the
// test hermetic.
func TestBuildInitParams(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg := map[string]string{"k": "v"}
	ip, err := buildInitParams("/plugins/example", "example", cfg)
	if err != nil {
		t.Fatalf("buildInitParams: %v", err)
	}

	if ip.PluginDir != "/plugins/example" {
		t.Errorf("PluginDir = %q, want /plugins/example", ip.PluginDir)
	}
	if ip.DataDir == "" {
		t.Fatal("DataDir unset")
	}
	if ip.CacheDir == "" {
		t.Fatal("CacheDir unset")
	}
	if ip.LogLevel == "" {
		t.Fatal("LogLevel unset")
	}
	switch ip.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		t.Errorf("LogLevel = %q, want one of debug/info/warn/error", ip.LogLevel)
	}
	wantData := filepath.Join(tmpHome, ".nanite", "plugin-data", "example")
	wantCache := filepath.Join(tmpHome, ".nanite", "plugin-cache", "example")
	if ip.DataDir != wantData {
		t.Errorf("DataDir = %q, want %q", ip.DataDir, wantData)
	}
	if ip.CacheDir != wantCache {
		t.Errorf("CacheDir = %q, want %q", ip.CacheDir, wantCache)
	}
	if fi, err := os.Stat(ip.DataDir); err != nil || !fi.IsDir() {
		t.Errorf("DataDir not created: err=%v", err)
	}
	if fi, err := os.Stat(ip.CacheDir); err != nil || !fi.IsDir() {
		t.Errorf("CacheDir not created: err=%v", err)
	}
	if !filepath.IsAbs(ip.DataDir) || !filepath.IsAbs(ip.CacheDir) {
		t.Error("expected absolute paths")
	}
	if ip.HostInfo.Protocol != ProtocolVersion {
		t.Errorf("HostInfo.Protocol = %d, want %d", ip.HostInfo.Protocol, ProtocolVersion)
	}
	if len(ip.Config) != 1 || ip.Config["k"] != "v" {
		t.Errorf("Config not passed through: %v", ip.Config)
	}
}

// TestBuildInitParams_NoID covers the legacy path where the host has no
// pre-handshake plugin id. DataDir/CacheDir stay empty so the plugin
// falls back to v0.1.2 ResolvedDataDir/ResolvedCacheDir semantics.
func TestBuildInitParams_NoID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ip, err := buildInitParams("/plugins/x", "", nil)
	if err != nil {
		t.Fatalf("buildInitParams: %v", err)
	}
	if ip.DataDir != "" {
		t.Errorf("DataDir = %q, want empty", ip.DataDir)
	}
	if ip.CacheDir != "" {
		t.Errorf("CacheDir = %q, want empty", ip.CacheDir)
	}
	if ip.LogLevel == "" {
		t.Error("LogLevel should still be populated without pluginID")
	}
}
