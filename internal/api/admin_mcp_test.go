package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func adminMCPSession(t *testing.T, mux http.Handler, p *apiauth.Principal) *mcp.ClientSession {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p != nil {
			r = r.WithContext(apiauth.WithPrincipal(r.Context(), *p))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL + adminMCPPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func adminMCPCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func TestAdminMCPRefusesNonAdmin(t *testing.T) {
	_, mux := newTestAPI(t)
	for name, p := range map[string]*apiauth.Principal{"no principal": nil, "agent credential": &agentPrincipal, "client without admin": &clientPrincipal} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p != nil {
				r = r.WithContext(apiauth.WithPrincipal(r.Context(), *p))
			}
			mux.ServeHTTP(w, r)
		}))
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).
			Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL + adminMCPPath}, nil)
		if err == nil {
			// The in-process test mux has no auth middleware; the wrapped
			// routes still refuse, so the tool call must fail.
			text, isErr := adminMCPCall(t, cs, "agent_list", nil)
			if !isErr {
				t.Errorf("%s: agent_list succeeded: %s", name, text)
			}
			_ = cs.Close()
		}
		ts.Close()
	}
}

func TestAdminMCPManagesAgentsLikeHTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a)
	a.store.DB.ExecContext(t.Context(), `DELETE FROM agent_host_settings`)
	cs := adminMCPSession(t, mux, &adminClientPrincipal)

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, want := range []string{"agent_setup", "agent_list", "agent_get", "host_settings_retire", "tool_grant", "tool_ungrant", "authority_revoke", "session_start", "session_stop", "turn_send", "turn_cancel"} {
		if !have[want] {
			t.Errorf("tool %s not listed", want)
		}
	}

	dir := filepath.Join(a.Services.AgentSetup.DirectoryRoot(t.Context()), "work")
	args := map[string]any{"artifact": setupFixtureDef, "provider": "claude", "model": "sonnet", "runtime": "cli", "tools": []string{"dev_read"}, "reason": "mcp test", "directories": []string{dir}}

	// A refusal carries the HTTP status and message.
	bad := map[string]any{"artifact": setupFixtureDef, "provider": "claude", "runtime": "cli", "tools": []string{"dev_read"}, "reason": ""}
	if text, isErr := adminMCPCall(t, cs, "agent_setup", bad); !isErr || !strings.Contains(text, "HTTP 400") {
		t.Fatalf("missing reason = %q err=%v", text, isErr)
	}

	text, isErr := adminMCPCall(t, cs, "agent_setup", args)
	if isErr {
		t.Fatalf("agent_setup: %s", text)
	}
	var setup map[string]any
	if err := json.Unmarshal([]byte(text), &setup); err != nil || setup["created"] != true {
		t.Fatalf("setup = %s (%v)", text, err)
	}
	authID, _ := setup["authority_id"].(string)

	// Reads match the HTTP bodies.
	httpList := naReq(mux, &adminClientPrincipal, "GET", "/api/native-agents", nil)
	if text, isErr := adminMCPCall(t, cs, "agent_list", nil); isErr || strings.TrimSpace(text) != strings.TrimSpace(httpList.Body.String()) {
		t.Fatalf("agent_list = %q, http %q", text, httpList.Body)
	}
	slug, _ := setup["slug"].(string)
	httpGet := naReq(mux, &adminClientPrincipal, "GET", "/api/native-agents/"+slug, nil)
	if text, isErr := adminMCPCall(t, cs, "agent_get", map[string]any{"id": slug}); isErr != (httpGet.Code >= 400) || strings.TrimSpace(text) != strings.TrimSpace(httpGet.Body.String()) {
		t.Fatalf("agent_get = %q, http %d %q", text, httpGet.Code, httpGet.Body)
	}
	if _, isErr := adminMCPCall(t, cs, "agent_get", nil); !isErr {
		t.Fatal("agent_get without id should fail")
	}

	// Grant, ungrant, revoke.
	if text, isErr := adminMCPCall(t, cs, "tool_ungrant", map[string]any{"id": authID, "tool": "dev_read", "reason": "mcp test"}); isErr {
		t.Fatalf("ungrant: %s", text)
	}
	if text, isErr := adminMCPCall(t, cs, "tool_grant", map[string]any{"id": authID, "tool": "dev_read"}); !isErr || !strings.Contains(text, "HTTP 400") {
		t.Fatalf("grant without reason = %q err=%v", text, isErr)
	}
	if text, isErr := adminMCPCall(t, cs, "tool_grant", map[string]any{"id": authID, "tool": "dev_read", "reason": "mcp test"}); isErr {
		t.Fatalf("grant: %s", text)
	}
	if text, isErr := adminMCPCall(t, cs, "authority_revoke", map[string]any{"id": authID, "reason": "mcp test"}); isErr {
		t.Fatalf("revoke: %s", text)
	}

	// Retire disables the host settings.
	hostID := setupHostID(t, a)
	if text, isErr := adminMCPCall(t, cs, "host_settings_retire", map[string]any{"id": hostID}); isErr {
		t.Fatalf("retire: %s", text)
	}
	var enabled int
	if err := a.store.DB.QueryRowContext(t.Context(), `SELECT enabled FROM agent_host_settings WHERE id = ?`, hostID).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("host enabled = %d err=%v", enabled, err)
	}

	// Sessions.
	if text, isErr := adminMCPCall(t, cs, "session_list", nil); isErr {
		t.Fatalf("session_list: %s", text)
	}
	if _, isErr := adminMCPCall(t, cs, "session_get", map[string]any{"id": "missing"}); !isErr {
		t.Fatal("session_get on a missing session should be an error")
	}
}

func setupHostID(t *testing.T, a *testAPI) string {
	t.Helper()
	var id string
	if err := a.store.DB.QueryRowContext(t.Context(), `SELECT id FROM agent_host_settings LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
