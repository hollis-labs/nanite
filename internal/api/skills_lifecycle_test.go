package api

// skills_lifecycle_test.go — TASKS/skills/12's own regression suite for
// the remaining skills REST surface: list/get-by-slug, real uninstall,
// grant/revoke, the grants/policy view, and invoke/preview. Reuses
// newSkillsTestAPI/postSkillJSON/decodeInstallResponse/skillFixture/
// writeMinimalSkillPackage (this package's own skills_install_test.go,
// TASKS/skills/05) rather than duplicating that harness.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
)

func createTestAgent(t *testing.T, a *API, slug string) *store.AgentProfile {
	t.Helper()
	ag := &store.AgentProfile{Name: "Test Agent " + slug, Slug: slug, SystemPrompt: "test"}
	if err := a.Services.Store.CreateAgent(ag); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return ag
}

func getJSON(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func deleteJSON(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func decodeGrantView(t *testing.T, w *httptest.ResponseRecorder) AgentSkillGrantView {
	t.Helper()
	var v AgentSkillGrantView
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("decode grant view: %v; body: %s", err, w.Body.String())
	}
	return v
}

// writeSkillPackageV2 overwrites dir's SKILL.md with content that hashes
// differently from writeMinimalSkillPackage's own fixed body — used to
// exercise a real re-sync/re-approval-required transition without
// depending on that shared helper's fixed content.
func writeSkillPackageV2(t *testing.T, dir, slug, name string) {
	t.Helper()
	content := "---\nname: " + name + "\nslug: " + slug + "\ndescription: v2 -- genuinely different content.\ncontext: inline\n---\n\nMinimal body v2.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write v2 SKILL.md: %v", err)
	}
}

// --- item 1: GET /api/skills/{slug} (list/get) ---

func TestHandleGetSkill_BySlugAndByID(t *testing.T) {
	_, mux := newSkillsTestAPI(t)

	installed := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	}))

	w := getJSON(t, mux, "/api/skills/"+installed.Skill.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("GET by slug: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var bySlug store.Skill
	if err := json.NewDecoder(w.Body).Decode(&bySlug); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bySlug.ID != installed.Skill.ID {
		t.Errorf("GET by slug: ID = %q, want %q", bySlug.ID, installed.Skill.ID)
	}
	if bySlug.ContentHash != installed.Address {
		t.Errorf("GET by slug: ContentHash = %q, want %q", bySlug.ContentHash, installed.Address)
	}

	// Bare index-row ID still resolves (task 02's admin-CRUD surface's own
	// addressing) even though the route's addressing convention is now
	// slug-primary.
	w2 := getJSON(t, mux, "/api/skills/"+installed.Skill.ID)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET by ID: expected 200, got %d; body: %s", w2.Code, w2.Body.String())
	}

	w3 := getJSON(t, mux, "/api/skills/does-not-exist")
	if w3.Code != http.StatusNotFound {
		t.Fatalf("GET unknown ref: expected 404, got %d", w3.Code)
	}
}

// --- item 5: DELETE /api/skills/{slug} (real uninstall) ---

func TestHandleDeleteSkill_RealUninstall(t *testing.T) {
	a, mux := newSkillsTestAPI(t)

	installed := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	}))

	w := deleteJSON(t, mux, "/api/skills/"+installed.Skill.Slug)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if vd, _ := resp["vendor_deleted"].(bool); !vd {
		t.Error("expected vendor_deleted = true for a fully installed skill")
	}

	got, err := a.Services.Store.GetSkillBySlug(installed.Skill.Slug)
	if err != nil {
		t.Fatalf("GetSkillBySlug: %v", err)
	}
	if got != nil {
		t.Error("expected the index row to be removed")
	}
	if _, err := a.Services.SkillVendor.Path(installed.Address); err == nil {
		t.Error("expected the vendored copy to be removed")
	}

	// Deleting an already-uninstalled skill -> 404, not a panic/500.
	w2 := deleteJSON(t, mux, "/api/skills/"+installed.Skill.Slug)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("DELETE again: expected 404, got %d; body: %s", w2.Code, w2.Body.String())
	}
}

// TestSkillDelete_RESTAndSelfTool_SameEndState is this task's own
// Done-means requirement: "skill_delete (self-tool) and DELETE
// /api/skills/{slug} (REST) both call the same uninstall logic — a test
// confirms deleting via either path leaves the system in the same state."
// Two independently-installed skills are deleted one via each path, then
// both are asserted to land in the identical end state (index row gone,
// vendored copy gone) — proving the shared internal/skillinstall.
// Uninstaller implementation behaves identically regardless of caller.
func TestSkillDelete_RESTAndSelfTool_SameEndState(t *testing.T) {
	a, mux := newSkillsTestAPI(t)

	restInstalled := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	}))

	toolPkg := t.TempDir()
	writeMinimalSkillPackage(t, toolPkg, "self-tool-delete-target", "Self Tool Delete Target")
	toolInstalled := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": toolPkg,
	}))

	if w := deleteJSON(t, mux, "/api/skills/"+restInstalled.Skill.Slug); w.Code != http.StatusOK {
		t.Fatalf("REST delete: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	transport := &selftools.SelfToolsTransport{Store: a.Services.Store, SkillVendor: a.Services.SkillVendor}
	toolResult, err := transport.CallTool(context.Background(), "skill_delete", map[string]any{"slug": toolInstalled.Skill.Slug})
	if err != nil {
		t.Fatalf("CallTool(skill_delete): %v", err)
	}
	if toolResult.IsError {
		t.Fatalf("skill_delete self-tool returned an error: %s", toolResult.Content[0].Text)
	}

	for _, tc := range []struct {
		label   string
		slug    string
		address string
	}{
		{"REST", restInstalled.Skill.Slug, restInstalled.Address},
		{"self-tool", toolInstalled.Skill.Slug, toolInstalled.Address},
	} {
		got, err := a.Services.Store.GetSkillBySlug(tc.slug)
		if err != nil {
			t.Fatalf("%s: GetSkillBySlug: %v", tc.label, err)
		}
		if got != nil {
			t.Errorf("%s: expected the index row to be removed", tc.label)
		}
		if _, err := a.Services.SkillVendor.Path(tc.address); err == nil {
			t.Errorf("%s: expected the vendored copy to be removed", tc.label)
		}
	}
}

// --- items 2/3: grant/revoke + grants/policy view ---

func TestAgentSkillGrant_FullLifecycle(t *testing.T) {
	a, mux := newSkillsTestAPI(t)

	pkg := t.TempDir()
	writeMinimalSkillPackage(t, pkg, "grant-lifecycle-skill", "Grant Lifecycle Skill")
	installed := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{"path": pkg}))

	agent := createTestAgent(t, a, "grant-lifecycle-agent")
	grantPath := fmt.Sprintf("/api/agents/%s/skills/%s/grant", agent.ID, installed.Skill.Slug)

	// 1. Before any grant -> grant_required.
	before := decodeGrantView(t, getJSON(t, mux, grantPath))
	if before.Status != "grant_required" {
		t.Fatalf("expected grant_required before any grant, got %q (%s)", before.Status, before.Message)
	}

	// granted_by is required.
	if w := postSkillJSON(t, mux, grantPath, map[string]any{}); w.Code != http.StatusBadRequest {
		t.Fatalf("grant without granted_by: expected 400, got %d", w.Code)
	}

	// 2. Grant.
	w := postSkillJSON(t, mux, grantPath, map[string]any{
		"granted_by": "test-operator",
		"capabilities": map[string]any{
			"network": map[string]any{"allow": true},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST grant: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	// 3. View -> approved, with the capability round-tripped.
	after := decodeGrantView(t, getJSON(t, mux, grantPath))
	if after.Status != "approved" {
		t.Fatalf("expected approved after grant, got %q (%s)", after.Status, after.Message)
	}
	if after.ApprovedContentHash != installed.Skill.ContentHash {
		t.Errorf("ApprovedContentHash = %q, want %q", after.ApprovedContentHash, installed.Skill.ContentHash)
	}
	if after.GrantedBy != "test-operator" {
		t.Errorf("GrantedBy = %q, want %q", after.GrantedBy, "test-operator")
	}
	if after.Capabilities == nil || after.Capabilities.Network == nil || !after.Capabilities.Network.Allow {
		t.Errorf("expected the granted network capability to round-trip, got %+v", after.Capabilities)
	}

	// list shows the granted skill with correct provenance.
	agentSkills, err := a.Services.Store.ListAgentSkills(agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	found := false
	for _, sk := range agentSkills {
		if sk.Slug == installed.Skill.Slug {
			found = true
		}
	}
	if !found {
		t.Error("expected the granted skill to appear in ListAgentSkills")
	}

	// 4. Content changes via re-sync -> reapproval_required.
	writeSkillPackageV2(t, pkg, "grant-lifecycle-skill", "Grant Lifecycle Skill")
	syncW := postSkillJSON(t, mux, "/api/skills/"+installed.Skill.Slug+"/sync", map[string]string{"path": pkg})
	if syncW.Code != http.StatusOK {
		t.Fatalf("sync: expected 200, got %d; body: %s", syncW.Code, syncW.Body.String())
	}

	stale := decodeGrantView(t, getJSON(t, mux, grantPath))
	if stale.Status != "reapproval_required" {
		t.Fatalf("expected reapproval_required after re-sync, got %q (%s)", stale.Status, stale.Message)
	}
	if stale.ApprovedContentHash == stale.CurrentContentHash {
		t.Error("expected ApprovedContentHash and CurrentContentHash to differ after re-sync")
	}

	// Preview must also refuse with the same re-approval-required state.
	previewW := postSkillJSON(t, mux, "/api/skills/"+installed.Skill.Slug+"/preview", map[string]any{"agent_id": agent.ID})
	if previewW.Code != http.StatusConflict {
		t.Fatalf("preview with stale approval: expected 409, got %d; body: %s", previewW.Code, previewW.Body.String())
	}

	// 5. Revoke.
	revokeW := deleteJSON(t, mux, grantPath)
	if revokeW.Code != http.StatusOK {
		t.Fatalf("DELETE grant: expected 200, got %d; body: %s", revokeW.Code, revokeW.Body.String())
	}
	revoked := decodeGrantView(t, getJSON(t, mux, grantPath))
	if revoked.Status != "grant_required" {
		t.Fatalf("expected grant_required after revoke, got %q", revoked.Status)
	}

	// Revoking again -> 404.
	if w := deleteJSON(t, mux, grantPath); w.Code != http.StatusNotFound {
		t.Fatalf("DELETE grant again: expected 404, got %d", w.Code)
	}
}

func TestHandleGrantAgentSkill_RejectsUnvendoredSkill(t *testing.T) {
	a, mux := newSkillsTestAPI(t)

	// A bare admin-CRUD row (task 02) with no ContentHash -- never
	// installed/vendored.
	sk := &store.Skill{Name: "Bare Row", Slug: "bare-row-grant-test", Description: "no content"}
	if err := a.Services.Store.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	agent := createTestAgent(t, a, "bare-row-grant-agent")

	w := postSkillJSON(t, mux, fmt.Sprintf("/api/agents/%s/skills/%s/grant", agent.ID, sk.Slug),
		map[string]any{"granted_by": "test-operator"})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("grant against an unvendored skill: expected 422, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- item 4: POST /api/skills/{slug}/preview ---

func TestHandlePreviewSkill_RequiresGrant(t *testing.T) {
	a, mux := newSkillsTestAPI(t)

	pkg := t.TempDir()
	writeMinimalSkillPackage(t, pkg, "preview-lifecycle-skill", "Preview Lifecycle Skill")
	installed := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{"path": pkg}))
	agent := createTestAgent(t, a, "preview-lifecycle-agent")
	previewPath := "/api/skills/" + installed.Skill.Slug + "/preview"

	// agent_id is required.
	if w := postSkillJSON(t, mux, previewPath, map[string]any{}); w.Code != http.StatusBadRequest {
		t.Fatalf("preview without agent_id: expected 400, got %d", w.Code)
	}

	// Before any grant -> 403, matching skill_get's own denial for an
	// ungranted agent (this endpoint deliberately does not bypass the
	// grant check -- see handlePreviewSkill's own doc comment).
	w := postSkillJSON(t, mux, previewPath, map[string]any{"agent_id": agent.ID})
	if w.Code != http.StatusForbidden {
		t.Fatalf("preview before grant: expected 403, got %d; body: %s", w.Code, w.Body.String())
	}

	// Grant, then preview succeeds and returns real materialized content.
	grantPath := fmt.Sprintf("/api/agents/%s/skills/%s/grant", agent.ID, installed.Skill.Slug)
	if w := postSkillJSON(t, mux, grantPath, map[string]any{"granted_by": "test-operator"}); w.Code != http.StatusCreated {
		t.Fatalf("grant: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	w2 := postSkillJSON(t, mux, previewPath, map[string]any{"agent_id": agent.ID})
	if w2.Code != http.StatusOK {
		t.Fatalf("preview after grant: expected 200, got %d; body: %s", w2.Code, w2.Body.String())
	}
	var resp SkillPreviewResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if resp.Slug != installed.Skill.Slug {
		t.Errorf("Slug = %q, want %q", resp.Slug, installed.Skill.Slug)
	}
	if !strings.Contains(resp.Content, "Minimal body.") {
		t.Errorf("expected the skill's materialized body in the preview content, got: %q", resp.Content)
	}
}

func TestHandlePreviewSkill_UnknownSlug(t *testing.T) {
	_, mux := newSkillsTestAPI(t)
	w := postSkillJSON(t, mux, "/api/skills/does-not-exist/preview", map[string]any{"agent_id": "whoever"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("preview unknown slug: expected 404, got %d", w.Code)
	}
}
