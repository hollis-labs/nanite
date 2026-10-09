package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func grantCatalogFixture(t *testing.T, names ...string) (string, func(...string)) {
	t.Helper()
	var mu sync.Mutex
	current := append([]string(nil), names...)
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "grant-fixture", Version: "test"}, nil)
	server.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			mu.Lock()
			defer mu.Unlock()
			result := &sdkmcp.ListToolsResult{}
			for _, name := range current {
				result.Tools = append(result.Tools, &sdkmcp.Tool{Name: name, Description: "Fixture tool.", InputSchema: map[string]any{"type": "object"}})
			}
			return result, nil
		}
	})
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, &sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	t.Cleanup(func() { httpServer.CloseClientConnections(); httpServer.Close() })
	return httpServer.URL, func(next ...string) { mu.Lock(); defer mu.Unlock(); current = append([]string(nil), next...) }
}

func TestMCPDiscoverySync_CreateRefreshUpdateAndGrantWithoutRestart(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager := mcp.NewManager()
	t.Cleanup(func() { manager.Close() })
	client := toolclient.New(manager, st, toolclient.DefaultConfig())
	discover := newMCPDiscoverySync(st, manager, client)
	servers := newContainerMCPServerService(st, manager, discover)
	url, change := grantCatalogFixture(t, "torque_task_get", "tether_gateway_status")
	cfg := &store.MCPServerConfig{Name: "tether-mux", TransportType: store.TransportStreamable, URL: url}
	if operationErr := servers.Create(ctx, cfg); operationErr != nil {
		t.Fatal(operationErr)
	}
	get, err := st.GetKnownToolByName(ctx, "torque_task_get")
	if err != nil || get.Status != "available" {
		t.Fatalf("create did not publish grantable tool: %+v %v", get, err)
	}
	row := &store.AgentProfile{Name: "Reader", Slug: "reader"}
	if operationErr := st.CreateAgent(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	capabilities := NewAgentCapabilitiesService(st)
	if operationErr := capabilities.GrantTool(ctx, row.ID, get.ID, "explicit"); operationErr != nil {
		t.Fatal(operationErr)
	}
	change("torque_task_get", "torque_task_list", "tether_gateway_status")
	container := &Container{MCP: manager, store: st, ToolClient: client, discoverMCP: discover}
	if _, operationErr := container.DiscoverMCPTools(ctx); operationErr != nil {
		t.Fatal(operationErr)
	}
	list, err := st.GetKnownToolByName(ctx, "torque_task_list")
	if err != nil || list.Status != "available" {
		t.Fatalf("refresh did not publish tool: %+v %v", list, err)
	}
	service := NewToolService(client, nil, st)
	selected, err := service.SelectForAgent(ctx, "fixture", row.ID, "fetch task", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range toolNames(selected.Tools) {
		if name != "torque_task_get" && name != "request_tools" {
			t.Fatalf("upstream exposure became model grant: %s", name)
		}
	}
	if operationErr := capabilities.GrantTool(ctx, row.ID, list.ID, "explicit"); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := capabilities.RevokeTool(ctx, row.ID, get.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	// A refresh must retain that revocation, not replay profile declarations.
	if _, operationErr := container.DiscoverMCPTools(ctx); operationErr != nil {
		t.Fatal(operationErr)
	}
	names, err := st.ListAgentToolNames(ctx, row.ID)
	if err != nil || !reflect.DeepEqual(names, []string{"torque_task_list"}) {
		t.Fatalf("refresh changed grants: %v %v", names, err)
	}
	replacementURL, _ := grantCatalogFixture(t, "replacement_tool")
	if _, operationErr := servers.Update(ctx, cfg, MCPServerPatch{URL: &replacementURL}); operationErr != nil {
		t.Fatal(operationErr)
	}
	replacement, err := st.GetKnownToolByName(ctx, "replacement_tool")
	if err != nil || replacement.Status != "available" {
		t.Fatalf("update did not sync: %+v %v", replacement, err)
	}
	old, err := st.GetKnownToolByName(ctx, "torque_task_get")
	if err != nil || old.ID != get.ID || old.Status != "unavailable" {
		t.Fatalf("disappeared tool was not retained unavailable: %+v %v", old, err)
	}
}

func TestMCPDiscoverySync_ConcurrentRefreshPreservesCatalogAndGrants(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager := mcp.NewManager()
	t.Cleanup(func() { manager.Close() })
	url, _ := grantCatalogFixture(t, "fresh_tool")
	if operationErr := manager.AddRemoteServerFromConfig("fixture", store.TransportStreamable, url, "", mcp.TierBuiltin); operationErr != nil {
		t.Fatal(operationErr)
	}
	client := toolclient.New(manager, st, toolclient.DefaultConfig())
	discover := newMCPDiscoverySync(st, manager, client)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := discover(ctx); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	known, err := st.GetKnownToolByName(ctx, "fresh_tool")
	if err != nil || known.Status != "available" {
		t.Fatalf("concurrent publication lost catalog: %+v %v", known, err)
	}
	meta, err := st.GetKnownToolByName(ctx, "request_tools")
	if err != nil || meta.Status != "available" {
		t.Fatalf("refresh retired synthetic discovery tool: %+v %v", meta, err)
	}
}

type scopedGrantTransport struct{}

func (scopedGrantTransport) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{Name: "torque_task_get", Description: "Read one task.", InputSchema: map[string]any{"type": "object"}},
		{Name: "torque_task_list", Description: "List tasks.", InputSchema: map[string]any{"type": "object"}},
		{Name: "tether_gateway_status", Description: "Gateway diagnostic.", InputSchema: map[string]any{"type": "object"}},
	}, nil
}
func (scopedGrantTransport) CallTool(context.Context, string, map[string]any) (*mcp.ToolResult, error) {
	return &mcp.ToolResult{}, nil
}

func TestMCPDiscoverySync_CollisionUsesRegistryNameForHostGrant(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	manager := mcp.NewManager()
	t.Cleanup(manager.Close)
	for _, server := range []string{"another-server", "tether-mux"} {
		if operationErr := manager.AddServer(server, scopedGrantTransport{}, mcp.TierBuiltin); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	client := toolclient.New(manager, st, toolclient.DefaultConfig())
	discover := newMCPDiscoverySync(st, manager, client)
	if _, operationErr := discover(ctx); operationErr != nil {
		t.Fatal(operationErr)
	}
	var grantedName string
	for _, tool := range manager.GetAllToolsUnfiltered() {
		server, original, registered := manager.ToolAttribution(tool.Name)
		if registered && server == "tether-mux" && original == "torque_task_get" {
			grantedName = tool.Name
		}
	}
	if grantedName == "" || grantedName == "torque_task_get" {
		t.Fatalf("fixture must exercise actual disambiguation: %q", grantedName)
	}
	// Load preferences must not hide a registered name from the grant catalog.
	manager.SetToolLoadPreferences(map[string]string{grantedName: "opt-in"})
	if _, operationErr := discover(ctx); operationErr != nil {
		t.Fatal(operationErr)
	}
	known, err := st.GetKnownToolByName(ctx, grantedName)
	if err != nil || known.Status != "available" {
		t.Fatalf("opt-in tool not grantable: %+v %v", known, err)
	}
	row := &store.AgentProfile{Name: "Scoped", Slug: "scoped"}
	if operationErr := st.CreateAgent(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := st.GrantAgentTool(ctx, row.ID, known.ID, "explicit"); operationErr != nil {
		t.Fatal(operationErr)
	}
	filtered := filterToolsByAgentTools(ctx, st, row.ID, client.GetAllToolsUnfiltered())
	if len(filtered) != 1 || filtered[0].Name != grantedName {
		t.Fatalf("host grant admitted list/status/other-server tool: %v", toolNames(filtered))
	}
}

func TestMCPDiscoverySync_ServerImportPublishesGrantableCatalog(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	manager := mcp.NewManager()
	url, _ := grantCatalogFixture(t, "imported_mcp_tool")
	t.Cleanup(manager.Close)
	discover := newMCPDiscoverySync(st, manager, toolclient.New(manager, st, toolclient.DefaultConfig()))
	servers := newContainerMCPServerService(st, manager, discover)
	input, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"imported-server": map[string]any{"type": "http", "url": url}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := servers.Import(ctx, input)
	if err != nil || len(result.Created) != 1 {
		t.Fatalf("server import: %+v %v", result, err)
	}
	known, err := st.GetKnownToolByName(ctx, "imported_mcp_tool")
	if err != nil || known.Status != "available" {
		t.Fatalf("import did not publish grantable catalog: %+v %v", known, err)
	}
}
