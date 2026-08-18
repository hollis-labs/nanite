package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestListSessionPluginEnvelopes_RehydratesPendingStandaloneCards(t *testing.T) {
	a, mux := newTestAPI(t)

	if err := a.Services.Store.CreateWorkspace(&store.Workspace{ID: "ws-penv", Name: "plugin env"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws-penv"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	pending := &store.EnvelopeInstance{
		ID:           "env-pending",
		SessionID:    sess.ID,
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: `{"run_id":"r-1","role":"worker","prompt":"fix it","mode":"interactive"}`,
	}
	if err := a.Services.Store.CreateEnvelopeInstance(pending); err != nil {
		t.Fatalf("CreateEnvelopeInstance pending: %v", err)
	}

	responded := &store.EnvelopeInstance{
		ID:           "env-responded",
		SessionID:    sess.ID,
		EnvelopeType: "elicitation-prompt",
		EnvelopeJSON: `{"elicitation_id":"e-1","message":"Need input","schema_type":"string","origin":"server","timeout_at":"2026-05-17T00:00:00Z"}`,
	}
	if err := a.Services.Store.CreateEnvelopeInstance(responded); err != nil {
		t.Fatalf("CreateEnvelopeInstance responded: %v", err)
	}
	if err := a.Services.Store.RecordResponse(responded.ID, "submitted", `{"status":"submitted"}`); err != nil {
		t.Fatalf("RecordResponse responded: %v", err)
	}

	ignored := &store.EnvelopeInstance{
		ID:           "env-inline",
		SessionID:    sess.ID,
		EnvelopeType: "approval-card",
		EnvelopeJSON: `{"prompt":"inline only"}`,
	}
	if err := a.Services.Store.CreateEnvelopeInstance(ignored); err != nil {
		t.Fatalf("CreateEnvelopeInstance ignored: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/sessions/"+sess.ID+"/plugin-envelopes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 pending standalone envelope, got %d", len(got))
	}
	if got[0]["id"] != "env-pending" {
		t.Errorf("id = %v, want env-pending", got[0]["id"])
	}
	if got[0]["type"] != "subagent-spawn-approval" {
		t.Errorf("type = %v, want subagent-spawn-approval", got[0]["type"])
	}
	if got[0]["display_class"] != "action-required" {
		t.Errorf("display_class = %v, want action-required", got[0]["display_class"])
	}
	data, _ := got[0]["data"].(map[string]any)
	if data["run_id"] != "r-1" {
		t.Errorf("data.run_id = %v, want r-1", data["run_id"])
	}
}
