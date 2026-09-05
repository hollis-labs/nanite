package api

import (
	"bytes"
	"context"
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

func TestManagedAgentLifecycleIsDatabaseOnly(t *testing.T) {
	a, mux := newTestAPI(t)
	w, body := mgReq(t, mux, "POST", "/api/agents", `{
		"name":"Atlas Curator","slug":"atlas-curator","system_prompt":"You curate."
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", w.Code, body)
	}
	var created agentViewResp
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ManageClass != "managed" || !created.Editable || created.ID == "" {
		t.Fatalf("created agent = %+v", created)
	}
	if created.SourceRef != "" || created.Revision != "" {
		t.Fatalf("created agent retained filesystem authority: %+v", created)
	}
	projectionDir := filepath.Join(a.Services.WorkingDir, ".nanite", "agents")
	if _, err := os.Stat(projectionDir); !os.IsNotExist(err) {
		t.Fatalf("create produced .nanite/agents projection: %v", err)
	}

	w, body = mgReq(t, mux, "POST", "/api/agents/"+created.ID+"/reflexes", `{
		"name":"ground","trigger_kind":"predicate",
		"trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":2,\"op\":\"=\",\"value\":0}",
		"action_kind":"inject_reminder","action_spec":"{\"body\":\"ground\"}","priority":5
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create reflex = %d body=%s", w.Code, body)
	}

	w, body = mgReq(t, mux, "PUT", "/api/agents/"+created.ID, `{
		"description":"Curates the atlas knowledge base","revision":"obsolete"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d body=%s", w.Code, body)
	}
	var updated agentViewResp
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Description != "Curates the atlas knowledge base" || updated.Revision != "" || updated.SourceRef != "" {
		t.Fatalf("updated agent = %+v", updated)
	}
	if got, err := a.Services.Store.GetAgentBySlug(context.Background(), "atlas-curator"); err != nil || got.Description != updated.Description {
		t.Fatalf("database stale after edit: %+v, %v", got, err)
	}
	if _, err := os.Stat(projectionDir); !os.IsNotExist(err) {
		t.Fatalf("update produced .nanite/agents projection: %v", err)
	}

	w, body = mgReq(t, mux, "DELETE", "/api/agents/"+created.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d body=%s", w.Code, body)
	}
	if _, err := a.Services.Store.GetAgentBySlug(context.Background(), "atlas-curator"); err == nil {
		t.Fatal("database row remained after delete")
	}
	reflexes, _ := a.Services.Store.ListAgentReflexesForAgent(context.Background(), created.ID, "")
	if len(reflexes) != 0 {
		t.Fatalf("reflex FK children remained after delete: %d", len(reflexes))
	}
}

func storeAgent(slug, name, source string) *store.AgentProfile {
	return &store.AgentProfile{Name: name, Slug: slug, SystemPrompt: "x", Source: source}
}

func TestManagedAgentEndpointsRejectUnsafeSlugsWithoutWritingFiles(t *testing.T) {
	a, mux := newTestAPI(t)
	w, body := mgReq(t, mux, "POST", "/api/agents", `{
		"name":"Atlas Curator","slug":"atlas-curator","system_prompt":"You curate."
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", w.Code, body)
	}
	var created agentViewResp
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	for _, malicious := range []string{"../evil", "../../etc/evil", "a/b", "UPPER"} {
		payload, _ := json.Marshal(map[string]string{"slug": malicious})
		w, body = mgReq(t, mux, "PUT", "/api/agents/"+created.ID, string(payload))
		if w.Code != http.StatusBadRequest || !strings.Contains(string(body), `"validation_failed"`) {
			t.Fatalf("rename with slug %q = %d body=%s", malicious, w.Code, body)
		}
	}
	w, body = mgReq(t, mux, "POST", "/api/agents", `{"name":"Evil","slug":"../evil","system_prompt":"x"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(string(body), `"validation_failed"`) {
		t.Fatalf("unsafe create = %d body=%s", w.Code, body)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "agents")); !os.IsNotExist(err) {
		t.Fatalf("unsafe requests produced projection files: %v", err)
	}
}

func TestCopyPluginAgentToManagedIsDatabaseOnly(t *testing.T) {
	a, mux := newTestAPI(t)
	plugin := storeAgent("giphy-helper", "Giphy Helper", "plugin")
	plugin.SourceRef = "/path/that/must/not/be/read/giphy-helper.md"
	if err := a.Services.Store.CreateAgent(context.Background(), plugin); err != nil {
		t.Fatalf("seed plugin agent: %v", err)
	}
	w, body := mgReq(t, mux, "PUT", "/api/agents/"+plugin.ID, `{"description":"x"}`)
	if w.Code != http.StatusConflict || !strings.Contains(string(body), `"copy_to_managed":true`) {
		t.Fatalf("plugin edit = %d body=%s", w.Code, body)
	}
	w, body = mgReq(t, mux, "POST", "/api/agents/"+plugin.ID+"/copy-to-managed", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("copy-to-managed = %d body=%s", w.Code, body)
	}
	var copied agentViewResp
	if err := json.Unmarshal(body, &copied); err != nil {
		t.Fatalf("decode copy: %v", err)
	}
	if copied.ID == plugin.ID || copied.SourceRef != "" || !copied.Editable {
		t.Fatalf("copy = %+v", copied)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "agents")); !os.IsNotExist(err) {
		t.Fatalf("copy produced .nanite/agents projection: %v", err)
	}
}

func TestCopyInternalAgentToManagedIsRejected(t *testing.T) {
	a, mux := newTestAPI(t)
	internal := storeAgent("internal-copy-guard", "Internal Copy Guard", "internal")
	internal.SourceRef = "/plugin-looking/path/that-must-not-change-ownership.md"
	if err := a.Services.Store.CreateAgent(context.Background(), internal); err != nil {
		t.Fatalf("seed internal agent: %v", err)
	}
	w, body := mgReq(t, mux, "POST", "/api/agents/"+internal.ID+"/copy-to-managed", "")
	if w.Code != http.StatusConflict || !strings.Contains(string(body), `"manage_class":"internal"`) ||
		!strings.Contains(string(body), `"copy_to_managed":false`) {
		t.Fatalf("internal copy-to-managed = %d body=%s", w.Code, body)
	}
	if _, err := a.Services.Store.GetAgentBySlug(context.Background(), "internal-copy-guard-copy"); err == nil {
		t.Fatal("endpoint created a managed copy of an internal profile")
	}
}

func TestManageableListExcludesInternal(t *testing.T) {
	_, mux := newTestAPI(t)
	w, body := mgReq(t, mux, "GET", "/api/agents?manageable=1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var views []agentViewResp
	if err := json.Unmarshal(body, &views); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, v := range views {
		if v.ManageClass == "internal" {
			t.Fatalf("manageable list leaked internal agent %q", v.Slug)
		}
	}
}
