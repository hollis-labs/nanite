package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A bare `nanite mcp` (no NANITE_API_URL, no allowlist) — how tether's MCP
// proxy runs it — must not offer a tool whose service it does not have
// (CW-20261001-0017).
func TestBareServer_ListingOffersNoUnwiredTool(t *testing.T) {
	srv := newAllowlistedTestServer(t, nil)
	cs := connectClient(t, srv)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	listed := toolNames(res.Tools)
	hidden := srv.self.(hiddenToolLister).HiddenTools()
	if len(hidden) == 0 {
		t.Fatal("a bare server should hide its unwired self tools")
	}
	for _, name := range hidden {
		if slices.Contains(listed, name) {
			t.Errorf("unwired tool %q is listed", name)
		}
	}
	for _, name := range []string{"message_inbox", "message_send", "handoff_request", "todo_create", "subagent_spawn"} {
		if slices.Contains(listed, name) {
			t.Errorf("%q is listed on a bare server", name)
		}
	}
	for _, name := range []string{"skill_list", "agent_list", "dev_read"} {
		if !slices.Contains(listed, name) {
			t.Errorf("%q should still be listed; got %v", name, listed)
		}
	}
}

// A client that already knows a hidden tool's name gets the tool's own
// reason, not the SDK's "unknown tool". A name no transport knows still
// gets "unknown tool".
func TestBareServer_HiddenToolCallReturnsItsReason(t *testing.T) {
	srv := newAllowlistedTestServer(t, nil)
	cs := connectClient(t, srv)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "message_inbox",
		Arguments: map[string]any{"session_id": "s", "agent_id": "a"},
	})
	if err != nil {
		t.Fatalf("CallTool(message_inbox): transport error %v, want the handler's result", err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	if !res.IsError || text != "messaging service not configured" {
		t.Fatalf("CallTool(message_inbox) = IsError %v %q, want the messaging handler's error", res.IsError, text)
	}

	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "no_such_tool"})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("CallTool(no_such_tool) err = %v, want unknown tool", err)
	}
}

// With NANITE_API_URL the self tools are forwarded to the live harness,
// which has every service, so the full catalog stays listed.
func TestProxyServer_ListsFullSelfCatalog(t *testing.T) {
	srv := NewForwarding("test-session", nil, "", "http://127.0.0.1:1", ScopeHarness, nil)
	cs := connectClient(t, srv)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	listed := toolNames(res.Tools)
	for _, name := range []string{"message_inbox", "todo_create", "subagent_spawn"} {
		if !slices.Contains(listed, name) {
			t.Errorf("proxy server should list %q", name)
		}
	}
}
