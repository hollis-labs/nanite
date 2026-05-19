package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleGetSession_InterruptedTurn covers CW-20260518-0084: the session GET
// response carries an `interrupted_turn` signal when a service restart killed
// an in-flight turn's backend agent. The FE uses it to swap the endless
// "generating" spinner for a clear interrupted state.
func TestHandleGetSession_InterruptedTurn(t *testing.T) {
	getInterrupted := func(t *testing.T, mux *http.ServeMux, sessID string) (map[string]any, bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessID, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
		}
		var resp struct {
			InterruptedTurn map[string]any `json:"interrupted_turn"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp.InterruptedTurn, resp.InterruptedTurn != nil
	}

	seed := func(t *testing.T, a *API, sessID string, lastRole string) {
		t.Helper()
		ws := &store.Workspace{ID: sessID + "-ws", Name: sessID + "-ws"}
		if err := a.Services.Store.CreateWorkspace(ws); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		sess := &store.Session{ID: sessID, WorkspaceID: ws.ID, Title: "t"}
		if err := a.Services.Store.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if err := a.Services.Store.CreateMessage(&store.Message{
			ID: sessID + "-m0", SessionID: sessID, Role: "user", Content: "hello",
		}); err != nil {
			t.Fatalf("CreateMessage user: %v", err)
		}
		if lastRole == "assistant" {
			if err := a.Services.Store.CreateMessage(&store.Message{
				ID: sessID + "-m1", SessionID: sessID, Role: "assistant", Content: "hi",
			}); err != nil {
				t.Fatalf("CreateMessage assistant: %v", err)
			}
		}
	}

	t.Run("interrupted when last message is an unanswered user turn and no live stream", func(t *testing.T) {
		a, mux := newTestAPI(t)
		seed(t, a, "interrupt-a", "user")

		it, ok := getInterrupted(t, mux, "interrupt-a")
		if !ok {
			t.Fatalf("expected interrupted_turn to be present")
		}
		if it["interrupted"] != true {
			t.Errorf("interrupted = %v, want true", it["interrupted"])
		}
		if it["reason"] != "service_restart" {
			t.Errorf("reason = %v, want service_restart", it["reason"])
		}
		if it["last_message_id"] != "interrupt-a-m0" {
			t.Errorf("last_message_id = %v, want interrupt-a-m0", it["last_message_id"])
		}
	})

	t.Run("not interrupted when the turn completed (last message is assistant)", func(t *testing.T) {
		a, mux := newTestAPI(t)
		seed(t, a, "interrupt-b", "assistant")

		if _, ok := getInterrupted(t, mux, "interrupt-b"); ok {
			t.Errorf("expected interrupted_turn to be null for a completed turn")
		}
	})

	t.Run("not interrupted when a live stream exists for the session", func(t *testing.T) {
		a, mux := newTestAPI(t)
		seed(t, a, "interrupt-c", "user")

		// A live in-memory stream for the session means a turn is genuinely
		// generating — not interrupted.
		a.Services.Streams.CreateStream("interrupt-c-msg", "interrupt-c")

		if _, ok := getInterrupted(t, mux, "interrupt-c"); ok {
			t.Errorf("expected interrupted_turn to be null while a live stream exists")
		}
	})
}
