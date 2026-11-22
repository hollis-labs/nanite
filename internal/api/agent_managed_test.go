package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

type agentViewResp struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Description   string `json:"description"`
	Source        string `json:"source"`
	SourceRef     string `json:"source_ref"`
	ManageClass   string `json:"manage_class"`
	Editable      bool   `json:"editable"`
	CopyToManaged bool   `json:"copy_to_managed"`
	Revision      string `json:"revision"`
}

func mgReq(t *testing.T, mux http.Handler, method, path, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w, w.Body.Bytes()
}

func storeAgent(slug, name, source string) *store.AgentProfile {
	return &store.AgentProfile{Name: name, Slug: slug, SystemPrompt: "x", Source: source}
}

func TestRetiredManagedAgentLifecycleHasNoDatabaseOrFileEffects(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "user")
	before := retiredAgentState(t, a)
	for _, request := range []struct{ method, path, body string }{
		{"POST", "/api/agents", `{"name":"Atlas Curator","slug":"atlas-curator","system_prompt":"You curate."}`},
		{"PUT", "/api/agents/" + p.ID, `{"description":"changed","revision":"claimed"}`},
		{"POST", "/api/agents/" + p.ID + "/reflexes", `{"name":"ground","action_kind":"inject_reminder"}`},
		{"DELETE", "/api/agents/" + p.ID, ""},
	} {
		retiredAgentRequest(t, mux, request.method, request.path, request.body)
		assertRetiredAgentState(t, a, before)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "agents")); !os.IsNotExist(err) {
		t.Fatalf("retired lifecycle produced projection: %v", err)
	}
}

func TestRetiredManagedAgentUnsafeSlugsRemainWithoutEffects(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "user")
	before := retiredAgentState(t, a)
	for _, slug := range []string{"../evil", "../../etc/evil", "a/b", "UPPER"} {
		payload, err := json.Marshal(map[string]string{"slug": slug})
		if err != nil {
			t.Fatal(err)
		}
		retiredAgentRequest(t, mux, "POST", "/api/agents", string(payload))
		retiredAgentRequest(t, mux, "PUT", "/api/agents/"+p.ID, string(payload))
		assertRetiredAgentState(t, a, before)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "agents")); !os.IsNotExist(err) {
		t.Fatalf("unsafe request produced projection: %v", err)
	}
}

func TestRetiredCopyToManagedDoesNotCreateOrConvertAuthority(t *testing.T) {
	for _, source := range []string{"plugin", "internal", "user", "import"} {
		t.Run(source, func(t *testing.T) {
			a, mux := newTestAPI(t)
			p := retiredAgentHistory(t, a, source)
			before := retiredAgentState(t, a)
			retiredAgentRequest(t, mux, "POST", "/api/agents/"+p.ID+"/copy-to-managed", "")
			assertRetiredAgentState(t, a, before)
		})
	}
}

func TestManageableListExcludesHistoricalProfiles(t *testing.T) {
	a, mux := newTestAPI(t)
	for _, source := range []string{"internal", "user", "plugin", "import"} {
		retiredAgentHistory(t, a, source)
	}
	before := retiredAgentState(t, a)
	w, body := mgReq(t, mux, "GET", "/api/agents?manageable=1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, body)
	}
	var views []agentViewResp
	if err := json.Unmarshal(body, &views); err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if strings.HasPrefix(v.Slug, "retained-") {
			t.Fatalf("runtime list exposed historical profile %s", v.Slug)
		}
	}
	assertRetiredAgentState(t, a, before)
}
