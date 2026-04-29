package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleSetSessionMode_BroadcastsModeChanged verifies F1's cross-tab
// fix: PATCH /api/sessions/{id}/mode now emits a session_mode_changed
// presence event after persisting, so a tab open on the same session
// updates its mode chip without manual refetch. F1 (CW-20260429-0001).
func TestHandleSetSessionMode_BroadcastsModeChanged(t *testing.T) {
	a, mux := newTestAPI(t)

	// Workspace + session.
	ws := &store.Workspace{ID: "f1-ws", Name: "F1 Test"}
	if err := a.Services.Store.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{
		ID:          "f1-sess",
		WorkspaceID: ws.ID,
		Title:       "F1 broadcast test",
	}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Seed a real mode so the resolved-mode read after persistence returns
	// non-nil and the broadcast carries the slug.
	planMode := &store.Mode{
		ID:             "f1-mode-plan",
		Slug:           "f1-plan",
		Name:           "F1 Plan",
		PromptAddendum: "plan-mode prompt",
	}
	if err := a.Services.Store.CreateMode(planMode); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	// Subscribe a presence client BEFORE issuing the PATCH so the broadcast
	// is observed. Drain in a goroutine so the broadcast doesn't block on a
	// slow test consumer.
	clientID, presenceCh := a.Services.Streams.RegisterPresenceClient()
	t.Cleanup(func() { a.Services.Streams.UnregisterPresenceClient(clientID) })

	gotEvents := make(chan chat.PresenceEvent, 4)
	doneRecv := make(chan struct{})
	go func() {
		defer close(doneRecv)
		// Capture all events for the duration of the test; the test code
		// closes the channel via UnregisterPresenceClient cleanup.
		for evt := range presenceCh {
			gotEvents <- evt
		}
	}()

	// PATCH /api/sessions/{id}/mode with a slug.
	body, _ := json.Marshal(map[string]string{"slug": planMode.Slug})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sess.ID+"/mode", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Wait for the session_mode_changed event.
	var evt chat.PresenceEvent
	select {
	case evt = <-gotEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session_mode_changed presence event")
	}

	if evt.Type != "session_mode_changed" {
		t.Errorf("event type = %q, want %q", evt.Type, "session_mode_changed")
	}
	if evt.SessionID != sess.ID {
		t.Errorf("event session_id = %q, want %q", evt.SessionID, sess.ID)
	}
	if evt.ModeID != planMode.ID {
		t.Errorf("event mode_id = %q, want %q", evt.ModeID, planMode.ID)
	}
	if evt.ModeSlug != planMode.Slug {
		t.Errorf("event mode_slug = %q, want %q", evt.ModeSlug, planMode.Slug)
	}
	if evt.Timestamp == "" {
		t.Error("event timestamp should be set")
	}
}

// TestHandleSetSessionMode_ClearBroadcasts verifies the clear path also
// emits a session_mode_changed event with empty mode_id/mode_slug, so the
// FE knows to fall back to the default chat mode. F1 (CW-20260429-0001).
func TestHandleSetSessionMode_ClearBroadcasts(t *testing.T) {
	a, mux := newTestAPI(t)

	ws := &store.Workspace{ID: "f1c-ws", Name: "F1 Clear Test"}
	if err := a.Services.Store.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{
		ID:          "f1c-sess",
		WorkspaceID: ws.ID,
		Title:       "F1 clear broadcast test",
	}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	clientID, presenceCh := a.Services.Streams.RegisterPresenceClient()
	t.Cleanup(func() { a.Services.Streams.UnregisterPresenceClient(clientID) })

	gotEvents := make(chan chat.PresenceEvent, 4)
	go func() {
		for evt := range presenceCh {
			gotEvents <- evt
		}
	}()

	// PATCH with empty body — clear path.
	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sess.ID+"/mode", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var evt chat.PresenceEvent
	select {
	case evt = <-gotEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session_mode_changed (clear) presence event")
	}

	if evt.Type != "session_mode_changed" {
		t.Errorf("event type = %q, want %q", evt.Type, "session_mode_changed")
	}
	if evt.SessionID != sess.ID {
		t.Errorf("event session_id = %q, want %q", evt.SessionID, sess.ID)
	}
	if evt.ModeID != "" {
		t.Errorf("clear path should leave mode_id empty; got %q", evt.ModeID)
	}
	if evt.ModeSlug != "" {
		t.Errorf("clear path should leave mode_slug empty; got %q", evt.ModeSlug)
	}
}
