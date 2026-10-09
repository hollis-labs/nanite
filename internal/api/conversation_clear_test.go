package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClearSession_HTTPWorkingResetPreservesTranscript(t *testing.T) {
	a, mux := newTestAPI(t)
	const sid = "clear-http"
	seedCompactSession(t, a, sid, 4)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/clear", strings.NewReader(`{}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("clear=%d %s", w.Code, w.Body.String())
	}
	working, err := a.store.ListWorkingMessages(t.Context(), sid, 100)
	if err != nil || len(working) != 0 {
		t.Fatalf("working=%+v %v", working, err)
	}
	full, err := a.store.ListMessages(t.Context(), sid, 100)
	if err != nil || len(full) != 5 || full[4].Content != "Conversation cleared here" {
		t.Fatalf("full=%+v %v", full, err)
	}
}

func TestClearSession_HTTPStrictOptionsAndMissing(t *testing.T) {
	a, mux := newTestAPI(t)
	seedCompactSession(t, a, "strict-clear", 2)
	for _, body := range []string{`null`, `{"unknown":true}`, `{"keep_handoff":"yes"}`, `{} {}`, `[]`, ``} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/strict-clear/clear", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d %s", body, w.Code, w.Body.String())
		}
	}
	cut, err := a.store.LatestConversationClear(t.Context(), "strict-clear")
	if err != nil || cut != nil {
		t.Fatalf("refused request wrote boundary=%+v %v", cut, err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/missing/clear", strings.NewReader(`{}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing=%d %s", w.Code, w.Body.String())
	}
}
