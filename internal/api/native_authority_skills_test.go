package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/store"
)

// skillAuthority installs a definition that requests the skill "review", host
// settings for it and a live authority, and returns the authority id and the
// skill's pinned digest.
func skillAuthority(t *testing.T, a *testAPI) (authorityID, digest string) {
	t.Helper()
	blob, err := agentpolicy.EncodeSkillTree(map[string][]byte{"SKILL.md": []byte("# review\n")})
	if err != nil {
		t.Fatal(err)
	}
	digest = agentdef.ArtifactDigest(blob)
	def := fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: def:api-skill\nrevision: \"1\"\nname: api-skill\ndescription: Requests a skill.\nbehavior:\n  purpose: Complete the requested work.\nrequirements:\n  skills:\n    - name: review\n      content: {uri: \"file:skills/review\", digest: \"%s\"}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nSkill agent.\n", digest)
	pin, err := a.store.InstallAgentDefinition(t.Context(), []byte(def), []store.DefinitionResource{{URI: "file:skills/review", Content: blob}})
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: "api-skill", Title: "api-skill", DefinitionRef: pin,
		Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "fixture-model"}, Enabled: true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := a.store.LocalAuthority().CreateAuthority(t.Context(), "cred-setup", host.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	return auth.ID, digest
}

// Skill grants follow the tool-grant rule exactly: admin only. An agents:manage
// credential (which may read authority) cannot grant or revoke a skill, and
// neither can maintain, an agent credential, or no credential.
func TestSkillGrantRoutesFollowTheToolGrantRule(t *testing.T) {
	a, mux := newTestAPI(t)
	id, digest := skillAuthority(t, a)
	skillPath := "/api/native-authority/" + id + "/skills/review"
	toolPath := "/api/native-authority/" + id + "/tools/dev_read"
	grant := map[string]any{"digest": digest, "reason": "approved"}
	maintainOnly := apiauth.Principal{CredentialID: "cred-maintain", Kind: apiauth.KindClient, Scopes: []apiauth.Scope{apiauth.ScopeRead, apiauth.ScopeOperate, apiauth.ScopeMaintain}}

	for name, p := range map[string]*apiauth.Principal{
		"agents:manage only (+operate)": &managerClientPrincipal,
		"maintain only":                 &maintainOnly,
		"agent credential":              &agentPrincipal,
		"no principal":                  nil,
	} {
		toolPut := naReq(mux, p, "PUT", toolPath, map[string]any{"reason": "r"})
		if w := naReq(mux, p, "PUT", skillPath, grant); w.Code != toolPut.Code || (w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized) {
			t.Errorf("%s: PUT skill = %d, tool grant = %d; both must be refused alike", name, w.Code, toolPut.Code)
		}
		if w := naReq(mux, p, "DELETE", skillPath, map[string]any{"reason": "r"}); w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
			t.Errorf("%s: DELETE skill = %d, want refused", name, w.Code)
		}
	}
	if n := authorityRows(t, a, "native_authority_skills"); n != 0 {
		t.Fatalf("a refused caller granted %d skills", n)
	}

	// An admin grants; the response and the authority view say so.
	w := naReq(mux, &adminClientPrincipal, "PUT", skillPath, grant)
	var got struct {
		AuthorityID    string `json:"authority_id"`
		Skill          string `json:"skill"`
		Digest         string `json:"digest"`
		GrantedAt      string `json:"granted_at"`
		AlreadyGranted bool   `json:"already_granted"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.AuthorityID != id || got.Skill != "review" || got.Digest != digest || got.GrantedAt == "" || got.AlreadyGranted {
		t.Fatalf("grant = %d %s", w.Code, w.Body)
	}
	if w = naReq(mux, &adminClientPrincipal, "PUT", skillPath, grant); w.Code != 200 || !strings.Contains(w.Body.String(), `"already_granted":true`) {
		t.Fatalf("repeat = %d %s", w.Code, w.Body)
	}
	// agents:manage may read what was granted.
	w = naReq(mux, &managerClientPrincipal, "GET", "/api/native-authority/"+id, nil)
	var view struct {
		Skills []struct{ Name, Digest string } `json:"skills"`
		Tools  []any                           `json:"tools"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if w.Code != 200 || len(view.Skills) != 1 || view.Skills[0].Name != "review" || view.Skills[0].Digest != digest {
		t.Fatalf("authority view = %d %s", w.Code, w.Body)
	}
	// The recorded actor is the authenticated credential id.
	var by string
	_ = a.store.DB.QueryRowContext(t.Context(), `SELECT granted_by FROM native_authority_audit WHERE action='skill_granted'`).Scan(&by)
	if by != adminClientPrincipal.CredentialID {
		t.Fatalf("granted_by = %q, want the credential id", by)
	}
	// agents:manage cannot revoke it either; admin can.
	if w = naReq(mux, &managerClientPrincipal, "DELETE", skillPath, map[string]any{"reason": "r"}); w.Code != http.StatusForbidden {
		t.Fatalf("manager revoke = %d", w.Code)
	}
	if n := authorityRows(t, a, "native_authority_skills"); n != 1 {
		t.Fatalf("grants = %d after a refused revoke", n)
	}
	if w = naReq(mux, &adminClientPrincipal, "DELETE", skillPath, map[string]any{"reason": "withdrawn"}); w.Code != http.StatusNoContent {
		t.Fatalf("admin revoke = %d %s", w.Code, w.Body)
	}
	if n := authorityRows(t, a, "native_authority_skills"); n != 0 {
		t.Fatalf("grants = %d after revoke", n)
	}
}

func TestSkillGrantErrorCodes(t *testing.T) {
	a, mux := newTestAPI(t)
	id, digest := skillAuthority(t, a)
	put := func(path string, body any) (int, string) {
		w := naReq(mux, &adminClientPrincipal, "PUT", path, body)
		return w.Code, w.Body.String()
	}
	base := "/api/native-authority/" + id + "/skills/"
	wrong := "sha256:" + strings.Repeat("c", 64)
	for name, c := range map[string]struct {
		path string
		body any
		code int
		text string
	}{
		"wrong digest":      {base + "review", map[string]any{"digest": wrong, "reason": "r"}, 422, "skill_digest_mismatch"},
		"not requested":     {base + "other", map[string]any{"digest": digest, "reason": "r"}, 422, "skill_not_requested"},
		"malformed digest":  {base + "review", map[string]any{"digest": "sha256:zz", "reason": "r"}, 400, ""},
		"missing digest":    {base + "review", map[string]any{"reason": "r"}, 400, ""},
		"missing reason":    {base + "review", map[string]any{"digest": digest}, 400, ""},
		"long reason":       {base + "review", map[string]any{"digest": digest, "reason": strings.Repeat("x", 501)}, 400, ""},
		"bad skill name":    {base + "Bad_Name", map[string]any{"digest": digest, "reason": "r"}, 400, ""},
		"unknown authority": {"/api/native-authority/nope/skills/review", map[string]any{"digest": digest, "reason": "r"}, 404, ""},
	} {
		if code, body := put(c.path, c.body); code != c.code || !strings.Contains(body, c.text) {
			t.Errorf("%s: %d %s, want %d %q", name, code, body, c.code, c.text)
		}
	}
	// A body cannot name who grants: unknown fields are refused, and the audit
	// would record the credential anyway.
	if code, _ := put(base+"review", map[string]any{"digest": digest, "reason": "r", "granted_by": "someone-else"}); code == 200 {
		var by string
		_ = a.store.DB.QueryRowContext(t.Context(), `SELECT granted_by FROM native_authority_audit WHERE action='skill_granted'`).Scan(&by)
		if by != adminClientPrincipal.CredentialID {
			t.Fatalf("granted_by = %q", by)
		}
	}
	if n := authorityRows(t, a, "native_authority_skills"); n > 1 {
		t.Fatalf("grants = %d", n)
	}
	// Revoked authority: 409.
	if err := a.store.LocalAuthority().RevokeAuthority(t.Context(), "c", id, "r"); err != nil {
		t.Fatal(err)
	}
	if code, body := put(base+"review", map[string]any{"digest": digest, "reason": "r"}); code != http.StatusConflict {
		t.Errorf("revoked authority = %d %s, want 409", code, body)
	}
}

// End to end through the real auth guard and the admin MCP endpoint.
func TestSkillGrantThroughTheGuardAndMCP(t *testing.T) {
	g := newGuarded(t)
	id, digest := skillAuthority(t, g.api)
	path := "/api/native-authority/" + id + "/skills/review"
	if code, _ := g.http(g.secrets["manager"], "PUT", path); code != http.StatusForbidden {
		t.Errorf("agents:manage PUT skill = %d, want 403", code)
	}
	if code, _ := g.http(g.secrets["manager"], "DELETE", path); code != http.StatusForbidden {
		t.Errorf("agents:manage DELETE skill = %d, want 403", code)
	}
	if code, _ := g.http(g.secrets["agent"], "PUT", path); code != http.StatusForbidden {
		t.Errorf("agent credential PUT skill = %d, want 403", code)
	}
	mgr := g.mcp(g.secrets["manager"])
	for tool, args := range map[string]map[string]any{
		"skill_grant":   {"id": id, "skill": "review", "digest": digest, "reason": "r"},
		"skill_ungrant": {"id": id, "skill": "review", "reason": "r"},
	} {
		if text, isErr := adminMCPCall(t, mgr, tool, args); !isErr || !strings.Contains(text, "needs the admin scope") {
			t.Errorf("%s via agents:manage = %q err=%v", tool, text, isErr)
		}
	}
	if n := authorityRows(t, g.api, "native_authority_skills"); n != 0 {
		t.Fatalf("%d skills granted by refused callers", n)
	}
	op := g.mcp(g.secrets["operator"])
	if text, isErr := adminMCPCall(t, op, "skill_grant", map[string]any{"id": id, "skill": "review", "digest": digest, "reason": "approved"}); isErr || !strings.Contains(text, digest) {
		t.Fatalf("operator skill_grant = %q err=%v", text, isErr)
	}
	if n := authorityRows(t, g.api, "native_authority_skills"); n != 1 {
		t.Fatalf("grants = %d", n)
	}
	if text, isErr := adminMCPCall(t, op, "skill_ungrant", map[string]any{"id": id, "skill": "review", "reason": "withdrawn"}); isErr {
		t.Fatalf("operator skill_ungrant = %q", text)
	}
	if n := authorityRows(t, g.api, "native_authority_skills"); n != 0 {
		t.Fatalf("grants = %d after ungrant", n)
	}
}
