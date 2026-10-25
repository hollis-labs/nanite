package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func (g *guarded) do(method, secret, path string, body any) (int, map[string]any) {
	g.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, g.url+path, bytes.NewReader(raw))
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

func secondHost(t *testing.T, a *testAPI, slug string) store.AgentHostSettings {
	return secondHostRT(t, a, slug, store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "m"})
}

func secondHostRT(t *testing.T, a *testAPI, slug string, settings store.NativeHostSettings) store.AgentHostSettings {
	t.Helper()
	def := []byte("---\nschema_version: \"2\"\ndefinition_id: def:" + slug + "\nrevision: \"1\"\nname: " + slug + "\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n")
	pin, err := a.store.InstallAgentDefinition(t.Context(), def, nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: slug, Title: slug, DefinitionRef: pin,
		Settings: settings, Enabled: true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestRetireOfAnEnrolledHostNeedsAdminAndOthersAreUnchanged(t *testing.T) {
	g, fake, _, hostID := enrollGuarded(t)
	plain := secondHost(t, g.api, "never-enrolled")
	op := g.secrets["operator"]
	if code, _ := g.post(op, "/api/native-agents/"+hostID+"/enroll", map[string]any{"reason": "r"}); code != http.StatusCreated {
		t.Fatalf("enroll = %d", code)
	}
	body := map[string]any{"reason": "r"}
	retire := "/api/agent-host-settings/" + hostID + "/retire"

	// agents:manage and maintain-class credentials are refused for an enrolled host.
	for _, who := range []string{"manager", "manageronly"} {
		code, res := g.post(g.secrets[who], retire, body)
		if code != http.StatusForbidden || res["code"] != "enrolled_retire_requires_admin" {
			t.Errorf("%s HTTP retire = %d %v", who, code, res)
		}
		text, isErr := adminMCPCall(t, g.mcp(g.secrets[who]), "host_settings_retire", map[string]any{"id": hostID, "reason": "r"})
		if !isErr || !strings.Contains(text, "enrolled_retire_requires_admin") {
			t.Errorf("%s MCP retire = %q err=%v", who, text, isErr)
		}
	}
	if _, _, d := fake.Counts(); d != 0 {
		t.Fatal("a refused retire reached Tether")
	}
	var enabled int
	_ = g.api.store.DB.QueryRowContext(t.Context(), `SELECT enabled FROM agent_host_settings WHERE id = ?`, hostID).Scan(&enabled)
	if enabled != 1 || authorityRows(t, g.api, "actor_instances") != 1 {
		t.Fatal("a refused retire changed state")
	}

	// A host that was never enrolled retires exactly as before, for agents:manage.
	if code, res := g.post(g.secrets["manager"], "/api/agent-host-settings/"+plain.ID+"/retire", body); code != http.StatusOK {
		t.Fatalf("non-enrolled retire by manager = %d %v", code, res)
	}
	// Admin may retire the enrolled one.
	code, res := g.post(op, retire, body)
	if code != http.StatusOK || res["actor"] == nil {
		t.Fatalf("admin retire = %d %v", code, res)
	}
	// Repeating on the now-retired enrolled host is still admin-only.
	if code, _ = g.post(g.secrets["manager"], retire, body); code != http.StatusForbidden {
		t.Fatalf("manager repeat = %d", code)
	}
}

func TestDeleteOfAnEnrolledHostIsRefused(t *testing.T) {
	g, _, _, hostID := enrollGuarded(t)
	plain := secondHost(t, g.api, "delete-plain")
	op := g.secrets["operator"]
	if code, _ := g.post(op, "/api/native-agents/"+hostID+"/enroll", map[string]any{"reason": "r"}); code != http.StatusCreated {
		t.Fatalf("enroll = %d", code)
	}
	code, res := g.do("DELETE", op, "/api/agent-host-settings/"+hostID, map[string]any{"revision": "x"})
	if code != http.StatusConflict || res["code"] != "enrolled_host_cannot_be_deleted" || !strings.Contains(res["error"].(string), "retire") {
		t.Fatalf("delete enrolled = %d %v", code, res)
	}
	// Still refused after retire: the record (and its URN) must outlive the host row.
	if c, _ := g.post(op, "/api/agent-host-settings/"+hostID+"/retire", map[string]any{"reason": "r"}); c != http.StatusOK {
		t.Fatalf("retire = %d", c)
	}
	if code, res = g.do("DELETE", op, "/api/agent-host-settings/"+hostID, map[string]any{"revision": "x"}); code != http.StatusConflict {
		t.Fatalf("delete retired = %d %v", code, res)
	}
	var n int
	_ = g.api.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_host_settings WHERE id = ?`, hostID).Scan(&n)
	if n != 1 {
		t.Fatal("the enrolled host row was deleted")
	}
	// A host that was never enrolled still deletes (revision check applies as before).
	if code, res = g.do("DELETE", op, "/api/agent-host-settings/"+plain.ID, map[string]any{"revision": plain.Revision}); code != http.StatusNoContent {
		t.Fatalf("delete non-enrolled = %d %v", code, res)
	}
}

func TestPutEnabledFalseLeavesTheActorUntouchedAndIsReversible(t *testing.T) {
	g, fake, _, _ := enrollGuarded(t)
	// A CLI host: PUT does not re-validate an API model for it.
	h := secondHostRT(t, g.api, "put-cli", store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "sonnet", Directories: []string{t.TempDir()}})
	hostID := h.ID
	op := g.secrets["operator"]
	code, enr := g.post(op, "/api/native-agents/"+hostID+"/enroll", map[string]any{"reason": "r"})
	if code != http.StatusCreated {
		t.Fatalf("enroll = %d", code)
	}
	host, err := g.api.store.GetAgentHostSettings(t.Context(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	put := func(enabled bool, rev string) {
		t.Helper()
		c, res := g.do("PUT", op, "/api/agent-host-settings/"+hostID, map[string]any{"slug": host.Slug, "title": host.Title,
			"definition_ref": host.DefinitionRef, "settings": host.Settings, "enabled": enabled, "revision": rev})
		if c != http.StatusOK {
			t.Fatalf("PUT enabled=%v = %d %v", enabled, c, res)
		}
	}
	put(false, host.Revision)
	rec, _ := g.api.store.ActorEnrollment().Get(t.Context(), hostID)
	var bindingEnabled int
	_ = g.api.store.DB.QueryRowContext(t.Context(), `SELECT enabled FROM agent_actor_bindings WHERE actor_uri = ?`, enr["urn"]).Scan(&bindingEnabled)
	inst, _ := g.api.store.GetDurableAgentInstanceByURN(t.Context(), enr["urn"].(string))
	if rec.Status != store.EnrollmentEnrolled || bindingEnabled != 1 || inst.Status == store.DurableAgentStatusArchived {
		t.Fatalf("PUT enabled=false touched the actor: %+v binding=%d inst=%+v", rec, bindingEnabled, inst)
	}
	if _, _, d := fake.Counts(); d != 0 {
		t.Fatal("PUT enabled=false deregistered the actor")
	}
	cur, _ := g.api.store.GetAgentHostSettings(t.Context(), hostID)
	put(true, cur.Revision)
	if rec, _ = g.api.store.ActorEnrollment().Get(t.Context(), hostID); rec.Status != store.EnrollmentEnrolled || rec.URN != enr["urn"] {
		t.Fatalf("after re-enable: %+v", rec)
	}
}

func TestReenrollAfterRetireStays409AndANewHostGetsANewURN(t *testing.T) {
	g, fake, _, hostID := enrollGuarded(t)
	op := g.secrets["operator"]
	_, first := g.post(op, "/api/native-agents/"+hostID+"/enroll", map[string]any{"reason": "r"})
	if c, _ := g.post(op, "/api/agent-host-settings/"+hostID+"/retire", map[string]any{"reason": "r"}); c != http.StatusOK {
		t.Fatalf("retire = %d", c)
	}
	code, res := g.post(op, "/api/native-agents/"+hostID+"/enroll", map[string]any{"reason": "r"})
	if code != http.StatusConflict || res["code"] != "retired" {
		t.Fatalf("re-enroll = %d %v", code, res)
	}
	fresh := secondHost(t, g.api, "replacement")
	code, second := g.post(op, "/api/native-agents/"+fresh.ID+"/enroll", map[string]any{"reason": "r"})
	if code != http.StatusCreated || second["urn"] == first["urn"] || second["external_key"] == first["external_key"] {
		t.Fatalf("new host = %d %v (old %v)", code, second, first)
	}
	if len(fake.Entries()) != 2 {
		t.Fatalf("tether entries = %d", len(fake.Entries()))
	}
}
