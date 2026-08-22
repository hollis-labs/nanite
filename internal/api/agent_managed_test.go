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

// agentViewResp mirrors the AgentProfileView JSON the management endpoints
// return (embedded profile fields + management metadata).
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

// TestManagedAgentLifecycle is the Phase 1 acceptance test: a managed
// file-backed agent created through the API is fully editable in place — its
// profile, reflexes, and identity persist to .nanite/agents/<slug>.md AND the
// DB projection, are visible without a restart, are guarded by optimistic
// concurrency, and can be deleted (file + DB + FK children).
func TestManagedAgentLifecycle(t *testing.T) {
	a, mux := newTestAPI(t)

	// --- create ---
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
	if created.ManageClass != "managed" || !created.Editable {
		t.Fatalf("created agent not managed/editable: %+v", created)
	}
	if created.ID == "" || strings.HasPrefix(created.ID, "file-") {
		t.Fatalf("managed agent must have a real UUID id, got %q", created.ID)
	}
	if created.Revision == "" {
		t.Fatal("created agent missing revision token")
	}

	// File written under the managed root and stamped with the UUID.
	path := filepath.Join(a.Services.ManagedConfigRoot, "agents", "atlas-curator.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("managed file not written: %v", err)
	}
	if !strings.Contains(string(raw), "id: "+created.ID) {
		t.Fatalf("managed file missing stamped id:\n%s", raw)
	}

	// --- reflex edit persists for the managed file-backed agent ---
	w, body = mgReq(t, mux, "POST", "/api/agents/"+created.ID+"/reflexes", `{
		"name":"ground","trigger_kind":"predicate",
		"trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":2,\"op\":\"=\",\"value\":0}",
		"action_kind":"inject_reminder","action_spec":"{\"body\":\"ground\"}","priority":5
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create reflex on managed agent = %d body=%s", w.Code, body)
	}
	w, body = mgReq(t, mux, "GET", "/api/agents/"+created.ID+"/reflexes", "")
	if w.Code != http.StatusOK || !strings.Contains(string(body), "ground") {
		t.Fatalf("reflex did not persist: %d body=%s", w.Code, body)
	}

	// --- profile edit persists to file + DB without restart ---
	w, body = mgReq(t, mux, "PUT", "/api/agents/"+created.ID, `{
		"description":"Curates the atlas knowledge base","revision":"`+created.Revision+`"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d body=%s", w.Code, body)
	}
	var updated agentViewResp
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Description != "Curates the atlas knowledge base" {
		t.Fatalf("description not applied: %+v", updated)
	}
	if updated.Revision == created.Revision {
		t.Fatal("revision token should change after edit")
	}
	// DB projection reflects the edit immediately (no restart).
	if got, err := a.Services.Store.GetAgentBySlug(context.Background(), "atlas-curator"); err != nil || got.Description != "Curates the atlas knowledge base" {
		t.Fatalf("DB projection stale after edit: %+v err=%v", got, err)
	}
	// File reflects the edit.
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "Curates the atlas knowledge base") {
		t.Fatalf("file not updated:\n%s", raw)
	}

	// --- optimistic concurrency: stale revision is rejected ---
	w, body = mgReq(t, mux, "PUT", "/api/agents/"+created.ID, `{
		"description":"second writer","revision":"`+created.Revision+`"
	}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale-revision update = %d, want 409; body=%s", w.Code, body)
	}

	// --- delete removes file + DB row + FK children ---
	w, body = mgReq(t, mux, "DELETE", "/api/agents/"+created.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d body=%s", w.Code, body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("managed file still present after delete: %v", err)
	}
	if _, err := a.Services.Store.GetAgentBySlug(context.Background(), "atlas-curator"); err == nil {
		t.Fatal("DB row still present after delete")
	}
	reflexes, _ := a.Services.Store.ListAgentReflexesForAgent(context.Background(), created.ID, "")
	if len(reflexes) != 0 {
		t.Fatalf("reflex FK children not cleaned on delete: %d remain", len(reflexes))
	}
}

// storeAgent builds a minimal profile with an explicit provenance for tests
// that need a non-managed (read-only) source like "plugin".
func storeAgent(slug, name, source string) *store.AgentProfile {
	return &store.AgentProfile{
		Name:         name,
		Slug:         slug,
		SystemPrompt: "x",
		Source:       source,
	}
}

// TestManagedAgentUpdate_RejectsSlugTraversal is the HTTP-level regression
// test for GO-AGENT-001's "more severe than Create" rename-branch
// traversal: a crafted slug on PUT /api/agents/{id} must be rejected with a
// clean 400 before any filesystem write, must not write or overwrite
// anything outside the managed agents/ directory, and must leave the
// original managed file untouched.
func TestManagedAgentUpdate_RejectsSlugTraversal(t *testing.T) {
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
	originalPath := filepath.Join(a.Services.ManagedConfigRoot, "agents", "atlas-curator.md")

	// One level above agents/ — where the pre-fix code's raw
	// filepath.Join(filepath.Dir(existing.SourceRef), slug+".md") would have
	// landed for a "../evil" slug.
	escapePath := filepath.Join(a.Services.ManagedConfigRoot, "evil.md")

	for _, malicious := range []string{
		"../evil",
		"../../etc/evil",
		"a/b",
		"UPPER",
	} {
		payload, _ := json.Marshal(map[string]string{"slug": malicious, "revision": created.Revision})
		w, body = mgReq(t, mux, "PUT", "/api/agents/"+created.ID, string(payload))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("rename with slug %q = %d, want 400; body=%s", malicious, w.Code, body)
		}
		if !strings.Contains(string(body), `"validation_failed"`) {
			t.Fatalf("rename with slug %q: expected a validation_failed body, got %s", malicious, body)
		}
	}
	if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
		t.Fatalf("rejected rename wrote outside the managed agents/ directory: %v", err)
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("original managed file missing after rejected rename attempts: %v", err)
	}
}

// TestManagedAgentCreate_RejectsSlugTraversal is the create-path HTTP-level
// counterpart: agentvalidation.ValidateAgentConfig must reject an unsafe
// slug with a clean 400 before AgentConfigService.Create is ever called.
func TestManagedAgentCreate_RejectsSlugTraversal(t *testing.T) {
	a, mux := newTestAPI(t)
	w, body := mgReq(t, mux, "POST", "/api/agents", `{
		"name":"Evil","slug":"../evil","system_prompt":"x"
	}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with traversal slug = %d, want 400; body=%s", w.Code, body)
	}
	if !strings.Contains(string(body), `"validation_failed"`) {
		t.Fatalf("expected a validation_failed body, got %s", body)
	}
	// Pre-fix, ManagedAgentPath(configRoot, "../evil") would have joined to
	// filepath.Join(configRoot, "agents", "../evil.md") == configRoot/evil.md
	// — one level up from agents/, landing inside ManagedConfigRoot itself.
	if _, err := os.Stat(filepath.Join(a.Services.ManagedConfigRoot, "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("rejected create wrote outside the managed agents/ directory: %v", err)
	}
}

// TestCopyPluginAgentToManaged pins the make-editable path: a read-only
// plugin agent is rejected for in-place edits but can be forked into an
// editable managed copy with a fresh identity.
func TestCopyPluginAgentToManaged(t *testing.T) {
	a, mux := newTestAPI(t)
	// Seed a plugin (read-only) agent directly in the projection.
	plugin := storeAgent("giphy-helper", "Giphy Helper", "plugin")
	if err := a.Services.Store.CreateAgent(context.Background(), plugin); err != nil {
		t.Fatalf("seed plugin agent: %v", err)
	}

	// In-place edit is rejected with a copy affordance.
	w, body := mgReq(t, mux, "PUT", "/api/agents/"+plugin.ID, `{"description":"x"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("plugin edit = %d, want 409; body=%s", w.Code, body)
	}
	if !strings.Contains(string(body), `"copy_to_managed":true`) {
		t.Fatalf("plugin rejection should offer copy-to-managed: %s", body)
	}

	// Copy to managed mints a new editable agent.
	w, body = mgReq(t, mux, "POST", "/api/agents/"+plugin.ID+"/copy-to-managed", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("copy-to-managed = %d body=%s", w.Code, body)
	}
	var copied agentViewResp
	if err := json.Unmarshal(body, &copied); err != nil {
		t.Fatalf("decode copy: %v", err)
	}
	if copied.ManageClass != "managed" || !copied.Editable {
		t.Fatalf("copied agent not managed/editable: %+v", copied)
	}
	if copied.ID == plugin.ID {
		t.Fatal("copied agent must have a new identity")
	}
	if copied.Slug == plugin.Slug {
		t.Fatalf("copied agent must have a distinct slug, got %q", copied.Slug)
	}
	if _, err := os.Stat(filepath.Join(a.Services.ManagedConfigRoot, "agents", copied.Slug+".md")); err != nil {
		t.Fatalf("copied managed file not written: %v", err)
	}
	// The original read-only plugin agent must remain intact.
	if orig, err := a.Services.Store.GetAgent(context.Background(), plugin.ID); err != nil || orig.Source != "plugin" {
		t.Fatalf("original plugin agent was clobbered: %+v err=%v", orig, err)
	}
}

// TestManageableListExcludesInternal pins that the management list filter
// (?manageable=1) hides embedded internal harness primitives while the
// unfiltered list still surfaces them (chat picker, etc.).
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
