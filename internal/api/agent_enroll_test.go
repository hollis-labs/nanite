package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tetherregistry/tetherregistrytest"
)

// enrollGuarded is the API behind the real auth guard with a fake Tether and a
// fake registry token file wired into the enrollment service.
func enrollGuarded(t *testing.T) (*guarded, *tetherregistrytest.Fake, string, string) {
	t.Helper()
	g := newGuarded(t)
	fake := tetherregistrytest.New(t)
	token := tetherregistrytest.TokenFile(t)
	g.api.Services.Enrollment.Registrar = fake.Registrar(token)
	host := nativeAuthorityHost(t, g.api)
	return g, fake, token, host.ID
}

func (g *guarded) post(secret, path string, body any) (int, map[string]any) {
	g.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, g.url+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestEnrollRouteAuth(t *testing.T) {
	g, fake, _, hostID := enrollGuarded(t)
	path := "/api/native-agents/" + hostID + "/enroll"
	body := map[string]any{"reason": "r"}

	// Unauthenticated: 401. Every non-admin kind: 403. agents:manage alone is
	// not enough (ADR: admin for the first version, like grants).
	if code, _ := g.post("", path, body); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d, want 401", code)
	}
	for _, who := range []string{"client", "manager", "manageronly", "agent"} {
		if code, _ := g.post(g.secrets[who], path, body); code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", who, code)
		}
	}
	if reg, _, _ := fake.Counts(); reg != 0 {
		t.Fatal("a refused caller reached Tether")
	}
	if n := authorityRows(t, g.api, "agent_enrollments"); n != 0 {
		t.Fatalf("refused callers wrote %d enrollment rows", n)
	}

	code, res := g.post(g.secrets["operator"], path, body)
	if code != http.StatusCreated || !strings.HasPrefix(res["urn"].(string), "msg://agent/agent-mux/agt_") || res["instance_id"] == "" || res["status"] != "sleeping" {
		t.Fatalf("operator enroll = %d %v", code, res)
	}
	binding, _ := res["binding"].(map[string]any)
	if binding["actor_uri"] != res["urn"] || binding["enabled"] != true {
		t.Fatalf("binding = %v", binding)
	}
	// The recorded principal is the credential, never a request field.
	var receipt string
	_ = g.api.store.DB.QueryRowContext(t.Context(), `SELECT binding_receipt FROM agent_actor_bindings`).Scan(&receipt)
	if strings.Contains(receipt, `"granted_by":"r"`) || !strings.Contains(receipt, `"granted_by":"`) {
		t.Fatalf("receipt = %s", receipt)
	}
	// Repeat: 200 and the same URN.
	code2, res2 := g.post(g.secrets["operator"], path, body)
	if code2 != http.StatusOK || res2["urn"] != res["urn"] || res2["already_enrolled"] != true {
		t.Fatalf("repeat = %d %v", code2, res2)
	}
}

func TestEnrollRouteTypedErrors(t *testing.T) {
	g, fake, token, hostID := enrollGuarded(t)
	op := g.secrets["operator"]
	path := "/api/native-agents/" + hostID + "/enroll"

	if code, _ := g.post(op, path, map[string]any{"reason": ""}); code != http.StatusBadRequest {
		t.Errorf("no reason = %d", code)
	}
	if code, _ := g.post(op, path, map[string]any{"reason": "r", "unknown_field": 1}); code != http.StatusBadRequest {
		t.Errorf("unknown field = %d", code)
	}
	if code, _ := g.post(op, "/api/native-agents/nope/enroll", map[string]any{"reason": "r"}); code != http.StatusNotFound {
		t.Errorf("unknown host = %d", code)
	}
	if code, res := g.post(op, path, map[string]any{"reason": "r", "revision": "stale"}); code != http.StatusConflict || res["code"] != "stale_revision" {
		t.Errorf("stale = %d %v", code, res)
	}

	cases := []struct {
		name    string
		arrange func()
		status  int
		code    string
	}{
		{"token missing", func() { _ = os.Remove(token) }, http.StatusServiceUnavailable, "registry_token_missing"},
		{"token insecure", func() { _ = os.WriteFile(token, []byte(tetherregistrytest.Token), 0o644); _ = os.Chmod(token, 0o644) }, http.StatusServiceUnavailable, "registry_token_insecure"},
		{"tether down", func() {
			_ = os.WriteFile(token, []byte(tetherregistrytest.Token), 0o600)
			_ = os.Chmod(token, 0o600)
			fake.Drop()
		}, http.StatusBadGateway, "tether_unreachable"},
		{"tether refuses", func() { fake.FailWith(403, "forbidden") }, http.StatusBadGateway, "tether_refused"},
	}
	for _, c := range cases {
		c.arrange()
		code, res := g.post(op, path, map[string]any{"reason": "r"})
		if code != c.status || res["code"] != c.code {
			t.Errorf("%s = %d %v, want %d %s", c.name, code, res, c.status, c.code)
		}
		if s, _ := json.Marshal(res); strings.Contains(string(s), tetherregistrytest.Token) {
			t.Errorf("%s leaked the token", c.name)
		}
	}
	if n := authorityRows(t, g.api, "agent_actor_bindings"); n != 0 {
		t.Fatalf("failed enrollments wrote %d bindings", n)
	}
	fake.Heal()
	if code, res := g.post(op, path, map[string]any{"reason": "r"}); code != http.StatusCreated {
		t.Fatalf("recovery = %d %v", code, res)
	}
}

func TestEnrollMCPToolIsAdminOnlyAndRetireDeregisters(t *testing.T) {
	g, fake, _, hostID := enrollGuarded(t)
	args := map[string]any{"id": hostID, "reason": "r"}

	for _, who := range []string{"manager", "manageronly"} {
		text, isErr := adminMCPCall(t, g.mcp(g.secrets[who]), "agent_enroll", args)
		if !isErr || !strings.Contains(text, "admin scope") {
			t.Errorf("%s agent_enroll = %q err=%v, want the admin refusal", who, text, isErr)
		}
	}
	text, isErr := adminMCPCall(t, g.mcp(g.secrets["operator"]), "agent_enroll", args)
	if isErr || !strings.Contains(text, "msg://agent/agent-mux/agt_") {
		t.Fatalf("operator agent_enroll = %q err=%v", text, isErr)
	}
	if n := authorityRows(t, g.api, "actor_instances"); n != 1 {
		t.Fatalf("instances = %d", n)
	}

	// host_settings_retire retires the actor first, then disables the host.
	fake.Drop()
	text, isErr = adminMCPCall(t, g.mcp(g.secrets["operator"]), "host_settings_retire", args)
	if isErr || !strings.Contains(text, `"deregister_pending":true`) {
		t.Fatalf("retire with Tether down = %q err=%v", text, isErr)
	}
	var enabled int
	_ = g.api.store.DB.QueryRowContext(t.Context(), `SELECT enabled FROM agent_host_settings WHERE id = ?`, hostID).Scan(&enabled)
	if enabled != 0 {
		t.Fatal("host settings were not disabled")
	}
	fake.Heal()
	text, isErr = adminMCPCall(t, g.mcp(g.secrets["operator"]), "host_settings_retire", args)
	if isErr || strings.Contains(text, `"deregister_pending":true`) {
		t.Fatalf("retire retry = %q err=%v", text, isErr)
	}
	if es := fake.Entries(); len(es) != 1 || es[0].Status != "deprecated" {
		t.Fatalf("tether entries = %+v", es)
	}
}

func TestEnrollRouteIsClassifiedAdminInTheRouteTable(t *testing.T) {
	if scope, ok := apiauth.ScopeFor("POST", "/api/native-agents/{id}/enroll"); !ok || scope != apiauth.ScopeAdmin {
		t.Fatalf("scope = %q ok=%v, want admin", scope, ok)
	}
	if scope, ok := apiauth.ScopeFor("GET", "/api/native-agents/{id}"); !ok || scope != apiauth.ScopeAgentsManage {
		t.Fatalf("native agent view = %q ok=%v, want agents:manage", scope, ok)
	}
}

// Ending a session through the session service (the v1 stop route and DELETE
// both end here) detaches the actor, via the container's archive hook.
func TestEndingASessionClearsTheActorsCurrentSession(t *testing.T) {
	g, _, _, hostID := enrollGuarded(t)
	res, err := g.api.Services.Enrollment.Enroll(t.Context(), "cred-test", hostID, "r", "")
	if err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{Provider: "anthropic"}
	if err = g.api.store.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	g.api.Services.Enrollment.AttachSession(t.Context(), hostID, sess.ID)
	inst, _ := g.api.store.GetDurableAgentInstanceByURN(t.Context(), res.URN)
	if inst.CurrentSessionID != sess.ID || inst.Status != store.DurableAgentStatusActive {
		t.Fatalf("after start: %+v", inst)
	}
	if err = g.api.Services.Sessions.Archive(t.Context(), sess.ID); err != nil {
		t.Fatal(err)
	}
	inst, _ = g.api.store.GetDurableAgentInstanceByURN(t.Context(), res.URN)
	if inst.CurrentSessionID != "" || inst.Status != store.DurableAgentStatusSleeping {
		t.Fatalf("after stop: %+v", inst)
	}
}
