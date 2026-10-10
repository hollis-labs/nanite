package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpbridge"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestForwardingProxyRefusesUnverifiedPluginPathsBeforeHostAccess(t *testing.T) {
	t.Setenv("NANITE_AUTH_TOKEN", "test-only-operator-token")
	var hostCalls atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCalls.Add(1)
		// A request here would consult live manager inventory/schema or dispatch.
		_ = json.NewEncoder(w).Encode(condmcp.TextResult("unexpected host effect"))
	}))
	defer host.Close()
	proxy := newSelfToolProxy(host.URL, "claimed-session", ScopeHarness)
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"python_run", map[string]any{"code": `result=tool_call("owned_plugin_tool",{})`, "session_id": "other-claimed-session"}},
		{"workflow_execute_tool_step", map[string]any{"tool": "owned_plugin_tool"}},
		{"owned_plugin_tool", map[string]any{}},
		{"context_pin", map[string]any{}},
		{"context_unpin", map[string]any{}},
		{"reminder_set", map[string]any{}},
		{"tool_describe", map[string]any{"name": "owned_plugin_tool"}},
		{"tool_validate", map[string]any{"tool_name": "owned_plugin_tool", "args": map[string]any{}}},
	} {
		if _, err := proxy.CallTool(context.Background(), test.name, test.args); !errors.Is(err, mcpbridge.ErrAuthorityUnavailable) {
			t.Fatalf("%s did not return typed missing authority: %v", test.name, err)
		}
	}
	if hostCalls.Load() != 0 {
		t.Fatal("missing authority consulted manager/dispatch host")
	}
	tools, err := proxy.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name == "python_run" || tool.Name == "workflow_execute_tool_step" || tool.Name == "owned_plugin_tool" {
			t.Fatal("unsupported capability advertised", tool.Name)
		}
	}
}

func TestForwardingProxyMetadataIsCoreOnlyAndCoreCallsStillForward(t *testing.T) {
	var hostCalls atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCalls.Add(1)
		var request struct {
			Name    string `json:"name"`
			Session string `json:"session_id"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Name != "todo_create" || request.Session != "retained-core-session" {
			t.Error("metadata reached host or retained call changed")
		}
		_ = json.NewEncoder(w).Encode(condmcp.TextResult("retained core result"))
	}))
	defer host.Close()
	proxy := newSelfToolProxy(host.URL, "retained-core-session", ScopeHarness)
	result, err := proxy.CallTool(context.Background(), "tool_list", map[string]any{"limit": 1000})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	var inventory struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal([]byte(result.Content[0].Text), &inventory) != nil || len(inventory.Tools) == 0 {
		t.Fatal("no core inventory")
	}
	for _, tool := range inventory.Tools {
		if !proxy.advertises(tool.Name) || tool.Name == "python_run" || tool.Name == "workflow_execute_tool_step" {
			t.Fatal("metadata escaped retained core surface", tool.Name)
		}
	}
	for _, request := range []struct {
		name string
		args map[string]any
	}{
		{"tool_describe", map[string]any{"name": "todo_create"}},
		{"tool_validate", map[string]any{"tool_name": "todo_create", "args": map[string]any{"title": "fixture"}}},
		{"tool_list", map[string]any{"filter": "owned_plugin_tool"}},
	} {
		if _, callErr := proxy.CallTool(context.Background(), request.name, request.args); callErr != nil {
			t.Fatal(request.name, callErr)
		}
	}
	if hostCalls.Load() != 0 {
		t.Fatal("core metadata used live/global host registry")
	}
	result, err = proxy.CallTool(context.Background(), "todo_create", map[string]any{"title": "fixture"})
	if err != nil || result.Content[0].Text != "retained core result" || hostCalls.Load() != 1 {
		t.Fatal("retained core forwarding disabled", result, err)
	}
}

func TestForwardingProxyExplicitAllowlistCannotReenableRecursiveAuthority(t *testing.T) {
	var hostCalls atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hostCalls.Add(1) }))
	defer host.Close()
	server := NewForwarding("claimed-session", nil, "", host.URL, ScopeHarness, []string{"python_run", "workflow_execute_tool_step", "todo_create"})
	client := connectClient(t, server)
	catalog, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if tool.Name != "todo_create" {
			t.Fatal("allowlist advertised missing authority", tool.Name)
		}
	}
	for _, name := range []string{"python_run", "workflow_execute_tool_step"} {
		result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || !result.IsError || !strings.Contains(callText(result), "authority_unavailable") {
			t.Fatal("explicit allowlist bypassed authority fence", name, result, err)
		}
	}
	if hostCalls.Load() != 0 {
		t.Fatal("SDK dispatch reached host without verified authority")
	}
}
