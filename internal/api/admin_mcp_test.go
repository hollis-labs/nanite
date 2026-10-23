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

// guardedAdminMCP puts the real auth guard in front of the API mux, as the
// server does, and returns the base URL plus a bearer secret per credential.
func guardedAdminMCP(t *testing.T) (url string, secrets map[string]string, a *testAPI) {
	t.Helper()
	a, mux := newTestAPI(t)
	st, err := apiauth.Open(t.Context(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := apiauth.NewGuard(st)
	g.Pattern = func(r *http.Request) string { _, p := mux.Handler(r); return p }
	srv := httptest.NewServer(g.Middleware(mux))
	t.Cleanup(srv.Close)
	secrets = map[string]string{}
	for name, p := range map[string]apiauth.CreateParams{
		"operator": {Kind: apiauth.KindOperator, Label: "op", Scopes: apiauth.AllScopes()},
		"client":   {Kind: apiauth.KindClient, Label: "cl", Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate}},
		"agent":    {Kind: apiauth.KindAgent, Label: "ag", Scopes: apiauth.AllScopes(), BoundSessionID: "s1"},
	} {
		_, secret, err := st.Create(t.Context(), p)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		secrets[name] = secret
	}
	return srv.URL, secrets, a
}

type bearerTransport struct{ secret string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.secret != "" {
		r.Header.Set("Authorization", "Bearer "+b.secret)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Every tool's authorization rests on the outer middleware plus the endpoint's
// own check; prove the route end to end with real credentials.
func TestAdminMCPThroughTheAuthGuard(t *testing.T) {
	url, secrets, _ := guardedAdminMCP(t)
	post := func(secret string) int {
		req, _ := http.NewRequest(http.MethodPost, url+adminMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(""); got != http.StatusUnauthorized {
		t.Errorf("no credential = %d, want 401", got)
	}
	for _, name := range []string{"client", "agent"} {
		if got := post(secrets[name]); got != http.StatusForbidden {
			t.Errorf("%s credential = %d, want 403", name, got)
		}
	}
	if got := post(secrets["operator"]); got != http.StatusOK {
		t.Errorf("operator = %d, want 200", got)
	}

	// The operator works end to end through the MCP client, tools included.
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: url + adminMCPPath, HTTPClient: &http.Client{Transport: bearerTransport{secrets["operator"]}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	if text, isErr := adminMCPCall(t, cs, "host_settings_list", nil); isErr {
		t.Fatalf("host_settings_list as operator: %s", text)
	}
}

// Without the middleware in front, the endpoint and each tool still refuse a
// request that carries no admin principal.
func TestAdminMCPRefusesNonAdminWithoutTheGuard(t *testing.T) {
	_, mux := newTestAPI(t)
	for name, p := range map[string]*apiauth.Principal{"no principal": nil, "agent credential": &agentPrincipal, "client without admin": &clientPrincipal} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p != nil {
				r = r.WithContext(apiauth.WithPrincipal(r.Context(), *p))
			}
			mux.ServeHTTP(w, r)
		}))
		req, _ := http.NewRequest(http.MethodPost, ts.URL+adminMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status %d, want 401/403", name, resp.StatusCode)
		}
		ts.Close()
	}
}

// Path-like ids must not reach the router, which would answer a redirect that
// is neither an error nor a success.
func TestAdminMCPRejectsUnsafeIDsAndRedirects(t *testing.T) {
	_, mux := newTestAPI(t)
	cs := adminMCPSession(t, mux, &adminClientPrincipal)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"tool_grant", map[string]any{"id": "..", "tool": "dev_read", "reason": "r"}},
		{"session_stop", map[string]any{"id": "."}},
		{"agent_get", map[string]any{"id": "a/b"}},
		{"tool_ungrant", map[string]any{"id": "x", "tool": "../y"}},
		{"host_settings_retire", map[string]any{"id": ".."}},
	} {
		if text, isErr := adminMCPCall(t, cs, c.tool, c.args); !isErr {
			t.Errorf("%s %v succeeded: %s", c.tool, c.args, text)
		}
	}
	for _, status := range []int{200, 201, 204} {
		if r := mcpToolResult(status, []byte("{}")); r.IsError {
			t.Errorf("status %d flagged as error", status)
		}
	}
	for _, status := range []int{301, 307, 400, 404, 500} {
		if r := mcpToolResult(status, nil); !r.IsError {
			t.Errorf("status %d not flagged as error", status)
		}
	}
	if r := mcpToolResult(200, make([]byte, adminMCPMaxResponse+1)); !r.IsError {
		t.Error("oversize response not refused")
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
	// Retiring must not depend on the stored settings still validating.
	if _, err := a.store.DB.ExecContext(t.Context(), `UPDATE agent_host_settings SET settings_json = json_set(settings_json, '$.model', '') WHERE id = ?`, hostID); err != nil {
		t.Fatal(err)
	}
	if text, isErr := adminMCPCall(t, cs, "host_settings_update", map[string]any{"id": hostID, "slug": "x", "definition_ref": map[string]any{}, "settings": map[string]any{}}); !isErr {
		t.Fatalf("update with invalid settings succeeded: %s", text)
	}
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
