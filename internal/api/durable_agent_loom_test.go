package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

func loomAuthoritySnapshot(t *testing.T, a *testAPI) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, q := range []string{
		`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`,
		`SELECT * FROM durable_agent_instances ORDER BY id`, `SELECT * FROM agent_schedules ORDER BY id`,
		`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM plugin_reflex_seed_bindings ORDER BY plugin_id,seed_id,agent_id`,
		`SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`,
		`SELECT * FROM actor_instances ORDER BY id`, `SELECT * FROM actor_schedules ORDER BY id`, `SELECT * FROM actor_instance_events ORDER BY id`,
		`SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`, `SELECT * FROM actor_known_skills ORDER BY agent_id,skill_name`,
		`SELECT * FROM sessions ORDER BY id`, `SELECT * FROM session_actor_bindings ORDER BY session_id,agent_id`,
	} {
		out[q] = retiredAPISnapshot(t, a, q)
	}
	return out
}
func assertLoomAuthorityUnchanged(t *testing.T, a *testAPI, before map[string][][]any) {
	t.Helper()
	if after := loomAuthoritySnapshot(t, a); !reflect.DeepEqual(before, after) {
		t.Fatal("Loom operation promoted historical profiles, provisioned authority, or changed retained schedules/adoption state")
	}
}

// The pre-boot setup contains retained historical rows only. It does not
// provision a fresh actor, instance, schedule, binding receipt, or grant.
func newTestAPIWithHistoricalLoom(t *testing.T) (*testAPI, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()
	for env, dir := range map[string]string{
		"HOME": filepath.Join(root, "home"), "XDG_DATA_HOME": filepath.Join(root, "xdg", "data"),
		"XDG_STATE_HOME": filepath.Join(root, "xdg", "state"), "XDG_CACHE_HOME": filepath.Join(root, "xdg", "cache"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg", "config"),
	} {
		t.Setenv(env, dir)
	}
	t.Setenv("TESSERACT_DB_PATH", filepath.Join(root, "tesseract", "main.db"))
	t.Setenv("TESSERACT_WORKSPACE", "private-loom-test")
	st, err := storetest.New(t, t.Context(), filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err = st.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"loom-curator", "loom-weaver"} {
		p := &store.AgentProfile{ID: "retained-" + slug, Slug: slug, Name: "Retained " + slug, SystemPrompt: "Private edited Loom instructions", Source: "internal"}
		if err = storetest.HistoricalProfile(t.Context(), st, p); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET class='process',durable=1,tags='["durable-agent","process"]' WHERE id=?`, p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO durable_agent_instances(id,name,slug,profile_id,lifecycle_class,status,metadata_json,urn) VALUES(?,?,?,?,'process','sleeping','{"private_retained":true}',?)`, "retained-instance-"+slug, p.Name, slug, p.ID, "msg://agent/retained/"+slug); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO agent_schedules(id,agent_id,name,schedule_kind,schedule_spec,body,status,next_run) VALUES(?,?,'Edited historical Loom schedule','cron','0 3 * * *','Private edited tool body','active','2020-01-01T00:00:00Z')`, "retained-schedule-"+slug, p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO agent_reflexes(id,agent_id,name,trigger_kind,trigger_spec,action_kind,action_spec,status,created_by,provenance_tier,fired_count) VALUES(?,?,'capture_on_discovery','predicate','{"kind":"tool_calls_window","window":1,"op":"=","value":0}','inject_reminder','{"body":"Private edited capture"}','paused','system','system',7)`, "retained-reflex-"+slug, p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO plugin_reflex_seed_bindings(plugin_id,seed_id,agent_id,reflex_id) VALUES('nanite.loom','deleted',?,NULL)`, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	historical := &testAPI{store: st}
	before := loomAuthoritySnapshot(t, historical)
	services, err := service.NewContainer(service.ContainerConfig{ModelCatalogOptions: modelsdevtest.Options(t), Store: st, Providers: provider.NewRegistry(), WorkingDir: root, DisableEmbeddedTesseract: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { services.Shutdown() })
	a := newAPIStoreFixture(services, st)
	assertLoomAuthorityUnchanged(t, a, before)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux
}

func TestImmutableLoomBootAndPublicCatalogDoNotProvisionHistoricalCurator(t *testing.T) {
	a, mux := newTestAPIWithHistoricalLoom(t)
	before := loomAuthoritySnapshot(t, a)
	for _, slug := range []string{"loom-curator", "loom-weaver"} {
		p, err := a.store.GetHistoricalAgentProfile(t.Context(), "retained-"+slug)
		if err != nil || p.SystemPrompt != "Private edited Loom instructions" {
			t.Fatalf("historical artifact=%+v,%v", p, err)
		}
		if inst, err := a.Services.DurableAgents.GetBySlug(t.Context(), slug); inst != nil || !errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
			t.Fatalf("historical instance became runtime=%+v,%v", inst, err)
		}
	}
	// Listing is a fresh catalog read, not an automatic Loom enrollment sweep.
	for _, path := range []string{"/api/durable-agents", "/api/agents"} {
		w := retiredAPIRequest(t, mux, http.MethodGet, path, "")
		var entries []json.RawMessage
		decodeErr := json.Unmarshal(w.Body.Bytes(), &entries)
		if w.Code != http.StatusOK || decodeErr != nil || len(entries) != 0 {
			t.Fatalf("historical catalog %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/durable-agents/retained-instance-loom-curator", http.StatusNotFound},
		{"/api/durable-agents/retained-instance-loom-curator/schedules", http.StatusBadRequest},
		{"/api/agents/retained-loom-curator", http.StatusNotFound},
	} {
		w := retiredAPIRequest(t, mux, http.MethodGet, tc.path, "")
		if w.Code != tc.status {
			t.Fatalf("historical runtime lookup %s=%d %s", tc.path, w.Code, w.Body.String())
		}
		if strings.HasSuffix(tc.path, "/schedules") && !strings.Contains(w.Body.String(), store.ErrDurableAgentInstanceNotFound.Error()) {
			t.Fatalf("historical schedules did not refuse missing fresh instance: %s", w.Body.String())
		}
	}
	requireRetiredAPI(t, retiredAPIRequest(t, mux, http.MethodPost, "/api/agents", `{"name":"Loom Curator","slug":"loom-curator","durable":true,"tags":"[\"durable-agent\"]"}`))
	assertLoomAuthorityUnchanged(t, a, before)
}

func TestImmutableLoomCreateRefusesAndHistoricalSchedulesStayOutsideWake(t *testing.T) {
	a, mux := newTestAPIWithHistoricalLoom(t)
	// Explicitly authored static host settings supply a catalog record, never
	// an actor. Reusing Loom's slug still cannot issue its durable instance.
	host := capabilityHostWithoutActor(t, a, "loom-curator", "user")
	before := loomAuthoritySnapshot(t, a)
	for _, id := range []string{host.ID, "retained-loom-curator", "msg://agent/claimed/loom-curator"} {
		inst := &store.DurableAgentInstance{Name: "Refused curator", Slug: "loom-curator", ProfileID: id, LifecycleClass: store.DurableAgentClassProcess}
		if err := a.Services.DurableAgents.Create(t.Context(), inst); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("create %q=%v", id, err)
		}
	}
	w := retiredAPIRequest(t, mux, http.MethodPost, "/api/durable-agents", `{"name":"Loom Curator","slug":"loom-curator","profile_id":"`+host.ID+`","lifecycle_class":"process","metadata_json":"{}"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), store.ErrVerifiedActorRequired.Error()) {
		t.Fatalf("host UUID durable creation=%d %s", w.Code, w.Body.String())
	}
	// Applicable JSON and slug validation remains at the HTTP boundary.
	for _, body := range []string{"not json", `{"slug":"INVALID SLUG","profile_id":"` + host.ID + `"}`} {
		w = retiredAPIRequest(t, mux, http.MethodPost, "/api/durable-agents", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("malformed create=%d %s", w.Code, w.Body.String())
		}
	}
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	due, err := a.Services.DurableWake.ListDue(t.Context(), now)
	if err != nil || len(due) != 0 {
		t.Fatalf("historical schedules became due=%+v,%v", due, err)
	}
	result, err := a.Services.DurableWake.RunDue(t.Context(), service.DurableAgentWakeRunRequest{Now: now})
	if err != nil || result == nil || len(result.Results) != 0 {
		t.Fatalf("historical schedule dispatch=%+v,%v", result, err)
	}
	assertLoomAuthorityUnchanged(t, a, before)
}
