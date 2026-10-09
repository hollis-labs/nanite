package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type wakeRuntimeCapture struct {
	mu              sync.Mutex
	session, prompt string
	calls           int
}

func (*wakeRuntimeCapture) StopSession(context.Context, string) error    { return nil }
func (*wakeRuntimeCapture) RebootSession(context.Context, string) error  { return nil }
func (*wakeRuntimeCapture) RecoverSession(context.Context, string) error { return nil }
func (*wakeRuntimeCapture) CancelSession(context.Context, string) error  { return nil }
func (r *wakeRuntimeCapture) SendMessage(ctx context.Context, session, prompt string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session = session
	r.prompt = prompt
	r.calls++
	return nil
}

type blockedWakeLookup struct {
	service.DurableAgentService
	entered chan struct{}
}

func (s *blockedWakeLookup) GetBySlug(ctx context.Context, _ string) (*store.DurableAgentInstance, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestPluginHostDurableWakeApprovedScopeAndRevocation(t *testing.T) {
	ctx := context.Background()
	st := newSeededStore(t)
	runtime := &wakeRuntimeCapture{}
	durable := service.NewDurableAgentServiceWithRuntime(st, runtime)
	profile := &store.AgentProfile{Name: "Curator", Slug: "loom-curator", SystemPrompt: "Classify fragments.", Source: "user", Durable: true}
	if err := st.CreateAgent(ctx, profile); err != nil {
		t.Fatal(err)
	}
	instance := &store.DurableAgentInstance{Name: "Curator", Slug: "loom-curator", ProfileID: profile.ID, LifecycleClass: store.DurableAgentClassProcess, RuntimeKind: "api", LaunchSourceType: store.DurableAgentLaunchProcessTick, LaunchSourceID: "loom-curator", Status: store.DurableAgentStatusSleeping}
	if err := durable.Create(ctx, instance); err != nil {
		t.Fatal(err)
	}
	a := New(&service.Container{DurableAgents: durable, DurableWake: service.NewDurableAgentWakeService(st, durable)})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugin-host/durable-wake", a.handlePluginHostDurableWake)
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", a.handlePluginHostQuery)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(nil, naniteplugin.NewLogger("wake-test"))
	a.SetPluginHost(host)
	if err := host.SetHostQueryURL(server.URL); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := filepath.Join(root, "wake-client")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := writeAPIPluginBundle(t, directory, "wake-client", "Wake client", pluginapi.Block{})
	raw, err := os.ReadFile(path) // #nosec G304 -- private test bundle path returned by fixture.
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := manifest.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityDurableWake, Metadata: json.RawMessage(`{"agent_slugs":["loom-curator","missing-curator"]}`)}, {Name: pluginapi.CapabilityReadOnlyQuery, Metadata: json.RawMessage(`{"resources":["sessions"],"all_sessions":true}`)}}
	var encoded strings.Builder
	if err = manifest.Encode(&encoded, declaration); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(encoded.String()), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = naniteplugin.SaveInstallApproval(root, review, review.Digest()); err != nil {
		t.Fatal(err)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("load: %v %v", loaded, failures)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("wake-client") })
	child, ok := host.GetPlugin("wake-client")
	if !ok {
		t.Fatal("plugin missing")
	}
	identity, err := child.(*subprocess.SubprocessPlugin).CallTool(ctx, &sdkprocess.MCPCallRequest{ToolName: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := pluginapi.DurableWakeGrantFromIdentity(identity.Content)
	if err != nil {
		t.Fatal(err)
	}
	queryGrant, queryErr := pluginapi.QueryGrantFromIdentity(identity.Content)
	if queryErr != nil || queryGrant.Token == grant.Token {
		t.Fatal("independent query grant missing", queryErr)
	}
	client, err := pluginapi.NewDurableWakeClient(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Wake(ctx, pluginapi.DurableWakeRequest{AgentSlug: "loom-curator", Reason: "callback:wiki_page", Prompt: "Fetch fragment-one and classify it.", Facts: map[string]string{"fragment_id": "fragment-one"}})
	if err != nil || response.Status != "queued" || response.InstanceID != instance.ID || response.SessionID == "" {
		t.Fatal(response, err)
	}
	runtime.mu.Lock()
	if runtime.calls != 1 || runtime.session != response.SessionID || runtime.prompt != "Fetch fragment-one and classify it." {
		t.Error("wake did not deliver real prompt")
	}
	runtime.mu.Unlock()
	request := func(method, path, body, token string, status int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), grant.Token) {
			t.Fatal("credential escaped response")
		}
	}
	valid := `{"agent_slug":"loom-curator","prompt":"wake"}`
	request("POST", "/api/plugin-host/durable-wake", valid, "", 401)
	request("POST", "/api/plugin-host/durable-wake", valid, strings.Repeat("a", 43), 401)
	request("POST", "/api/plugin-host/durable-wake", `{"agent_slug":"other","prompt":"wake"}`, grant.Token, 403)
	request("POST", "/api/plugin-host/durable-wake", `{"agent_slug":"missing-curator","prompt":"wake"}`, grant.Token, 404)
	request("POST", "/api/plugin-host/durable-wake", `{"agent_slug":"loom-curator","prompt":"wake","profile":"other"}`, grant.Token, 400)
	request("POST", "/api/plugin-host/durable-wake", `{"agent_slug":"other","agent_slug":"loom-curator","prompt":"wake"}`, grant.Token, 400)
	request("POST", "/api/plugin-host/durable-wake?other=1", valid, grant.Token, 400)
	request("POST", "/api/plugin-host/durable-wake", strings.Repeat("x", pluginapi.MaxDurableWakeBytes+1), grant.Token, 413)
	request("GET", "/api/plugin-host/query/sessions", "", grant.Token, 401)
	request("POST", "/api/plugin-host/durable-wake", valid, queryGrant.Token, 401)
	request("GET", "/api/plugin-host/query/sessions", "", queryGrant.Token, 503)
	if _, err = durable.RequestPause(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/plugin-host/durable-wake", valid, grant.Token, 409)
	blocked := &blockedWakeLookup{DurableAgentService: durable, entered: make(chan struct{})}
	a.Services.DurableAgents = blocked
	finished := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/plugin-host/durable-wake", strings.NewReader(valid))
		r.Header.Set("Authorization", "Bearer "+grant.Token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		finished <- w.Code
	}()
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("wake lookup never started")
	}
	if err = host.UnloadPlugin("wake-client"); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-finished:
		if status != 504 {
			t.Fatal("unload did not cancel wake", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked wake remained blocked")
	}
	request("POST", "/api/plugin-host/durable-wake", valid, grant.Token, 401)
	if _, ok := host.AuthorizeHostQuery(queryGrant.Token); ok {
		t.Fatal("unload retained read credential")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.calls != 1 {
		t.Fatal("refused wake delivered prompt")
	}
}
