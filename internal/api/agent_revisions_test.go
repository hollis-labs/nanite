package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredAgentPublicMuxCannotEditOrRestoreHistoricalPromptAndAssignments(t *testing.T) {
	a, mux := newTestAPI(t)
	const id = "historical-revision-api"
	retiredAPIHistoricalProfile(t, a, id, "user")
	if _, err := a.store.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET source_ref='/provenance/only.md',protocol='acp',transport='stdio',can_execute=1,default_provider='retained-provider',system_prompt='Retained edited private prompt' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	profile, err := a.store.GetHistoricalAgentProfile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, body := range []string{"", "null", "[]", `{}`, `{"revision":"only-token"}`, `{"agent":{}}`, `{"name":null}`, `{"role_id":null}`, `{"revision":null}`, `{"Name":"case alias"}`, `{"unknown":true}`, `{"name":"a","name":"b"}`, `{"name":""}`, `{"slug":" "}`, `{"system_prompt":" "}`, `{"can_execute":"false"}`, `{"name":"a"} {}`, `{"name":"a"} trailing`, `{"settings":"null"}`, `{"role_tools":"{}"}`, `{"status":"unsupported"}`, `{"protocol":"made-up"}`, `{"description":"changed","can_execute":false}`, `{"role_id":"","protocol":"","transport":"","default_provider":"changed","runtime_kind":"api"}`} {
		requireRetiredAPI(t, retiredAPIRequest(t, mux, "PUT", "/api/agents/"+id, body))
	}
	for _, revision := range []string{profile.Revision, "missing-revision"} {
		for _, body := range []string{`{}`, `{"revision":"` + profile.Revision + `"}`, `{"revision":"stale"}`, "not json"} {
			requireRetiredAPI(t, retiredAPIRequest(t, mux, "POST", "/api/agents/"+id+"/revisions/"+revision+"/restore", body))
		}
	}
	requireRetiredAPI(t, retiredAPIRequest(t, mux, "DELETE", "/api/agents/"+id, ""))
	if w := retiredAPIRequest(t, mux, "GET", "/api/agents/"+id, ""); w.Code != http.StatusNotFound {
		t.Fatalf("ordinary runtime getter fell back to history: %d %s", w.Code, w.Body.String())
	}
	for i, q := range queries {
		retiredAPIHistoryUnchanged(t, a, q, before[i])
	}
}

func TestHistoricalAgentRevisionPublicMuxReadsPagesWithoutRestoringAuthority(t *testing.T) {
	a, mux := newTestAPI(t)
	const id = "historical-revision-pages"
	retiredAPIHistoricalProfile(t, a, id, "user")
	original, err := a.store.GetHistoricalAgentProfile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET system_prompt='Retained later prompt' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	current, err := a.store.GetHistoricalAgentProfile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_profile_revisions ORDER BY sequence`
	before := retiredAPISnapshot(t, a, query)
	base := "/api/agents/" + id + "/revisions"
	w := retiredAPIRequest(t, mux, "GET", base+"?limit=1&offset=1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("historical revision page: %d %s", w.Code, w.Body.String())
	}
	var page struct {
		Revisions     []store.AgentRevision `json:"revisions"`
		Scope         string                `json:"restore_scope"`
		Limit, Offset int
	}
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Revisions) != 1 || page.Revisions[0].ID != original.Revision || page.Revisions[0].Profile.SystemPrompt != original.SystemPrompt || page.Scope != "partial_profile_and_assignments" || page.Limit != 1 || page.Offset != 1 {
		t.Fatalf("historical page lost original snapshot: %+v", page)
	}
	w = retiredAPIRequest(t, mux, "GET", base+"?limit=1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), current.Revision) {
		t.Fatalf("latest historical revision: %d %s", w.Code, w.Body.String())
	}
	for _, suffix := range []string{"?limit=0", "?limit=101", "?offset=-1", "?limit=abc", "?offset=abc"} {
		w = retiredAPIRequest(t, mux, "GET", base+suffix, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("malformed historical page %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	w = retiredAPIRequest(t, mux, "GET", "/api/agents/missing/revisions", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing historical profile: %d %s", w.Code, w.Body.String())
	}
	requireRetiredAPI(t, retiredAPIRequest(t, mux, "POST", base+"/"+original.Revision+"/restore", `{"revision":"`+current.Revision+`"}`))
	retiredAPIHistoryUnchanged(t, a, query, before)
	retained, err := a.store.GetHistoricalAgentProfile(t.Context(), id)
	if err != nil || retained.Revision != current.Revision || retained.SystemPrompt != current.SystemPrompt {
		t.Fatalf("read-only historical page/restore changed profile: %+v %v", retained, err)
	}
}
