package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite" // reads the auth audit table
)

// guarded is the API behind the real auth guard, as the server runs it, with
// one real credential per kind.
type guarded struct {
	t       *testing.T
	url     string
	secrets map[string]string
	api     *testAPI
	authDB  string
}

func newGuarded(t *testing.T) *guarded {
	t.Helper()
	a, mux := newTestAPI(t)
	dbPath := filepath.Join(t.TempDir(), "auth.db")
	st, err := apiauth.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := apiauth.NewGuard(st)
	g.Pattern = func(r *http.Request) string { _, p := mux.Handler(r); return p }
	srv := httptest.NewServer(g.Middleware(mux))
	t.Cleanup(srv.Close)
	secrets := map[string]string{}
	for name, p := range map[string]apiauth.CreateParams{
		"operator": {Kind: apiauth.KindOperator, Label: "op", Scopes: apiauth.AllScopes()},
		"client":   {Kind: apiauth.KindClient, Label: "cl", Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate}},
		// manager holds agents:manage and operate; manageronly lacks operate.
		"manager":     {Kind: apiauth.KindClient, Label: "mg", Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate, apiauth.ScopeAgentsManage}},
		"manageronly": {Kind: apiauth.KindClient, Label: "mo", Scopes: []apiauth.Scope{apiauth.ScopeAgentsManage}},
		"agent":       {Kind: apiauth.KindAgent, Label: "ag", Scopes: apiauth.AllScopes(), BoundSessionID: "s1"},
	} {
		_, secret, err := st.Create(t.Context(), p)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		secrets[name] = secret
	}
	return &guarded{t: t, url: srv.URL, secrets: secrets, api: a, authDB: dbPath}
}

type bearerTransport struct{ secret string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.secret != "" {
		r.Header.Set("Authorization", "Bearer "+b.secret)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (g *guarded) mcp(secret string) *mcp.ClientSession {
	g.t.Helper()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).
		Connect(g.t.Context(), &mcp.StreamableClientTransport{Endpoint: g.url + adminMCPPath, HTTPClient: &http.Client{Transport: bearerTransport{secret}}}, nil)
	if err != nil {
		g.t.Fatal(err)
	}
	g.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func (g *guarded) http(secret, method, path string) (int, string) {
	g.t.Helper()
	req, _ := http.NewRequest(method, g.url+path, nil)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (g *guarded) auditRoutes() []string {
	g.t.Helper()
	db, err := sql.Open("sqlite", g.authDB)
	if err != nil {
		g.t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT route || ' ' || outcome FROM auth_audit ORDER BY rowid`)
	if err != nil {
		g.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
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

// Every tool's authorization rests on the outer middleware plus the endpoint's
// own check; prove the route end to end with real credentials.
func TestAdminMCPThroughTheAuthGuard(t *testing.T) {
	g := newGuarded(t)
	post := func(secret string) int {
		req, _ := http.NewRequest(http.MethodPost, g.url+adminMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
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
		if got := post(g.secrets[name]); got != http.StatusForbidden {
			t.Errorf("%s credential = %d, want 403", name, got)
		}
	}
	if got := post(g.secrets["operator"]); got != http.StatusOK {
		t.Errorf("operator = %d, want 200", got)
	}
}

// Without the middleware in front, the endpoint itself still refuses a request
// that carries no admin principal.
func TestAdminMCPEndpointRefusesNonAdminWithoutTheGuard(t *testing.T) {
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

// The per-tool check is the second line behind the endpoint's: build the
// server directly, as the factory does, for a context without admin.
func TestAdminMCPToolsRefuseNonAdminContexts(t *testing.T) {
	a, mux := newTestAPI(t)
	carrier := httptest.NewRequest(http.MethodPost, adminMCPPath, nil)
	for name, p := range map[string]*apiauth.Principal{"no principal": nil, "agent credential": &agentPrincipal, "client without admin": &clientPrincipal} {
		ctx := t.Context()
		if p != nil {
			ctx = apiauth.WithPrincipal(ctx, *p)
		}
		ct, st := mcp.NewInMemoryTransports()
		ss, err := adminMCPServer(a.API, mux, carrier).Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		for tool, args := range map[string]map[string]any{
			"host_settings_list":   nil,
			"agent_list":           nil,
			"tool_grant":           {"id": "x", "tool": "dev_read", "reason": "r"},
			"host_settings_retire": {"id": "x", "reason": "r"},
		} {
			if text, isErr := adminMCPCall(t, cs, tool, args); !isErr || !strings.Contains(text, "credential is required") {
				t.Errorf("%s via %s = %q err=%v, want the admin refusal", tool, name, text, isErr)
			}
		}
		_ = cs.Close()
		_ = ss.Close()
	}
}

// Path-like ids must not reach the router, which would answer a redirect that
// is neither an error nor a success.
func TestAdminMCPRejectsUnsafeIDsAndRedirects(t *testing.T) {
	g := newGuarded(t)
	cs := g.mcp(g.secrets["operator"])
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"tool_grant", map[string]any{"id": "..", "tool": "dev_read", "reason": "r"}},
		{"session_stop", map[string]any{"id": "."}},
		{"agent_get", map[string]any{"id": "a/b"}},
		{"tool_ungrant", map[string]any{"id": "x", "tool": "../y"}},
		{"host_settings_retire", map[string]any{"id": "..", "reason": "r"}},
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
	g := newGuarded(t)
	a := g.api
	op := g.secrets["operator"]
	nativeAuthorityHost(t, a)
	a.store.DB.ExecContext(t.Context(), `DELETE FROM agent_host_settings`)
	cs := g.mcp(op)

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
	hostID := setupHostID(t, a)

	// Reads match the HTTP bodies, paged.
	code, httpList := g.http(op, "GET", "/api/native-agents?limit=50")
	if text, isErr := adminMCPCall(t, cs, "agent_list", nil); isErr || code != 200 || strings.TrimSpace(text) != strings.TrimSpace(httpList) {
		t.Fatalf("agent_list = %q, http %d %q", text, code, httpList)
	}
	slug, _ := setup["slug"].(string)
	code, httpGet := g.http(op, "GET", "/api/native-agents/"+slug)
	if text, isErr := adminMCPCall(t, cs, "agent_get", map[string]any{"id": slug}); isErr != (code >= 400) || strings.TrimSpace(text) != strings.TrimSpace(httpGet) {
		t.Fatalf("agent_get = %q, http %d %q", text, code, httpGet)
	}
	if _, isErr := adminMCPCall(t, cs, "agent_get", nil); !isErr {
		t.Fatal("agent_get without id should fail")
	}

	// List tools page, and authority_list names its host.
	if text, isErr := adminMCPCall(t, cs, "host_settings_list", map[string]any{"limit": 1}); isErr || !strings.HasPrefix(strings.TrimSpace(text), "[") {
		t.Fatalf("host_settings_list limit 1 = %q err=%v", text, isErr)
	}
	var page []any
	if text, _ := adminMCPCall(t, cs, "host_settings_list", map[string]any{"limit": 1, "offset": 5}); json.Unmarshal([]byte(text), &page) != nil || len(page) != 0 {
		t.Fatalf("offset past the end = %q", text)
	}
	if text, isErr := adminMCPCall(t, cs, "host_settings_list", map[string]any{"limit": 0}); !isErr || !strings.Contains(text, "HTTP 400") {
		t.Fatalf("limit 0 = %q err=%v", text, isErr)
	}
	if text, isErr := adminMCPCall(t, cs, "authority_list", map[string]any{"host_settings_id": hostID}); isErr || !strings.Contains(text, authID) {
		t.Fatalf("authority_list = %q err=%v", text, isErr)
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

	// Retire disables the host settings, with a reason, even when the stored
	// settings no longer validate.
	if _, err := a.store.DB.ExecContext(t.Context(), `UPDATE agent_host_settings SET settings_json = json_set(settings_json, '$.model', '') WHERE id = ?`, hostID); err != nil {
		t.Fatal(err)
	}
	if text, isErr := adminMCPCall(t, cs, "host_settings_update", map[string]any{"id": hostID, "slug": "x", "definition_ref": map[string]any{}, "settings": map[string]any{}}); !isErr {
		t.Fatalf("update with invalid settings succeeded: %s", text)
	}
	if text, isErr := adminMCPCall(t, cs, "host_settings_retire", map[string]any{"id": hostID}); !isErr || !strings.Contains(text, "reason") {
		t.Fatalf("retire without reason = %q err=%v", text, isErr)
	}
	if text, isErr := adminMCPCall(t, cs, "host_settings_retire", map[string]any{"id": hostID, "reason": "retired by test"}); isErr {
		t.Fatalf("retire: %s", text)
	}
	var enabled int
	if err := a.store.DB.QueryRowContext(t.Context(), `SELECT enabled FROM agent_host_settings WHERE id = ?`, hostID).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("host enabled = %d err=%v", enabled, err)
	}

	// Sessions.
	if text, isErr := adminMCPCall(t, cs, "session_start", map[string]any{"definition_ref": map[string]any{"id": "def:no-such", "revision": "1", "digest": "x"}}); !isErr {
		t.Fatalf("session_start on an unknown definition succeeded: %s", text)
	}
	if text, isErr := adminMCPCall(t, cs, "session_list", nil); isErr {
		t.Fatalf("session_list: %s", text)
	}
	if _, isErr := adminMCPCall(t, cs, "session_get", map[string]any{"id": "missing"}); !isErr {
		t.Fatal("session_get on a missing session should be an error")
	}

	// Every mutating call is audited by tool and target; reads are not.
	audit := strings.Join(g.auditRoutes(), "\n")
	for _, want := range []string{
		"mcp:agent_setup " + slug + " ok",
		"mcp:tool_grant " + authID + " dev_read ok",
		"mcp:tool_grant " + authID + " dev_read failed:400",
		"mcp:tool_ungrant " + authID + " dev_read ok",
		"mcp:authority_revoke " + authID + " ok",
		"mcp:session_start def:no-such failed:",
		"mcp:host_settings_retire " + hostID + " reason=retired by test ok",
	} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit lacks %q:\n%s", want, audit)
		}
	}
	if strings.Contains(audit, "mcp:agent_list") || strings.Contains(audit, "mcp:session_list") {
		t.Errorf("reads were audited:\n%s", audit)
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

// The list tools send a limit even when the caller gives none, and pass an
// explicit limit/offset through untouched.
func TestAdminMCPListToolsPageByDefault(t *testing.T) {
	a, _ := newTestAPI(t)
	var seen []string
	spy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = w.Write([]byte("[]"))
	})
	carrier := httptest.NewRequest(http.MethodPost, adminMCPPath, nil)
	ctx := apiauth.WithPrincipal(t.Context(), adminClientPrincipal)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := adminMCPServer(a.API, spy, carrier).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	adminMCPCall(t, cs, "agent_list", nil)
	adminMCPCall(t, cs, "host_settings_list", nil)
	adminMCPCall(t, cs, "authority_list", map[string]any{"host_settings_id": "h1"})
	adminMCPCall(t, cs, "agent_list", map[string]any{"limit": 7, "offset": 3})
	want := []string{
		"/api/native-agents?limit=50",
		"/api/agent-host-settings?limit=50",
		"/api/native-authority?host_settings_id=h1&limit=50",
		"/api/native-agents?limit=7&offset=3",
	}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("inner requests = %v, want %v", seen, want)
	}
}

// The list routes honor limit and offset before rendering.
func TestListRoutesPage(t *testing.T) {
	a, mux := newTestAPI(t)
	host := nativeAuthorityHost(t, a)
	second := host
	second.ID, second.Revision, second.Slug, second.Title = "", "", "native-authority-api-2", "Second"
	if _, err := a.store.CreateAgentHostSettings(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if w := naReq(mux, &adminClientPrincipal, "POST", "/api/native-authority", map[string]any{"host_settings_id": host.ID, "reason": "paging test"}); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create authority = %d %s", w.Code, w.Body)
	}
	dir := filepath.Join(a.Services.AgentSetup.DirectoryRoot(t.Context()), "work")
	if w := naReq(mux, &adminClientPrincipal, "POST", "/api/agent-setup", map[string]any{"artifact": setupFixtureDef, "provider": "claude", "model": "sonnet", "runtime": "cli", "tools": []string{"dev_read"}, "reason": "paging test", "directories": []string{dir}}); w.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	count := func(path string) int {
		w := naReq(mux, &adminClientPrincipal, "GET", path, nil)
		var items []json.RawMessage
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &items) != nil {
			t.Fatalf("GET %s = %d %s", path, w.Code, w.Body)
		}
		return len(items)
	}
	auth := "/api/native-authority?host_settings_id=" + host.ID
	for path, want := range map[string]int{
		auth:                                        1,
		auth + "&limit=1":                           1,
		auth + "&limit=5&offset=1":                  0,
		"/api/agent-host-settings":                  3,
		"/api/agent-host-settings?limit=1":          1,
		"/api/agent-host-settings?limit=2&offset=2": 1,
		"/api/native-agents":                        3,
		"/api/native-agents?limit=1":                1,
		"/api/native-agents?limit=5&offset=1":       2,
		"/api/native-agents?limit=5&offset=3":       0,
	} {
		if got := count(path); got != want {
			t.Errorf("GET %s returned %d items, want %d", path, got, want)
		}
	}
	for _, path := range []string{auth + "&limit=0", "/api/native-agents?limit=0", "/api/agent-host-settings?limit=501", "/api/native-agents?limit=1&offset=-1"} {
		if w := naReq(mux, &adminClientPrincipal, "GET", path, nil); w.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, w.Code)
		}
	}
}
