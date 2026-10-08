package api

import (
	"bytes"
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type retainedOperationGuardStub struct {
	agentChatStub
	calls atomic.Int32
}

func (s *retainedOperationGuardStub) RetryLastMessage(context.Context, string) (string, error) {
	s.calls.Add(1)
	return "legacy-turn", nil
}
func (s *retainedOperationGuardStub) SendAgentMessage(context.Context, string, string, string) (string, error) {
	s.calls.Add(1)
	return "legacy-turn", nil
}
func (s *retainedOperationGuardStub) CancelActiveGeneration(string) bool {
	s.calls.Add(1)
	return true
}

func TestCognitiveRetainedRoutesRefuseDefinedViewsAndKeepLegacyDispatch(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	view, err := a.Services.CognitiveViews.Create(t.Context(), service.CreateDefinedView{DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef})
	if err != nil {
		t.Fatal(err)
	}
	legacy := &store.Session{ID: "legacy", Provider: "fixture", Model: "model"}
	if err = a.store.CreateSession(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	chat := &retainedOperationGuardStub{}
	a.Services.Chat = chat
	for _, target := range []struct {
		id     string
		status int
	}{{view.ID, 422}, {legacy.ID, 202}} {
		for _, operation := range []string{"retry", "agent-message", "chat/cancel"} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/sessions/"+target.id+"/"+operation, bytes.NewBufferString(`{"from_session_id":"sender","content":"peer"}`)))
			want := target.status
			if operation == "chat/cancel" && want == 202 {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("%s/%s: status%d want%d: %s", target.id, operation, w.Code, want, w.Body.String())
			}
		}
	}
	if chat.calls.Load() != 3 {
		t.Fatal("defined view reached legacy dispatcher", chat.calls.Load())
	}
	rows, err := a.store.ListMessages(t.Context(), view.ID, 100)
	if err != nil || len(rows) != 0 {
		t.Fatal("retained bypass wrote defined transcript", rows, err)
	}
}
