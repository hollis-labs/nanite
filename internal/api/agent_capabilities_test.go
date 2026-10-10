package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// A fresh host settings row is deliberately created without an actor binding.
// This fixture never fabricates an enrollment receipt or an authority issuer.
func capabilityHostWithoutActor(t *testing.T, a *testAPI, slug, source string) store.AgentHostSettings {
	t.Helper()
	embedded, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: slug, Title: "Capability host", Source: source, DefinitionRef: embedded.Ref.MeshRef(), Enabled: true, Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "fixture-model"}})
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestAgentCapabilitiesPublicMuxKeepsHistoricalCatalogInvisibleAndRequiresActor(t *testing.T) {
	a, mux := newTestAPI(t)
	const historical = "historical-capability-api"
	retiredAPIHistoricalProfile(t, a, historical, "user")
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_known_tools(agent_id,tool_name,pinned,activation_count,last_used_at,added_at,ttl_seconds,reason) VALUES(?,'historical-tool',1,7,'retained-last-used','retained-added',3600,'retained reason')`, historical); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_known_skills(agent_id,skill_name,pinned,activation_count,last_used_at,added_at,ttl_seconds,reason) VALUES(?,'historical-skill',1,8,'retained-last-used','retained-added',600,'retained reason')`, historical); err != nil {
		t.Fatal(err)
	}
	host := capabilityHostWithoutActor(t, a, "capability-api-fresh", "user")
	queries := []string{`SELECT * FROM agent_known_tools ORDER BY agent_id,tool_name`, `SELECT * FROM agent_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM actor_known_tools ORDER BY agent_id,tool_name`, `SELECT * FROM actor_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM agent_host_settings ORDER BY id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, resource := range []struct{ path, name, body string }{
		{"known-tools", "historical-tool", `{"tool_name":"historical-tool","pinned":true,"reason":"claimed authority","actor_uri":"urn:claimed"}`},
		{"known-skills", "historical-skill", `{"skill_name":"historical-skill","pinned":true,"reason":"claimed authority","actor_uri":"urn:claimed"}`},
	} {
		for _, id := range []string{historical, "missing-agent"} {
			for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
				path := "/api/agents/" + id + "/" + resource.path
				if method == "PUT" || method == "DELETE" {
					path += "/" + resource.name
				}
				w := retiredAPIRequest(t, mux, method, path, resource.body)
				if w.Code != http.StatusNotFound {
					t.Fatalf("historical/missing host %s %s: %d %s", method, path, w.Code, w.Body.String())
				}
			}
		}
		base := "/api/agents/" + host.ID + "/" + resource.path
		w := retiredAPIRequest(t, mux, "GET", base, "")
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatalf("fresh catalog inherited historical capabilities: %d %s", w.Code, w.Body.String())
		}
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			w = retiredAPIRequest(t, mux, method, base+"/"+resource.name, resource.body)
			if w.Code != http.StatusNotFound {
				t.Fatalf("absent operational capability %s: %d %s", method, w.Code, w.Body.String())
			}
		}
		w = retiredAPIRequest(t, mux, "POST", base, resource.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "verified enabled actor binding required") {
			t.Fatalf("host UUID accepted as actor authority: %d %s", w.Code, w.Body.String())
		}
	}
	for i, q := range queries {
		retiredAPIHistoryUnchanged(t, a, q, before[i])
	}
}

func TestAgentCapabilitiesPublicMuxPreservesApplicableBodyAndManagedSourceGuards(t *testing.T) {
	a, mux := newTestAPI(t)
	host := capabilityHostWithoutActor(t, a, "capability-guard-user", "user")
	internal := capabilityHostWithoutActor(t, a, "capability-guard-internal", "internal")
	for _, resource := range []struct{ path, key string }{{"known-tools", "tool_name"}, {"known-skills", "skill_name"}} {
		base := "/api/agents/" + host.ID + "/" + resource.path
		for _, body := range []string{"not json", `{}`, `{"agent_id":"different","` + resource.key + `":"fixture"}`} {
			w := retiredAPIRequest(t, mux, "POST", base, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid active capability body=%s: %d %s", body, w.Code, w.Body.String())
			}
		}
		w := retiredAPIRequest(t, mux, "POST", "/api/agents/"+internal.ID+"/"+resource.path, `{"`+resource.key+`":"fixture"}`)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"manage_class":"internal"`) {
			t.Fatalf("internal host guard: %d %s", w.Code, w.Body.String())
		}
	}
	for _, query := range []string{`SELECT * FROM actor_known_tools`, `SELECT * FROM actor_known_skills`, `SELECT * FROM agent_actor_bindings`} {
		if rows := retiredAPISnapshot(t, a, query); len(rows) != 0 {
			t.Fatal("rejected body created operational authority", query, rows)
		}
	}
}

func TestRetiredCapabilitiesPublicMuxPreservesProceduresAndAppliedSeedHistory(t *testing.T) {
	a, mux := newTestAPI(t)
	const owner = "historical-sop-seed-api"
	retiredAPIHistoricalProfile(t, a, owner, "user")
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_procedures(agent_id,name,body,scope,created_at,updated_at) VALUES(?,'retained-sop','Private retained SOP','shared','retained-created','retained-updated')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.ExecContext(t.Context(), `INSERT INTO agent_knowledge_seed(agent_id,seed_key,namespace,body,tags_json,applied_at) VALUES(?,'unapplied','project/private-fixture','Retained unapplied','["private"]',NULL),(?,'applied','project/private-fixture','Retained applied','[]','2026-10-01')`, owner, owner); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_procedures ORDER BY agent_id,name`, `SELECT * FROM agent_knowledge_seed ORDER BY agent_id,seed_key`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, agentID := range []string{owner, "missing-agent"} {
		for _, resource := range []string{"procedures", "knowledge-seeds"} {
			base := "/api/agents/" + agentID + "/" + resource
			for _, c := range []struct{ method, path, body string }{
				{"GET", base, ""}, {"GET", base + "/retained-sop", ""},
				{"POST", base, `{"name":"new","seed_key":"new","body":"replacement","tags":["changed"]}`},
				{"POST", base, "not json"}, {"PUT", base + "/unapplied", `{"body":"changed","namespace":"project/other"}`}, {"DELETE", base + "/applied", ""},
			} {
				requireRetiredAPI(t, retiredAPIRequest(t, mux, c.method, c.path, c.body))
			}
		}
		for _, key := range []string{"unapplied", "applied", "missing"} {
			requireRetiredAPI(t, retiredAPIRequest(t, mux, "POST", "/api/agents/"+agentID+"/knowledge-seeds/"+key+"/mark-applied", `{}`))
		}
	}
	for i, q := range queries {
		retiredAPIHistoryUnchanged(t, a, q, before[i])
	}
	// Applied state is retained raw historical data; it is not a successful
	// response shape from the retired mutable seed endpoint.
	rows := retiredAPISnapshot(t, a, queries[1])
	if len(rows) != 2 {
		t.Fatal("historical seed preservation controls missing")
	}
}
