package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
)

// chatCancelStub implements service.ChatService for the cancel-endpoint
// test. The endpoint only consults CancelActiveGeneration; the rest of
// the interface is unused here, so the stub's other methods panic to
// surface any accidental coupling.
type chatCancelStub struct {
	cancelFn func(sessionID string) bool
}

func (s *chatCancelStub) HandleMessage(context.Context, string, string) (string, error) {
	panic("HandleMessage not used in cancel-endpoint tests")
}
func (s *chatCancelStub) RetryLastMessage(context.Context, string) (string, error) {
	panic("RetryLastMessage not used in cancel-endpoint tests")
}
func (s *chatCancelStub) SendAgentMessage(context.Context, string, string, string) (string, error) {
	panic("SendAgentMessage not used in cancel-endpoint tests")
}
func (s *chatCancelStub) DelegateTask(context.Context, chat.DelegationRequest) (*chat.DelegationResult, error) {
	panic("DelegateTask not used in cancel-endpoint tests")
}
func (s *chatCancelStub) DelegateAndAggregate(context.Context, string, string, string) (*chat.OrchestrationResult, error) {
	panic("DelegateAndAggregate not used in cancel-endpoint tests")
}
func (s *chatCancelStub) GetStream(string) (<-chan chat.StreamEvent, bool) {
	panic("GetStream not used in cancel-endpoint tests")
}
func (s *chatCancelStub) CancelActiveGeneration(sessionID string) bool {
	return s.cancelFn(sessionID)
}
func (s *chatCancelStub) RebootSessionAgent(context.Context, string) (service.RebootResult, error) {
	panic("RebootSessionAgent not used in cancel-endpoint tests")
}
func (s *chatCancelStub) RecoverSession(context.Context, string) (service.RebootResult, error) {
	panic("RecoverSession not used in cancel-endpoint tests")
}
func (s *chatCancelStub) Shutdown() error { return nil }

// TestChatCancelEndpoint_HappyPath verifies that POST
// /api/sessions/{id}/chat/cancel dispatches the cancel through the
// chat service and returns 200 + {"status":"cancelled"}. CW-20260512-0006.
func TestChatCancelEndpoint_HappyPath(t *testing.T) {
	a, mux := newTestAPI(t)

	called := make(chan string, 1)
	a.Services.Chat = &chatCancelStub{
		cancelFn: func(sessionID string) bool {
			called <- sessionID
			return true
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-cancel/chat/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "cancelled" {
		t.Errorf("status = %q, want cancelled", got["status"])
	}

	select {
	case sid := <-called:
		if sid != "s-cancel" {
			t.Errorf("CancelActiveGeneration called with sessionID = %q, want s-cancel", sid)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("CancelActiveGeneration was not invoked")
	}
}

// TestChatCancelEndpoint_NoActive verifies that the endpoint returns 404
// when there is no active generation for the session. This is the
// idempotent path: clicking stop twice or stopping after the stream
// already ended is not an error condition the user should see surfaced
// as failure — the FE just ignores 404.
func TestChatCancelEndpoint_NoActive(t *testing.T) {
	a, mux := newTestAPI(t)

	a.Services.Chat = &chatCancelStub{
		cancelFn: func(string) bool { return false },
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-idle/chat/cancel", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}
