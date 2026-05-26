package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// forkAndDecode POSTs a fork request and returns the created session.
func forkAndDecode(t *testing.T, mux http.Handler, sourceID string, body ForkSessionRequest) *store.Session {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sourceID+"/fork", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("fork: expected 201, got %d (body=%s)", w.Code, w.Body.String())
	}
	var got store.Session
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode fork response: %v (body=%s)", err, w.Body.String())
	}
	return &got
}

// TestHandleForkSession_ModeIDOverride verifies the fork handler honors an
// explicit mode_id (Slice 4 fork-contract drift fix): the GUI sends the
// source session's resolved mode on every fork, and that mode must land on
// the forked session rather than being silently dropped.
func TestHandleForkSession_ModeIDOverride(t *testing.T) {
	a, mux := newTestAPI(t)

	ws := &store.Workspace{ID: "fork-ws", Name: "Fork Test"}
	if err := a.Services.Store.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	planMode := &store.Mode{ID: "fork-mode-plan", Slug: "fork-plan", Name: "Fork Plan"}
	if err := a.Services.Store.CreateMode(planMode); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}
	src := &store.Session{ID: "fork-src", WorkspaceID: ws.ID, Title: "source"}
	if err := a.Services.Store.CreateSession(src); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got := forkAndDecode(t, mux, src.ID, ForkSessionRequest{ModeID: planMode.ID})

	if got.CurrentModeID == nil || *got.CurrentModeID != planMode.ID {
		t.Fatalf("fork current_mode_id = %v, want %q", got.CurrentModeID, planMode.ID)
	}
	if got.ID == src.ID {
		t.Fatalf("fork must create a new session, got source id %q", got.ID)
	}
}

// TestHandleForkSession_InheritsSourceMode verifies the default path is
// preserved: with no mode_id in the request, the fork inherits the source
// session's current_mode_id (store-default copy behavior).
func TestHandleForkSession_InheritsSourceMode(t *testing.T) {
	a, mux := newTestAPI(t)

	ws := &store.Workspace{ID: "fork2-ws", Name: "Fork Inherit Test"}
	if err := a.Services.Store.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	mode := &store.Mode{ID: "fork2-mode", Slug: "fork2-mode", Name: "Fork2 Mode"}
	if err := a.Services.Store.CreateMode(mode); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}
	src := &store.Session{ID: "fork2-src", WorkspaceID: ws.ID, Title: "source"}
	if err := a.Services.Store.CreateSession(src); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Mode is set via the dedicated path (CreateSession does not persist it).
	if err := a.Services.Store.SetSessionMode(src.ID, mode.ID); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	got := forkAndDecode(t, mux, src.ID, ForkSessionRequest{})

	if got.CurrentModeID == nil || *got.CurrentModeID != mode.ID {
		t.Fatalf("fork should inherit source mode %q, got %v", mode.ID, got.CurrentModeID)
	}
}
