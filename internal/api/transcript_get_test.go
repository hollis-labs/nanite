package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
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

func TestGetMessageTranscript_ExactProseWithoutProjectionMutation(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "host-owned", Model: "m"}
	if err := a.store.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, role, content, want string }{
		{"wrapped assistant", "assistant", `{"v":1,"text":"  Exact\nUnicode: λ🙂  ","envelopes":[{"private":"hidden"}],"tool_calls":[{"id":"opaque"}]}`, "  Exact\nUnicode: λ🙂  "},
		{"empty assistant", "assistant", `{"v":1,"text":"","envelopes":[{"private":"hidden"}]}`, ""},
		{"literal JSON user", "user", `{"v":1,"text":"literal user input"}`, `{"v":1,"text":"literal user input"}`},
		{"bare assistant", "assistant", "exact old prose\nwith spacing  ", "exact old prose\nwith spacing  "},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg := &store.Message{SessionID: sess.ID, Role: test.role, Content: test.content, IsCompacted: true}
			if err := a.store.CreateMessage(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID+"/messages/"+msg.ID+"/transcript", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			var result map[string]string
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result["transcript"] != test.want {
				t.Fatal("exact prose retrieval failed", w.Code, w.Body.String())
			}
			retained, err := a.store.GetMessage(t.Context(), msg.ID)
			if err != nil || retained.Content != test.content || !retained.IsCompacted {
				t.Fatal("retrieval rewrote durable content/projection", retained, err)
			}
		})
	}
}

type transcriptFailureReader struct{ service.SessionService }

func (transcriptFailureReader) GetMessage(context.Context, string) (*store.Message, error) {
	return nil, errors.New("private SQL path and credentials")
}

func TestGetMessageTranscript_InternalErrorPrivacy(t *testing.T) {
	a, mux := newTestAPI(t)
	a.Services.Sessions = transcriptFailureReader{SessionService: a.Services.Sessions}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions/s/messages/m/transcript", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private SQL") || !strings.Contains(w.Body.String(), "internal error") {
		t.Fatal("internal cause leaked", w.Code, w.Body.String())
	}
}
