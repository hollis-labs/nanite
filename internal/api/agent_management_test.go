package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

const setupFixtureDef = "---\nschema_version: \"2\"\ndefinition_id: def:http-setup\nrevision: \"1\"\nname: http-setup\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n"

var adminClientPrincipal = apiauth.Principal{CredentialID: "cred-admin-client", Kind: apiauth.KindClient, Label: "tachyon", Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeAdmin}} // #nosec G101 -- test fixture id, not a credential

func TestAgentManagementRoutesRefuseNonAdmin(t *testing.T) {
	_, mux := newTestAPI(t)
	for name, p := range map[string]*apiauth.Principal{"no principal": nil, "agent credential": &agentPrincipal, "client without admin": &clientPrincipal} {
		for _, c := range []struct{ method, path string }{{"POST", "/api/agent-setup"}, {"GET", "/api/native-agents"}, {"GET", "/api/native-agents/x"}} {
			var body any
			if c.method == "POST" {
				body = map[string]any{"reason": "r"}
			}
			w := naReq(mux, p, c.method, c.path, body)
			if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s %s = %d, want 401/403", name, c.method, c.path, w.Code)
			}
		}
	}
}

func TestAgentSetupOverHTTPIsAtomicIdempotentAndViewable(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a) // registers the dev_read tool
	a.store.DB.ExecContext(t.Context(), `DELETE FROM agent_host_settings`)
	dir := filepath.Join(a.Services.AgentSetup.DirectoryRoot(t.Context()), "work")
	body := map[string]any{"artifact": setupFixtureDef, "provider": "claude", "runtime": "cli", "tools": []string{"dev_read"}, "reason": "test", "directories": []string{dir}}

	// Refusals write nothing.
	before := authorityRows(t, a, "agent_definitions") + authorityRows(t, a, "agent_host_settings") + authorityRows(t, a, "native_authorities")
	for name, mut := range map[string]func(map[string]any){
		"no reason":     func(m map[string]any) { m["reason"] = "" },
		"unknown tool":  func(m map[string]any) { m["tools"] = []string{"no_such"} },
		"no tools":      func(m map[string]any) { m["tools"] = []string{} },
		"dir outside":   func(m map[string]any) { m["directories"] = []string{"/tmp/elsewhere-" + t.Name()} },
		"dir dotdot":    func(m map[string]any) { m["directories"] = []string{dir + "/../../escape"} },
		"relative dir":  func(m map[string]any) { m["directories"] = []string{"rel"} },
		"bad runtime":   func(m map[string]any) { m["runtime"] = "vm" },
		"both sources":  func(m map[string]any) { m["archetype"] = "x" },
		"no definition": func(m map[string]any) { delete(m, "artifact") },
	} {
		bad := map[string]any{}
		for k, v := range body {
			bad[k] = v
		}
		mut(bad)
		if w := naReq(mux, &adminClientPrincipal, "POST", "/api/agent-setup", bad); w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body)
		}
	}
	if after := authorityRows(t, a, "agent_definitions") + authorityRows(t, a, "agent_host_settings") + authorityRows(t, a, "native_authorities"); after != before {
		t.Fatalf("refused requests wrote rows: %d -> %d", before, after)
	}

	w := naReq(mux, &adminClientPrincipal, "POST", "/api/agent-setup", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	var first map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &first)
	if first["slug"] != "def-http-setup" || first["created"] != true || first["authority_id"] == "" {
		t.Fatalf("first = %v", first)
	}
	req, _ := first["session_request"].(map[string]any)
	if req["path"] != "/api/agent/v1/sessions" {
		t.Fatalf("session_request = %v", req)
	}
	// The authority records the credential, not anything in the body.
	var by string
	_ = a.store.DB.QueryRowContext(t.Context(), `SELECT granted_by FROM native_authority_audit WHERE authority_id=? AND action='authority_created'`, first["authority_id"]).Scan(&by)
	if by != adminClientPrincipal.CredentialID {
		t.Fatalf("granted_by = %q", by)
	}

	w = naReq(mux, &adminClientPrincipal, "POST", "/api/agent-setup", body)
	var second map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if w.Code != http.StatusOK || second["created"] != false || second["authority_id"] != first["authority_id"] {
		t.Fatalf("rerun = %d %v", w.Code, second)
	}

	// Same slug, different settings: conflict, not silent reuse.
	diff := map[string]any{}
	for k, v := range body {
		diff[k] = v
	}
	diff["model"] = "other"
	if w = naReq(mux, &adminClientPrincipal, "POST", "/api/agent-setup", diff); w.Code != http.StatusConflict {
		t.Fatalf("conflict = %d %s", w.Code, w.Body)
	}

	w = naReq(mux, &adminClientPrincipal, "GET", "/api/native-agents/def-http-setup", nil)
	var view struct {
		State       string   `json:"state"`
		AuthorityID string   `json:"authority_id"`
		Tools       []string `json:"tools"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if w.Code != 200 || view.State != "ready" || view.AuthorityID != first["authority_id"] || strings.Join(view.Tools, ",") != "dev_read" {
		t.Fatalf("view = %d %s", w.Code, w.Body)
	}
	if w = naReq(mux, &adminClientPrincipal, "GET", "/api/native-agents", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "def-http-setup") {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w = naReq(mux, &adminClientPrincipal, "GET", "/api/native-agents/nope", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", w.Code)
	}

	// Ungrant through the existing route, then the view reflects it.
	w = naReq(mux, &adminClientPrincipal, "DELETE", "/api/native-authority/"+view.AuthorityID+"/tools/dev_read", map[string]any{"reason": "r"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("ungrant = %d %s", w.Code, w.Body)
	}
	w = naReq(mux, &adminClientPrincipal, "GET", "/api/native-agents/def-http-setup", nil)
	view.Tools = nil
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if len(view.Tools) != 0 {
		t.Fatalf("tools after ungrant = %v", view.Tools)
	}
}

func TestAgentSetupRefusalsCreateNoDirectories(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a)
	root := a.Services.AgentSetup.DirectoryRoot(t.Context())
	good := filepath.Join(root, "first")
	body := map[string]any{"artifact": setupFixtureDef, "provider": "claude", "runtime": "cli", "tools": []string{"dev_read"}, "reason": "t", "directories": []string{good}}
	if w := naReq(mux, &operatorPrincipal, "POST", "/api/agent-setup", body); w.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	// A slug conflict must not leave the new directory behind.
	other := filepath.Join(root, "second")
	body["directories"] = []string{other}
	if w := naReq(mux, &operatorPrincipal, "POST", "/api/agent-setup", body); w.Code != http.StatusConflict {
		t.Fatalf("conflict = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("conflicting request created %s (err %v)", other, err)
	}
	// A symlink inside the root cannot lead out of it.
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	body["slug"], body["directories"] = "linked", []string{filepath.Join(link, "x")}
	if w := naReq(mux, &operatorPrincipal, "POST", "/api/agent-setup", body); w.Code != http.StatusBadRequest {
		t.Fatalf("symlink escape = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); !os.IsNotExist(err) {
		t.Fatal("directory created through a symlink")
	}
}

func TestAgentSetupAllToolsAndOperatorCredentialOverHTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a)
	body := map[string]any{"artifact": setupFixtureDef, "provider": "claude", "runtime": "cli", "tools": []string{"*"}, "reason": "all"}
	w := naReq(mux, &operatorPrincipal, "POST", "/api/agent-setup", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	var res struct {
		Tools       []string `json:"tools"`
		Directories []string `json:"directories"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if !strings.Contains(strings.Join(res.Tools, ","), "dev_read") {
		t.Fatalf("* did not grant the available tools: %v", res.Tools)
	}
	root := a.Services.AgentSetup.DirectoryRoot(t.Context())
	if len(res.Directories) != 1 || res.Directories[0] != filepath.Join(root, "def-http-setup") {
		t.Fatalf("default directory = %v, want under %s", res.Directories, root)
	}
	if fi, err := os.Stat(res.Directories[0]); err != nil || !fi.IsDir() {
		t.Fatalf("directory not created after commit: %v", err)
	}
}

func TestNativeAgentReadsAnswerHEAD(t *testing.T) {
	a, mux := newTestAPI(t)
	nativeAuthorityHost(t, a)
	for path, want := range map[string]int{"/api/native-agents": 200, "/api/native-agents/native-authority-api": 200, "/api/native-agents/missing": 404} {
		if w := naReq(mux, &operatorPrincipal, "HEAD", path, nil); w.Code != want {
			t.Errorf("HEAD %s = %d, want %d", path, w.Code, want)
		}
		if w := naReq(mux, &clientPrincipal, "HEAD", path, nil); w.Code != http.StatusForbidden {
			t.Errorf("HEAD %s as non-admin = %d", path, w.Code)
		}
	}
}

func TestAgentSetupErrorMapsUnknownErrorsTo500WithoutDetail(t *testing.T) {
	a, _ := newTestAPI(t)
	for _, err := range []error{
		errors.New("create agent directory /secret/path: permission denied"),
		errors.New("sql: database is locked"),
	} {
		w := httptest.NewRecorder()
		a.agentSetupError(w, err)
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "sql") {
			t.Errorf("%v -> %d %s", err, w.Code, w.Body)
		}
	}
}
