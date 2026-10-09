package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/inspector"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginHostQueryApprovedSubprocessAndScope(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "query.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	first, other := &store.Session{Title: "Allowed", Metadata: `{"secret":"private metadata"}`}, &store.Session{Title: "Other"}
	for _, s := range []*store.Session{first, other} {
		if checkErr := st.CreateSession(ctx, s); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	metric := &store.ExecutionMetrics{SessionID: first.ID, InputTokens: 17, Error: "private failure", DebugSnapshots: "private args", EffectiveLimitsJSON: "private settings"}
	for range 2 {
		if checkErr := st.RecordExecutionMetrics(ctx, metric); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	captures := inspector.NewService()
	captures.RecordSlots(first.ID, "captured-turn", []inspector.SlotSnapshot{{Name: "system", Content: "private instructions", Tokens: 23, Sensitive: true}})
	sessions := service.NewSessionService(service.SessionServiceDeps{Sessions: st})
	svc := service.NewPluginQueryService(st, sessions, service.NewUsageService(st, st), captures)
	a := New(&service.Container{PluginQueries: svc})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", a.handlePluginHostQuery)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(nil, naniteplugin.NewLogger("query-test"))
	a.SetPluginHost(host)
	if checkErr := host.SetHostQueryURL(server.URL); checkErr != nil {
		t.Fatal(checkErr)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "query-reader")
	if checkErr := os.Mkdir(dir, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	path := writeAPIPluginBundle(t, dir, "query-reader", "Query reader", pluginapi.Block{})
	raw, err := os.ReadFile(path) // #nosec G304 -- path returned by the private test bundle fixture.
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := manifest.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	scope := pluginapi.QueryScope{Resources: []pluginapi.QueryResource{pluginapi.QuerySessions, pluginapi.QueryUsage, pluginapi.QueryExecutionMetrics, pluginapi.QueryContextSlots}, SessionIDs: []string{first.ID}}
	metadata, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityReadOnlyQuery, Reason: "Read approved session accounting", Metadata: metadata}}
	var encoded strings.Builder
	if checkErr := manifest.Encode(&encoded, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(path, []byte(encoded.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := naniteplugin.SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("load: %v %v", loaded, failures)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("query-reader") })
	child, ok := host.GetPlugin("query-reader")
	if !ok {
		t.Fatal("plugin missing")
	}
	result, err := child.(*subprocess.SubprocessPlugin).CallTool(ctx, &sdkprocess.MCPCallRequest{ToolName: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := pluginapi.QueryGrantFromIdentity(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	client, err := pluginapi.NewQueryClient(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Query(ctx, pluginapi.QueryRequest{Resource: pluginapi.QuerySessions})
	if err != nil {
		t.Fatal(err)
	}
	var list pluginapi.QuerySessionsData
	if checkErr := json.Unmarshal(response.Data, &list); checkErr != nil {
		t.Fatal(checkErr)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != first.ID || strings.Contains(string(response.Data), "private") {
		t.Fatalf("scope/projection: %s", response.Data)
	}
	response, err = client.Query(ctx, pluginapi.QueryRequest{Resource: pluginapi.QueryExecutionMetrics, SessionID: first.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	var metrics pluginapi.QueryMetricsData
	if checkErr := json.Unmarshal(response.Data, &metrics); checkErr != nil {
		t.Fatal(checkErr)
	}
	if !metrics.More || len(metrics.Metrics) != 1 || !metrics.Metrics[0].Failed || metrics.Metrics[0].InputTokens != 17 || strings.Contains(string(response.Data), "private") {
		t.Fatalf("metrics: %s", response.Data)
	}
	response, err = client.Query(ctx, pluginapi.QueryRequest{Resource: pluginapi.QueryContextSlots, SessionID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	var slots pluginapi.QuerySlotsData
	if checkErr := json.Unmarshal(response.Data, &slots); checkErr != nil {
		t.Fatal(checkErr)
	}
	if !slots.Available || len(slots.Slots) != 1 || slots.Slots[0].Content != "" || slots.Slots[0].Tokens != 23 {
		t.Fatalf("slots: %s", response.Data)
	}
	if _, checkErr := client.Query(ctx, pluginapi.QueryRequest{Resource: pluginapi.QueryUsage, SessionID: first.ID}); checkErr != nil {
		t.Fatal(checkErr)
	}
	request := func(path, token string, expected int) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != expected || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), grant.Token) {
			t.Fatal("token escaped error response")
		}
	}
	request("/api/plugin-host/query/sessions", "", 401)
	request("/api/plugin-host/query/sessions", strings.Repeat("a", 43), 401)
	request("/api/plugin-host/query/usage?session_id="+other.ID, grant.Token, 403)
	request("/api/plugin-host/query/sql?sql=DELETE", grant.Token, 400)
	request("/api/plugin-host/query/sql", grant.Token, 403)
	for _, tail := range []string{"?limit=", "?limit=101", "?limit=1&limit=2", "?session_id=%ZZ", "?include_content=true", "?tool=write"} {
		request("/api/plugin-host/query/sessions"+tail, grant.Token, 400)
	}

	a.Services.PluginQueries = service.NewPluginQueryService(st, &oversizedQuerySession{}, nil, nil)
	request("/api/plugin-host/query/sessions?session_id="+first.ID, grant.Token, http.StatusRequestEntityTooLarge)

	// Revocation must stop an API read already waiting in the service layer.
	entered := make(chan struct{})
	blocked := &blockingQuerySessions{entered: entered}
	a.Services.PluginQueries = service.NewPluginQueryService(st, blocked, nil, nil)
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("GET", "/api/plugin-host/query/usage?session_id="+first.ID, nil)
		req.Header.Set("Authorization", "Bearer "+grant.Token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		done <- rec.Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("read did not enter service")
	}
	permit, ok := host.AuthorizeHostQuery(grant.Token)
	if !ok {
		t.Fatal("connection missing")
	}
	if checkErr := host.UnloadPlugin("query-reader"); checkErr != nil {
		t.Fatal(checkErr)
	}
	select {
	case status := <-done:
		if status != http.StatusGatewayTimeout {
			t.Fatalf("revoked active read status: %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked read did not finish")
	}
	if permit.Context.Err() == nil {
		t.Fatal("unload did not cancel lease")
	}
	request("/api/plugin-host/query/sessions", grant.Token, 401)
	full, err := st.GetSessionExecutionMetrics(ctx, first.ID)
	if err != nil || len(full) != 2 || full[0].Error != "private failure" {
		t.Fatal("reads mutated metrics")
	}
	original, err := st.GetSession(ctx, first.ID)
	if err != nil || original.Metadata != first.Metadata {
		t.Fatal("reads mutated session")
	}
}

type blockingQuerySessions struct {
	service.SessionService
	entered chan struct{}
}

func (s *blockingQuerySessions) Get(ctx context.Context, _ string) (*store.Session, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

type oversizedQuerySession struct{ service.SessionService }

func (*oversizedQuerySession) Get(_ context.Context, id string) (*store.Session, error) {
	return &store.Session{ID: id, Title: strings.Repeat("x", pluginapi.MaxQueryResponseBytes)}, nil
}
