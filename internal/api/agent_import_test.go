package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

const nativeAgentDef = `---
name: Imported Reviewer
slug: imported-reviewer
description: Came from outside
---
Imported system prompt.
`

const claudeAgentDef = `---
name: code-reviewer
description: Expert code review specialist
tools: Read, Grep, Bash
model: sonnet
---
You are a code reviewer.
`

func writeAgentSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func decodeImportResponse(t *testing.T, body []byte) ImportAgentResponse {
	t.Helper()
	var out ImportAgentResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode import response: %v; body: %s", err, body)
	}
	return out
}

func postAgentImport(t *testing.T, mux http.Handler, path string, body map[string]string) (*http.Response, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w, out := mgReq(t, mux, "POST", path, string(raw))
	return w.Result(), out
}

// TestAgentImportInstallLandsExternalAndReadOnly — the endpoint exists, it
// writes a real row, and the view the client gets back says immediately that
// what it just imported is read-only with a copy path. That last part is the
// point: a client should not have to do a second GET to learn it.
func TestAgentImportInstallLandsExternalAndReadOnly(t *testing.T) {
	_, mux := newTestAPI(t)
	src := writeAgentSource(t, t.TempDir(), "imported-reviewer.md", nativeAgentDef)

	resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": src})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("install = %d, want 201; body: %s", resp.StatusCode, body)
	}
	out := decodeImportResponse(t, body)
	if out.Created != 1 || out.Skipped != 0 || len(out.Outcomes) != 1 {
		t.Fatalf("response = %+v", out)
	}
	o := out.Outcomes[0]
	if o.Action != "created" || o.Slug != "imported-reviewer" {
		t.Fatalf("outcome = %+v", o)
	}
	if o.Agent == nil {
		t.Fatal("created outcome carries no agent view")
	}
	if o.Agent.ManageClass != "external" {
		t.Errorf("manage_class = %q, want external", o.Agent.ManageClass)
	}
	if o.Agent.Editable {
		t.Error("an imported agent must not be editable in place")
	}
	if !o.Agent.CopyToManaged {
		t.Error("an imported agent must offer the copy path, or the boundary is a dead end")
	}
}

// TestAgentImportInstallRefusesSlugItDoesNotOwn — a slug held by another
// class is a 409 naming that class, and the incumbent is untouched.
func TestAgentImportInstallRefusesSlugItDoesNotOwn(t *testing.T) {
	a, mux := newTestAPI(t)
	if err := a.Services.Store.CreateAgent(t.Context(), &store.AgentProfile{
		Name: "Incumbent", Slug: "imported-reviewer", SystemPrompt: "incumbent", Source: "internal",
	}); err != nil {
		t.Fatalf("seed incumbent: %v", err)
	}
	src := writeAgentSource(t, t.TempDir(), "imported-reviewer.md", nativeAgentDef)

	resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": src})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("install over an incumbent = %d, want 409; body: %s", resp.StatusCode, body)
	}
	out := decodeImportResponse(t, body)
	if out.Skipped != 1 || out.Created != 0 {
		t.Fatalf("response = %+v", out)
	}
	o := out.Outcomes[0]
	if o.BlockedBy != "internal" {
		t.Errorf("blocked_by = %q, want internal", o.BlockedBy)
	}
	if o.CopyToManaged {
		t.Error("an internal profile offers no copy path; saying it does would be worse than saying nothing")
	}

	row, err := a.Services.Store.GetAgentBySlug(t.Context(), "imported-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if row.SystemPrompt != "incumbent" || row.Source != "internal" {
		t.Errorf("incumbent changed: %+v", row)
	}
}

// TestAgentImportSyncRoundTrip — install, edit the source, sync, and the row
// carries the new content under the same identity.
func TestAgentImportSyncRoundTrip(t *testing.T) {
	a, mux := newTestAPI(t)
	dir := t.TempDir()
	src := writeAgentSource(t, dir, "imported-reviewer.md", nativeAgentDef)

	if resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": src}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("install = %d; body: %s", resp.StatusCode, body)
	}
	first, err := a.Services.Store.GetAgentBySlug(t.Context(), "imported-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}

	writeAgentSource(t, dir, "imported-reviewer.md",
		"---\nname: Imported Reviewer\nslug: imported-reviewer\n---\nEdited system prompt.\n")

	resp, body := postAgentImport(t, mux, "/api/agents/imported-reviewer/sync", map[string]string{"path": src})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync = %d, want 200; body: %s", resp.StatusCode, body)
	}
	out := decodeImportResponse(t, body)
	if out.Synced != 1 {
		t.Fatalf("response = %+v", out)
	}

	second, err := a.Services.Store.GetAgentBySlug(t.Context(), "imported-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("sync minted a new identity: %q -> %q", first.ID, second.ID)
	}
	if second.SystemPrompt != "Edited system prompt." {
		t.Errorf("SystemPrompt = %q, want the re-read content", second.SystemPrompt)
	}
}

// TestAgentImportSyncRefusesNonImportedThroughWriteNotManaged is the check
// CW-20260910-0013 asks for by name: the external class must route through the
// EXISTING writeNotManaged rather than a new branch. Asserting the exact
// response shape that helper produces — error code, manage_class,
// copy_to_managed, source_ref, slug — is what makes that a verification
// rather than a claim.
func TestAgentImportSyncRefusesNonImportedThroughWriteNotManaged(t *testing.T) {
	for _, tc := range []struct {
		source        string
		wantClass     string
		wantCopyPath  bool
		wantSubstring string
	}{
		{"internal", "internal", false, "managed by Nanite"},
		{"user", "managed", false, "not a writable managed config"},
		{"plugin", "plugin", true, "copy it to the managed layer to edit"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			a, mux := newTestAPI(t)
			slug := "incumbent-" + tc.source
			if err := a.Services.Store.CreateAgent(t.Context(), &store.AgentProfile{
				Name: "Incumbent", Slug: slug, SystemPrompt: "x",
				Source: tc.source, SourceRef: "/provenance/only.md",
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			src := writeAgentSource(t, t.TempDir(), slug+".md", nativeAgentDef)

			resp, body := postAgentImport(t, mux, "/api/agents/"+slug+"/sync", map[string]string{"path": src})
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("sync = %d, want 409; body: %s", resp.StatusCode, body)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			// The exact writeNotManaged envelope.
			if got["error"] != "agent_not_managed" {
				t.Errorf("error = %v, want agent_not_managed (writeNotManaged's own code)", got["error"])
			}
			if got["manage_class"] != tc.wantClass {
				t.Errorf("manage_class = %v, want %q", got["manage_class"], tc.wantClass)
			}
			if got["copy_to_managed"] != tc.wantCopyPath {
				t.Errorf("copy_to_managed = %v, want %v", got["copy_to_managed"], tc.wantCopyPath)
			}
			if got["slug"] != slug {
				t.Errorf("slug = %v, want %q", got["slug"], slug)
			}
			if got["source_ref"] != "/provenance/only.md" {
				t.Errorf("source_ref = %v — provenance is reported, never dereferenced", got["source_ref"])
			}
			msg, _ := got["message"].(string)
			if !strings.Contains(msg, tc.wantSubstring) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.wantSubstring)
			}
		})
	}
}

// TestAgentImportSyncGuards — the two pre-flight refusals, both before
// anything is written.
func TestAgentImportSyncGuards(t *testing.T) {
	a, mux := newTestAPI(t)
	src := writeAgentSource(t, t.TempDir(), "imported-reviewer.md", nativeAgentDef)

	// Unknown slug: a clear 404, not a silent create.
	resp, body := postAgentImport(t, mux, "/api/agents/never-imported/sync", map[string]string{"path": src})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("sync unknown slug = %d, want 404; body: %s", resp.StatusCode, body)
	}
	if _, err := a.Services.Store.GetAgentBySlug(t.Context(), "never-imported"); err == nil {
		t.Error("a refused sync created the agent anyway")
	}

	// Mismatched source: the target exists and is imported, but the source
	// declares a different slug. Nothing may be written under either.
	if resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": src}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("install = %d; body: %s", resp.StatusCode, body)
	}
	other := writeAgentSource(t, t.TempDir(), "other.md",
		"---\nname: Other\nslug: other-agent\n---\nSomething else.\n")
	mismatch, mismatchBody := postAgentImport(t, mux, "/api/agents/imported-reviewer/sync", map[string]string{"path": other})
	if mismatch.StatusCode != http.StatusConflict {
		t.Fatalf("mismatched sync = %d, want 409; body: %s", mismatch.StatusCode, mismatchBody)
	}
	if _, err := a.Services.Store.GetAgentBySlug(t.Context(), "other-agent"); err == nil {
		t.Error("a rejected sync target still wrote the mismatched definition")
	}
	row, _ := a.Services.Store.GetAgentBySlug(t.Context(), "imported-reviewer")
	if row.SystemPrompt != "Imported system prompt." {
		t.Errorf("the sync target changed despite the refusal: %q", row.SystemPrompt)
	}
}

// TestAgentImportMalformedSourceIs422 — a source that is nobody's format is
// the caller's problem, reported clearly, never a panic or an opaque 500.
func TestAgentImportMalformedSourceIs422(t *testing.T) {
	_, mux := newTestAPI(t)
	src := writeAgentSource(t, t.TempDir(), "notes.md", "# Just prose, no frontmatter\n")

	resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": src})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("install malformed = %d, want 422; body: %s", resp.StatusCode, body)
	}
}

func TestAgentImportRequiresPath(t *testing.T) {
	_, mux := newTestAPI(t)
	resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("install with no path = %d, want 400; body: %s", resp.StatusCode, body)
	}
}

// TestAgentImportThroughTheClaudeAdapter — the REST surface reaches the
// adapter seam too, not only the CLI. A project root (equally, a planted boot
// directory) imports every subagent under it.
func TestAgentImportThroughTheClaudeAdapter(t *testing.T) {
	a, mux := newTestAPI(t)
	root := t.TempDir()
	writeAgentSource(t, filepath.Join(root, ".claude", "agents"), "code-reviewer.md", claudeAgentDef)

	resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": root})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("install = %d, want 201; body: %s", resp.StatusCode, body)
	}
	out := decodeImportResponse(t, body)
	if out.Created != 1 || out.Outcomes[0].Slug != "code-reviewer" {
		t.Fatalf("response = %+v", out)
	}

	row, err := a.Services.Store.GetAgentBySlug(t.Context(), "code-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if row.OriginSystem != "claude" {
		t.Errorf("origin_system = %q, want claude", row.OriginSystem)
	}
	if row.Source != "import" {
		t.Errorf("source = %q, want import — an adapter never declares an imported agent operator-owned", row.Source)
	}
	if row.DefaultModel != "" {
		t.Errorf("default_model = %q, want blank", row.DefaultModel)
	}
}

// TestUpdateAnImportedAgentIsRefused closes the loop the endpoints open: the
// existing PUT surface must refuse the newly-reachable external class, with
// the copy path offered.
func TestUpdateAnImportedAgentIsRefused(t *testing.T) {
	a, mux := newTestAPI(t)
	src := writeAgentSource(t, t.TempDir(), "imported-reviewer.md", nativeAgentDef)
	if resp, body := postAgentImport(t, mux, "/api/agents/install", map[string]string{"path": src}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("install = %d; body: %s", resp.StatusCode, body)
	}
	row, err := a.Services.Store.GetAgentBySlug(t.Context(), "imported-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}

	w, body := mgReq(t, mux, "PUT", "/api/agents/"+row.ID, `{"description":"edited in place"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("PUT on an imported agent = %d, want 409; body: %s", w.Code, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["manage_class"] != "external" || got["copy_to_managed"] != true {
		t.Errorf("refusal = %v, want external with a copy path", got)
	}

	after, _ := a.Services.Store.GetAgentBySlug(t.Context(), "imported-reviewer")
	if after.Description != "Came from outside" {
		t.Errorf("the refused edit landed: %q", after.Description)
	}

	// And the copy path actually works from here.
	w, body = mgReq(t, mux, "POST", "/api/agents/"+row.ID+"/copy-to-managed", "")
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("copy-to-managed = %d; body: %s", w.Code, body)
	}
	var copied agentViewResp
	if err := json.Unmarshal(body, &copied); err != nil {
		t.Fatalf("decode copy: %v", err)
	}
	if !copied.Editable || copied.ManageClass != "managed" {
		t.Errorf("copy = %+v, want an editable managed profile", copied)
	}
}
