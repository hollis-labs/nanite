package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestGetMessageTranscript_Success(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	if err := a.store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	msg := &store.Message{
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   "This is the exact durable prose.",
	}
	if err := a.store.CreateMessage(ctx, msg); err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID+"/messages/"+msg.ID+"/transcript", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["transcript"] != "This is the exact durable prose." {
		t.Errorf("expected correct transcript, got %q", resp["transcript"])
	}
}

func TestGetMessageTranscript_ForeignSession(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	sess1 := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	sess2 := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	_ = a.store.CreateSession(ctx, sess1)
	_ = a.store.CreateSession(ctx, sess2)

	msg := &store.Message{
		SessionID: sess1.ID,
		Role:      "assistant",
		Content:   "This is the exact durable prose.",
	}
	_ = a.store.CreateMessage(ctx, msg)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess2.ID+"/messages/"+msg.ID+"/transcript", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", w.Code)
	}
}

func TestGetMessageTranscript_NotFound(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	_ = a.store.CreateSession(ctx, sess)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID+"/messages/invalid-id/transcript", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", w.Code)
	}
}
