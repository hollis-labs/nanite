package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleCreateAgent_DefaultsSourceToUser is the regression pin for
// PR-152 review round 1 (item C): when the caller omits `source`, the
// handler must default it to "user" before inserting. Previously the
// handler accepted the empty string and the row landed with source=''.
func TestHandleCreateAgent_DefaultsSourceToUser(t *testing.T) {
	_, mux := newTestAPI(t)

	body, _ := json.Marshal(map[string]any{
		"id":            "test-agent-source-default",
		"name":          "Source Default",
		"slug":          "source-default",
		"system_prompt": "test prompt",
		// Source intentionally omitted.
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/agents: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var got store.AgentProfile
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Source != "user" {
		t.Errorf("Source = %q, want %q (handler must default omitted source to 'user')", got.Source, "user")
	}
}

// TestHandleCreateAgent_RejectsInternalSource is the regression pin for
// PR-152 review round 1 (item C, complementary): source='internal' is
// reserved for file-sourced internal profiles and must be rejected with
// 400 even after the default-to-user landing.
func TestHandleCreateAgent_RejectsInternalSource(t *testing.T) {
	_, mux := newTestAPI(t)

	body, _ := json.Marshal(map[string]any{
		"id":            "test-agent-internal-reject",
		"name":          "Internal Reject",
		"slug":          "internal-reject",
		"system_prompt": "test prompt",
		"source":        "internal",
	})
	req := httptest.NewRequest("POST", "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/agents with source=internal: expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleUpdateAgent_InternalRejectedAsHarnessOwned pins the new editability
// gate: embedded internal harness profiles are not editable via the API and
// must be rejected with a structured 409 (manage_class=internal, no
// copy-to-managed dead-end), regardless of whether SourceRef is populated.
func TestHandleUpdateAgent_InternalRejectedAsHarnessOwned(t *testing.T) {
	a, mux := newTestAPI(t)

	// Seed an internal-source row with deliberately empty source_ref.
	if err := a.Services.Store.CreateAgent(&store.AgentProfile{
		ID:           "test-internal-empty-ref",
		Name:         "Test Internal",
		Slug:         "test-internal-empty-ref",
		SystemPrompt: "test",
		Source:       "internal",
		SourceRef:    "", // deliberately empty
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	newName := "Updated Name"
	body, _ := json.Marshal(map[string]any{"name": &newName})
	req := httptest.NewRequest("PUT", "/api/agents/test-internal-empty-ref", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("PUT /api/agents/{id} on internal source: expected 409, got %d; body: %s", w.Code, w.Body.String())
	}
	msg := w.Body.String()
	if !strings.Contains(msg, `"manage_class":"internal"`) {
		t.Errorf("error response missing manage_class=internal: %s", msg)
	}
	// Internal agents are harness-owned: no copy-to-managed dead-end offered.
	if !strings.Contains(msg, `"copy_to_managed":false`) {
		t.Errorf("internal agent should not offer copy-to-managed: %s", msg)
	}
}
