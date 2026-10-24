package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

var managerClientPrincipal = apiauth.Principal{CredentialID: "cred-manager-client", Kind: apiauth.KindClient, Label: "tachyon", Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate, apiauth.ScopeAgentsManage}} // #nosec G101 -- test fixture id, not a credential

// agents:manage sets an agent up but never grants tools: naming any is refused
// before anything is written; an empty list creates the agent with no grants.
func TestAgentSetupWithAgentsManageGrantsNothing(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a)
	a.store.DB.ExecContext(t.Context(), `DELETE FROM agent_host_settings`)
	dir := filepath.Join(a.Services.AgentSetup.DirectoryRoot(t.Context()), "work")
	body := map[string]any{"artifact": setupFixtureDef, "provider": "claude", "runtime": "cli", "tools": []string{"dev_read"}, "reason": "test", "directories": []string{dir}}

	before := authorityRows(t, a, "agent_definitions") + authorityRows(t, a, "agent_host_settings") + authorityRows(t, a, "native_authorities")
	if w := naReq(mux, &managerClientPrincipal, "POST", "/api/agent-setup", body); w.Code != http.StatusForbidden {
		t.Fatalf("setup naming tools = %d %s, want 403", w.Code, w.Body)
	}
	if after := authorityRows(t, a, "agent_definitions") + authorityRows(t, a, "agent_host_settings") + authorityRows(t, a, "native_authorities"); after != before {
		t.Fatalf("refused setup wrote rows: %d -> %d", before, after)
	}

	body["tools"] = []string{}
	w := naReq(mux, &managerClientPrincipal, "POST", "/api/agent-setup", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup without tools = %d %s", w.Code, w.Body)
	}
	var res struct {
		AuthorityID string   `json:"authority_id"`
		Tools       []string `json:"tools"`
		Added       []string `json:"tools_added"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Tools) != 0 || len(res.Added) != 0 || res.AuthorityID == "" {
		t.Fatalf("setup result = %s, want an authority and no tools", w.Body)
	}
	if n := authorityRows(t, a, "native_authority_tools"); n != 0 {
		t.Fatalf("%d tool grants written for an agents:manage caller", n)
	}
	var by string
	_ = a.store.DB.QueryRowContext(t.Context(), `SELECT granted_by FROM native_authority_audit WHERE authority_id=? AND action='authority_created'`, res.AuthorityID).Scan(&by)
	if by != managerClientPrincipal.CredentialID {
		t.Fatalf("granted_by = %q, want the credential id", by)
	}

	// The same caller reads the agent and its authority, but cannot change authority.
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/native-agents", 200},
		{"GET", "/api/native-agents/def-http-setup", 200},
		{"GET", "/api/native-authority/tools", 200},
		{"GET", "/api/native-authority/" + res.AuthorityID, 200},
		{"PUT", "/api/native-authority/" + res.AuthorityID + "/tools/dev_read", 403},
		{"DELETE", "/api/native-authority/" + res.AuthorityID + "/tools/dev_read", 403},
		{"POST", "/api/native-authority/" + res.AuthorityID + "/revoke", 403},
		{"POST", "/api/native-authority", 403},
	} {
		var b any
		if c.method != "GET" {
			b = map[string]any{"reason": "r"}
		}
		if w := naReq(mux, &managerClientPrincipal, c.method, c.path, b); w.Code != c.want {
			t.Errorf("%s %s = %d %s, want %d", c.method, c.path, w.Code, w.Body, c.want)
		}
	}
}

// End to end with real credentials: the admin MCP endpoint serves an
// agents:manage client, whose grant and revoke tools are refused and whose
// session tools need operate as well.
func TestAdminMCPWithAgentsManageCredential(t *testing.T) {
	g := newGuarded(t)
	cs := g.mcp(g.secrets["manager"])

	if text, isErr := adminMCPCall(t, cs, "agent_list", nil); isErr {
		t.Fatalf("agent_list = %q", text)
	}
	if text, isErr := adminMCPCall(t, cs, "host_settings_list", nil); isErr {
		t.Fatalf("host_settings_list = %q", text)
	}
	if text, isErr := adminMCPCall(t, cs, "session_list", nil); isErr {
		t.Fatalf("session_list with operate = %q", text)
	}
	for tool, args := range map[string]map[string]any{
		"tool_grant":       {"id": "x", "tool": "dev_read", "reason": "r"},
		"skill_grant":      {"id": "x", "skill": "s", "digest": "sha256:" + strings.Repeat("a", 64), "reason": "r"},
		"skill_ungrant":    {"id": "x", "skill": "s", "reason": "r"},
		"tool_ungrant":     {"id": "x", "tool": "dev_read", "reason": "r"},
		"authority_revoke": {"id": "x", "reason": "r"},
	} {
		if text, isErr := adminMCPCall(t, cs, tool, args); !isErr || !strings.Contains(text, "needs the admin scope") {
			t.Errorf("%s = %q err=%v, want the admin-scope refusal", tool, text, isErr)
		}
	}

	noOp := g.mcp(g.secrets["manageronly"])
	if text, isErr := adminMCPCall(t, noOp, "agent_list", nil); isErr {
		t.Fatalf("agent_list without operate = %q", text)
	}
	for tool, args := range map[string]map[string]any{
		"session_list":  nil,
		"session_start": {"definition_ref": map[string]any{"id": "x"}},
		"turn_send":     {"id": "s", "content": "hi"},
	} {
		if text, isErr := adminMCPCall(t, noOp, tool, args); !isErr || !strings.Contains(text, "needs the operate scope") {
			t.Errorf("%s without operate = %q err=%v, want the operate-scope refusal", tool, text, isErr)
		}
	}
}

// Through the real guard: an agents:manage client reaches agent management and
// is refused credentials, providers and every grant route; a client without it
// is refused the management routes.
func TestAgentsManageHTTPThroughTheAuthGuard(t *testing.T) {
	g := newGuarded(t)
	for _, c := range []struct {
		secret, method, path string
		want                 int
	}{
		{"manager", "GET", "/api/native-agents", 200},
		{"manager", "GET", "/api/agent-host-settings", 200},
		{"manager", "GET", "/api/native-authority/tools", 200},
		{"manager", "GET", "/api/auth/credentials", 403},
		{"manager", "POST", "/api/auth/credentials", 403},
		{"manager", "PUT", "/api/providers/x/api-key", 403},
		{"manager", "PUT", "/api/native-authority/x/tools/dev_read", 403},
		{"manager", "DELETE", "/api/native-authority/x/tools/dev_read", 403},
		{"manager", "POST", "/api/native-authority/x/revoke", 403},
		{"client", "GET", "/api/native-agents", 403},
		{"client", "POST", "/api/agent-setup", 403},
		{"agent", "GET", "/api/native-agents", 403},
	} {
		if got, _ := g.http(g.secrets[c.secret], c.method, c.path); got != c.want {
			t.Errorf("%s %s %s = %d, want %d", c.secret, c.method, c.path, got, c.want)
		}
	}
	// The MCP endpoint itself.
	if got, _ := g.http(g.secrets["manager"], "GET", adminMCPPath); got == http.StatusForbidden || got == http.StatusUnauthorized {
		t.Errorf("agents:manage client at the MCP endpoint = %d, want it admitted", got)
	}
}
