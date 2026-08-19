package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	goplugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
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
				{Type: "env-plug-card", Component: "EnvPlugCard", Version: 1, Schema: "schemas/card.json"},
			},
		},
	}
	if err := applyManifestRegistrations(host, m, p, ""); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	entries := host.GetEnvelopes()
	if len(entries) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(entries))
	}
	e := entries[0]
	if e.Type != "env-plug-card" || e.PluginID != "env-plug" || e.Component != "EnvPlugCard" || e.Version != 1 || e.SchemaPath != "schemas/card.json" {
		t.Errorf("unexpected envelope entry: %+v", e)
	}
	if !seen["env-plug-card"] {
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
	if err := applyManifestRegistrations(host, m, p, ""); err != nil {
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
	if err := applyManifestRegistrations(host, m, p, ""); err != nil {
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
	if err := applyManifestRegistrations(host, m, p, ""); err != nil {
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

	// p is a builtin (fakePlugin, not *subprocess.SubprocessPlugin). Commands,
	// Events, Crud, HttpRoutes, and McpServers are all subprocess-only
	// registration paths — a builtin is expected to register those directly
	// from its own Load(), so applyManifestRegistrations logs and no-ops for
	// each rather than erroring. AgentProfiles remains genuinely deferred
	// (no registration path exists yet for any plugin kind, builtin or
	// subprocess) as of this test. See TestApplyManifestRegistrations_Crud_Subprocess
	// below for the real (non-skipped) subprocess-plugin crud wiring path.
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
	if err := applyManifestRegistrations(host, m, p, ""); err != nil {
		t.Fatalf("applyManifestRegistrations with deferred categories returned error: %v", err)
	}
}

// TestApplyManifestRegistrations_Crud_Subprocess proves the registers.crud[]
// manifest path end to end (Phase 5 item 05,
// TASKS/phase-5/05-develop-registers-panels-and-crud.md): no real plugin
// declares registers.crud[] yet, so this is the minimal test-plugin consumer
// the task calls for. A subprocess plugin declares a "things" resource;
// applyManifestRegistrations wires it into the host's generic CRUD router
// (Host.RegisterCRUDHandler); the resulting REST routes are exercised through
// the real *http.ServeMux (the same forwarder-to-pluginMux path production
// traffic uses) and round-trip over JSON-RPC to an in-process mock "plugin"
// that answers MethodCRUDList/Create/Read/Update/Delete — the same technique
// TestNewSubprocessHTTPHandler above uses for http_routes.
func TestApplyManifestRegistrations_Crud_Subprocess(t *testing.T) {
	mux := http.NewServeMux()
	host := NewHost(mux, NewLogger("test"))

	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	t.Cleanup(func() {
		hostToPluginW.Close()
		pluginToHostW.Close()
	})

	// Minimal in-process "plugin": answers the five CRUD JSON-RPC methods
	// with canned data instead of a real spawned subprocess.
	go func() {
		br := make([]byte, 0, 8192)
		buf := make([]byte, 4096)
		for {
			n, err := hostToPluginR.Read(buf)
			if n > 0 {
				br = append(br, buf[:n]...)
				for {
					i := bytes.IndexByte(br, '\n')
					if i < 0 {
						break
					}
					line := br[:i]
					br = br[i+1:]

					var req struct {
						ID     int64           `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					_ = json.Unmarshal(line, &req)

					var params subprocess.CRUDParams
					_ = json.Unmarshal(req.Params, &params)

					var result interface{}
					switch req.Method {
					case subprocess.MethodCRUDList:
						result = subprocess.CRUDListResult{
							Items: []json.RawMessage{[]byte(`{"id":"1","name":"widget"}`)},
						}
					case subprocess.MethodCRUDCreate:
						name, _ := params.Data["name"].(string)
						result = subprocess.CRUDResult{Data: json.RawMessage(`{"id":"2","name":"` + name + `"}`)}
					case subprocess.MethodCRUDRead:
						result = subprocess.CRUDResult{Data: json.RawMessage(`{"id":"` + params.ID + `","name":"widget"}`)}
					case subprocess.MethodCRUDUpdate:
						result = subprocess.CRUDResult{Data: json.RawMessage(`{"id":"` + params.ID + `","name":"updated"}`)}
					case subprocess.MethodCRUDDelete:
						result = json.RawMessage(`null`)
					default:
						result = map[string]any{}
					}

					resp := map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"result":  result,
					}
					out, _ := json.Marshal(resp)
					out = append(out, '\n')
					_, _ = pluginToHostW.Write(out)
				}
			}
			if err != nil {
				return
			}
		}
	}()

	transport := subprocess.NewTransport(pluginToHostR, hostToPluginW)
	sp := subprocess.NewSubprocessPluginForTest("crud-plug", transport)

	m := &PluginManifest{
		Name: "crud-plug",
		Registers: ManifestRegisters{
			Crud: []CRUDRegistration{{Resource: "things", Methods: []string{"list", "create", "read", "update", "delete"}}},
		},
	}
	if err := applyManifestRegistrations(host, m, sp, ""); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	handlers := host.GetCRUDHandlers()
	if _, ok := handlers["things"]; !ok {
		t.Fatalf("expected a CRUD handler registered for resource %q, got %+v", "things", handlers)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	doReq := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r = r.WithContext(ctx)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}

	// List: GET /api/plugins/things -> MethodCRUDList.
	if rec := doReq(http.MethodGet, "/api/plugins/things", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "widget") {
		t.Fatalf("list: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Create: POST /api/plugins/things -> MethodCRUDCreate.
	if rec := doReq(http.MethodPost, "/api/plugins/things", `{"name":"gadget"}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "gadget") {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Read: GET /api/plugins/things/42 -> MethodCRUDRead. Also proves the
	// double-mux forwarder (core *http.ServeMux -> host.pluginMux's inner
	// *http.ServeMux) preserves Go 1.22 {id} path-value extraction.
	if rec := doReq(http.MethodGet, "/api/plugins/things/42", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"42"`) {
		t.Fatalf("read: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Update: PUT /api/plugins/things/42 -> MethodCRUDUpdate.
	if rec := doReq(http.MethodPut, "/api/plugins/things/42", `{"name":"changed"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "updated") {
		t.Fatalf("update: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Delete: DELETE /api/plugins/things/42 -> MethodCRUDDelete.
	if rec := doReq(http.MethodDelete, "/api/plugins/things/42", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestApplyManifestRegistrations_EnvelopeOwnershipCollision(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	SetEnvelopeTypeRegistrar(nil)

	p1 := &fakePlugin{id: "plug-a"}
	p2 := &fakePlugin{id: "plug-b"}

	m1 := &PluginManifest{Name: p1.id, Registers: ManifestRegisters{Envelopes: []EnvelopeRegistration{{Type: "shared", Component: "A", Version: 1}}}}
	if err := applyManifestRegistrations(host, m1, p1, ""); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	m2 := &PluginManifest{Name: p2.id, Registers: ManifestRegisters{Envelopes: []EnvelopeRegistration{{Type: "shared", Component: "B", Version: 1}}}}
	if err := applyManifestRegistrations(host, m2, p2, ""); err == nil {
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

// TestNewSubprocessHTTPHandler verifies the B.10 http_routes proxy:
//   - bodies are capped (MaxBytesReader → 413 on overflow)
//   - sensitive headers (Authorization, Cookie, ...) are stripped
//   - multi-value headers are flattened with ", "
//   - canned HTTPResponse headers/status/body are written back
//
// Uses in-process pipes and a tiny JSON-RPC responder to stand in for the
// subprocess — no real plugin binary is spawned.
func TestNewSubprocessHTTPHandler(t *testing.T) {
	// Set up pipes for a subprocess.Transport pair.
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	t.Cleanup(func() {
		hostToPluginW.Close()
		pluginToHostW.Close()
	})

	// Capture what the "plugin" sees so we can assert against it after the
	// handler returns.
	var gotReq subprocess.HTTPRequest
	gotReqCh := make(chan struct{}, 1)

	// Minimal JSON-RPC responder — reads one request from hostToPluginR,
	// writes a canned HTTPResponse back to pluginToHostW.
	go func() {
		br := make([]byte, 0, 8192)
		buf := make([]byte, 4096)
		for {
			n, err := hostToPluginR.Read(buf)
			if n > 0 {
				br = append(br, buf[:n]...)
				if i := bytes.IndexByte(br, '\n'); i >= 0 {
					line := br[:i]
					var req struct {
						ID     int64           `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					_ = json.Unmarshal(line, &req)
					_ = json.Unmarshal(req.Params, &gotReq)
					gotReqCh <- struct{}{}
					resp := map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"result": subprocess.HTTPResponse{
							Status:  201,
							Headers: map[string]string{"Content-Type": "application/json", "X-Plugin": "ok"},
							Body:    []byte(`{"ok":true}`),
						},
					}
					out, _ := json.Marshal(resp)
					out = append(out, '\n')
					pluginToHostW.Write(out)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	transport := subprocess.NewTransport(pluginToHostR, hostToPluginW)
	handler := newSubprocessHTTPHandler(transport, "my-handler")

	// --- Case 1: happy path — sensitive headers stripped, multi-values flattened.
	body := []byte(`{"hello":"world"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/plugin/foo?x=1", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer secret-token")
	r.Header.Set("Cookie", "session=abc")
	r.Header.Set("X-Api-Key", "key")
	r.Header.Add("X-Multi", "a")
	r.Header.Add("X-Multi", "b")
	// Respect the request context so the transport read doesn't stall forever.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	select {
	case <-gotReqCh:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin never received the request")
	}

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("X-Plugin"); got != "ok" {
		t.Errorf("X-Plugin = %q, want ok", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Errorf("body = %q, want {\"ok\":true}", got)
	}

	// Params forwarded to the plugin: sensitive headers must be absent,
	// multi-value header joined, and the body echoed through.
	if gotReq.Method != http.MethodPost {
		t.Errorf("forwarded method = %q", gotReq.Method)
	}
	if gotReq.Path != "/api/plugin/foo" {
		t.Errorf("forwarded path = %q", gotReq.Path)
	}
	if gotReq.Query["x"] != "1" {
		t.Errorf("forwarded query = %v", gotReq.Query)
	}
	for _, banned := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		if _, ok := gotReq.Headers[banned]; ok {
			t.Errorf("sensitive header %q was forwarded: %q", banned, gotReq.Headers[banned])
		}
	}
	if gotReq.Headers["X-Multi"] != "a, b" {
		t.Errorf("multi-value header not joined: %q", gotReq.Headers["X-Multi"])
	}
	if !bytes.Equal(gotReq.Body, body) {
		t.Errorf("forwarded body = %q, want %q", gotReq.Body, body)
	}
}

// TestNewSubprocessHTTPHandler_BodyTooLarge verifies the MaxBytesReader cap
// translates an oversized upload into a 413 without calling the plugin.
func TestNewSubprocessHTTPHandler_BodyTooLarge(t *testing.T) {
	// Build a transport that will never be called — if it is, the test
	// fails by timeout on the plugin side. We use closed pipes so any
	// accidental Call() returns fast.
	pr1, _ := io.Pipe()
	_, pw2 := io.Pipe()
	pr1.Close()
	pw2.Close()
	transport := subprocess.NewTransport(pr1, pw2)
	handler := newSubprocessHTTPHandler(transport, "h")

	// 10 MiB + 1 — one byte over the cap.
	oversized := bytes.Repeat([]byte("x"), maxPluginHTTPBodyBytes+1)
	r := httptest.NewRequest(http.MethodPost, "/big", bytes.NewReader(oversized))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("body = %q, expected 'exceeds' message", rec.Body.String())
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
