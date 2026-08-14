package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/nanite/internal/store"
)

// newAllowlistedTestServer mirrors newTestServer (handlers_test.go) but
// threads a tool allowlist through, mirroring the workflow-runner launch
// path's CW-20260814-0006 scoping.
func newAllowlistedTestServer(t *testing.T, allowlist []string) *Server {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, "test-session", nil, "", "", allowlist)
}

// connectClient wires srv (this package's Server, wrapping an *mcp.Server)
// to a real mcp.Client over an in-memory transport pair and returns a live
// ClientSession — driving genuine ListTools/CallTool JSON-RPC requests
// through the same dispatch path a real workflow-runner subprocess would
// use, not just inspecting what got registered.
func connectClient(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	mcpSrv := srv.buildMCPServer()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	serverSession, err := mcpSrv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })

	return clientSession
}

// TestToolAllowlist_DiscoveryIsRestricted verifies a workflow-runner-scoped
// server's tools/list response contains ONLY the three allowlisted
// workflow callback tools — none of the dozens of other self-tools or any
// dev_* filesystem tool.
func TestToolAllowlist_DiscoveryIsRestricted(t *testing.T) {
	allowlist := []string{"workflow_execute_llm_step", "workflow_execute_tool_step", "workflow_verify_step"}
	srv := newAllowlistedTestServer(t, allowlist)
	cs := connectClient(t, srv)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}

	for _, name := range allowlist {
		if !got[name] {
			t.Errorf("expected allowlisted tool %q to be listed, was not; got %v", name, toolNames(res.Tools))
		}
	}
	if len(got) != len(allowlist) {
		t.Errorf("expected exactly %d tools listed (the allowlist), got %d: %v", len(allowlist), len(got), toolNames(res.Tools))
	}
	// Spot-check a couple of tools that exist on the unrestricted catalog
	// (see TestToolAllowlist_Unrestricted_SeesFullCatalog) but must be
	// absent here.
	for _, name := range []string{"todo_create", "dev_read", "skill_create"} {
		if got[name] {
			t.Errorf("out-of-scope tool %q leaked into the restricted discovery list", name)
		}
	}
}

// TestToolAllowlist_DispatchIsRestricted is the load-bearing test for
// CW-20260814-0006: it proves an out-of-scope tool call is genuinely
// REJECTED by the MCP transport, not merely absent from the tools/list
// response. A client that already knows a tool name (e.g. a compromised or
// buggy workflow-runner script that hardcodes "todo_create" instead of
// discovering tools first) must not be able to invoke it.
func TestToolAllowlist_DispatchIsRestricted(t *testing.T) {
	allowlist := []string{"workflow_execute_llm_step", "workflow_execute_tool_step", "workflow_verify_step"}
	srv := newAllowlistedTestServer(t, allowlist)
	cs := connectClient(t, srv)

	// An out-of-scope SELF tool.
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "todo_create",
		Arguments: map[string]any{"title": "should never be created"},
	})
	if err == nil {
		t.Fatal("expected CallTool(\"todo_create\") to fail for an allowlisted server, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected an \"unknown tool\" rejection from the MCP SDK's own dispatch layer, got: %v", err)
	}

	// An out-of-scope DEV tool — proves the restriction applies across
	// both transports registerTools wires up, not just the self transport.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "dev_read",
		Arguments: map[string]any{"path": "/etc/hosts"},
	})
	if err == nil {
		t.Fatal("expected CallTool(\"dev_read\") to fail for an allowlisted server, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected an \"unknown tool\" rejection, got: %v", err)
	}

	// An IN-scope tool must still reach the real handler. WorkflowExecutor
	// is unwired on this bare test store, so the call succeeds at the
	// transport level and the handler itself reports the wiring gap via
	// IsError — proving the request was actually dispatched, not rejected
	// by the allowlist.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "workflow_execute_llm_step",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("expected CallTool(\"workflow_execute_llm_step\") to reach the handler (transport-level success), got error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an application-level error result (WorkflowExecutor unwired), got %+v", res)
	}
}

// TestToolAllowlist_Unrestricted_SeesFullCatalog is the control: a server
// built with a nil allowlist (the CLI-launched coding agent path,
// mirroring cmd/nanite/main.go's cmdMCPServe when NANITE_MCP_TOOL_ALLOWLIST
// is unset) must keep seeing and dispatching the full self+dev catalog
// exactly as before CW-20260814-0006. This guards the ticket's explicit
// non-goal: no behavior change for Claude/Codex/Opencode.
func TestToolAllowlist_Unrestricted_SeesFullCatalog(t *testing.T) {
	srv := newAllowlistedTestServer(t, nil)
	cs := connectClient(t, srv)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{"todo_create", "dev_read", "skill_create", "workflow_execute_llm_step"} {
		if !got[name] {
			t.Errorf("expected unrestricted server to list %q, it did not; got %v", name, toolNames(res.Tools))
		}
	}

	// Dispatch of a non-workflow tool must still reach the real handler —
	// this bare test store has no TodoStore wired, so the handler itself
	// reports that gap via an application-level IsError result rather
	// than the MCP SDK ever rejecting the call as unknown. The point here
	// is the absence of an "unknown tool" transport-level error, proving
	// the allowlist gate did not apply.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "todo_create",
		Arguments: map[string]any{"title": "unrestricted dispatch smoke test"},
	})
	if err != nil {
		t.Fatalf("expected CallTool(\"todo_create\") to reach the handler on an unrestricted server, got transport-level error: %v", err)
	}
}

func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}
