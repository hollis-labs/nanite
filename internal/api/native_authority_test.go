package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/store"
)

var (
	operatorPrincipal = apiauth.Principal{CredentialID: "cred-operator", Kind: apiauth.KindOperator, Label: "operator"}
	clientPrincipal   = apiauth.Principal{CredentialID: "cred-client", Kind: apiauth.KindClient, Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate, apiauth.ScopeTools, apiauth.ScopeMaintain}}
	agentPrincipal    = apiauth.Principal{CredentialID: "cred-agent", Kind: apiauth.KindAgent, Scopes: []apiauth.Scope{apiauth.ScopeTools}, BoundSessionID: "s1"}
)

func naReq(mux http.Handler, p *apiauth.Principal, method, path string, body any) *httptest.ResponseRecorder {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		r = r.WithContext(apiauth.WithPrincipal(r.Context(), *p))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func nativeAuthorityHost(t *testing.T, a *testAPI) store.AgentHostSettings {
	t.Helper()
	def := []byte("---\nschema_version: \"2\"\ndefinition_id: def:native-authority-api\nrevision: \"1\"\nname: test-definition\ndescription: Storage fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nNative authority API fixture.\n")
	pin, err := a.store.InstallAgentDefinition(t.Context(), def, nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: "native-authority-api", Title: "Native authority API", DefinitionRef: pin,
		Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "fixture-model"}, Enabled: true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.UpsertKnownTool(t.Context(), "dev_read", "builtin", "available", "read"); err != nil {
		t.Fatal(err)
	}
	return host
}

func authorityRows(t *testing.T, a *testAPI, table string) int {
	t.Helper()
	var n int
	if err := a.store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- negative cases first ---------------------------------------------------

func TestNativeAuthorityRoutesRefuseEveryoneButAnAdminCredential(t *testing.T) {
	a, mux := newTestAPI(t)
	host := nativeAuthorityHost(t, a)
	reason := map[string]any{"reason": "because"}
	calls := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/native-authority?host_settings_id=" + host.ID, nil},
		{"POST", "/api/native-authority", map[string]any{"host_settings_id": host.ID, "reason": "r"}},
		{"GET", "/api/native-authority/x", nil},
		{"POST", "/api/native-authority/x/revoke", reason},
		{"PUT", "/api/native-authority/x/tools/dev_read", reason},
		{"DELETE", "/api/native-authority/x/tools/dev_read", reason},
	}
	for name, p := range map[string]*apiauth.Principal{"no principal": nil, "agent credential": &agentPrincipal, "client with every non-admin scope": &clientPrincipal} {
		for _, c := range calls {
			w := naReq(mux, p, c.method, c.path, c.body)
			want := http.StatusForbidden
			if p == nil {
				want = http.StatusUnauthorized
			}
			if w.Code != want {
				t.Errorf("%s %s %s = %d, want %d", name, c.method, c.path, w.Code, want)
			}
		}
	}
	if authorityRows(t, a, "native_authorities") != 0 || authorityRows(t, a, "native_authority_audit") != 0 {
		t.Fatal("a refused request wrote authority")
	}
}

func TestNativeAuthorityWritesNeedAReasonAndIgnoreAClaimedGrantor(t *testing.T) {
	a, mux := newTestAPI(t)
	host := nativeAuthorityHost(t, a)
	for name, body := range map[string]any{
		"no reason":         map[string]any{"host_settings_id": host.ID},
		"blank reason":      map[string]any{"host_settings_id": host.ID, "reason": "   "},
		"no host":           map[string]any{"reason": "r"},
		"claimed grantor":   map[string]any{"host_settings_id": host.ID, "reason": "r", "granted_by": "forged-credential"},
		"unknown field too": map[string]any{"host_settings_id": host.ID, "reason": "r", "authority": "environment"},
	} {
		if w := naReq(mux, &operatorPrincipal, "POST", "/api/native-authority", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, w.Code, w.Body.String())
		}
	}
	if authorityRows(t, a, "native_authorities") != 0 {
		t.Fatal("a rejected create wrote a row")
	}
	w := naReq(mux, &operatorPrincipal, "POST", "/api/native-authority", map[string]any{"host_settings_id": host.ID, "reason": "reviewed"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	for name, body := range map[string]any{"no reason": nil, "blank": map[string]any{"reason": " "}} {
		if w = naReq(mux, &operatorPrincipal, "PUT", "/api/native-authority/"+created.ID+"/tools/dev_read", body); w.Code != http.StatusBadRequest {
			t.Errorf("grant %s = %d, want 400", name, w.Code)
		}
	}
	var by string
	if err := a.store.DB.QueryRowContext(t.Context(), `SELECT json_extract(receipt,'$.granted_by') FROM native_authorities WHERE id=?`, created.ID).Scan(&by); err != nil || by != operatorPrincipal.CredentialID {
		t.Fatalf("receipt granted_by = %q (%v), want the authenticated credential", by, err)
	}
	if n := authorityRows(t, a, "native_authority_tools"); n != 0 {
		t.Fatalf("a grant without a reason wrote %d tools", n)
	}
}

// --- positive path ------------------------------------------------------------

func TestNativeAuthorityLifecycleOverTheAPI(t *testing.T) {
	a, mux := newTestAPI(t)
	host := nativeAuthorityHost(t, a)
	create := func() *httptest.ResponseRecorder {
		return naReq(mux, &operatorPrincipal, "POST", "/api/native-authority", map[string]any{"host_settings_id": host.ID, "reason": "reviewed"})
	}
	w := create()
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w = create(); w.Code != http.StatusConflict {
		t.Fatalf("second live authority = %d, want 409", w.Code)
	}
	if w = naReq(mux, &operatorPrincipal, "POST", "/api/native-authority", map[string]any{"host_settings_id": "nope", "reason": "r"}); w.Code != http.StatusNotFound {
		t.Fatalf("unknown host = %d, want 404", w.Code)
	}
	base := "/api/native-authority/" + created.ID
	if w = naReq(mux, &operatorPrincipal, "PUT", base+"/tools/no_such_tool", map[string]any{"reason": "r"}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown tool = %d, want 422", w.Code)
	}
	if w = naReq(mux, &operatorPrincipal, "PUT", base+"/tools/dev_read", map[string]any{"reason": "workspace helper"}); w.Code != http.StatusNoContent {
		t.Fatalf("grant = %d %s", w.Code, w.Body.String())
	}
	w = naReq(mux, &operatorPrincipal, "GET", base, nil)
	var got struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || len(got.Tools) != 1 || got.Tools[0].Name != "dev_read" {
		t.Fatalf("get = %d %s", w.Code, w.Body.String())
	}
	if w = naReq(mux, &operatorPrincipal, "GET", "/api/native-authority?host_settings_id="+host.ID, nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(created.ID)) {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	if w = naReq(mux, &operatorPrincipal, "GET", "/api/native-authority", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("list without host = %d, want 400", w.Code)
	}
	if w = naReq(mux, &operatorPrincipal, "DELETE", base+"/tools/dev_read", map[string]any{"reason": "no longer needed"}); w.Code != http.StatusNoContent {
		t.Fatalf("ungrant = %d", w.Code)
	}
	if w = naReq(mux, &operatorPrincipal, "POST", base+"/revoke", map[string]any{"reason": "retire"}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", w.Code)
	}
	if w = naReq(mux, &operatorPrincipal, "PUT", base+"/tools/dev_read", map[string]any{"reason": "r"}); w.Code != http.StatusConflict {
		t.Fatalf("grant to revoked = %d, want 409", w.Code)
	}
	if w = naReq(mux, &operatorPrincipal, "GET", "/api/native-authority/missing", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d, want 404", w.Code)
	}
	var actions []string
	rows, err := a.store.DB.QueryContext(t.Context(), `SELECT action||':'||granted_by FROM native_authority_audit ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		actions = append(actions, s)
	}
	want := fmt.Sprint([]string{"authority_created:cred-operator", "tool_granted:cred-operator", "tool_revoked:cred-operator", "authority_revoked:cred-operator"})
	if fmt.Sprint(actions) != want {
		t.Fatalf("audit = %v, want %s", actions, want)
	}
}
