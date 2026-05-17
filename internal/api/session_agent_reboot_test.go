package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// rebootChatStub implements service.ChatService for the reboot-endpoint
// test. Only RebootSessionAgent is exercised; the embedded nil interface
// makes any other call panic, surfacing accidental coupling.
type rebootChatStub struct {
	service.ChatService
	rebootFn func(sessionID string) (service.RebootResult, error)
}

func (s *rebootChatStub) RebootSessionAgent(_ context.Context, sessionID string) (service.RebootResult, error) {
	return s.rebootFn(sessionID)
}

// TestHandleRebootSessionAgent_UnknownSession returns 404 before the chat
// service is consulted.
func TestHandleRebootSessionAgent_UnknownSession(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/does-not-exist/agent/reboot", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleRebootSessionAgent_HappyPath seeds a session, stubs a successful
// reboot, and asserts 200 + the {rebooted, status} body, and that the
// handler forwarded the path session id to the service.
func TestHandleRebootSessionAgent_HappyPath(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{ID: "reboot-sess", Title: "Reboot Test"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	called := make(chan string, 1)
	a.Services.Chat = &rebootChatStub{
		rebootFn: func(sessionID string) (service.RebootResult, error) {
			called <- sessionID
			return service.RebootResult{Rebooted: true, Status: "rebooted"}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sess.ID+"/agent/reboot", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var got service.RebootResult
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Rebooted || got.Status != "rebooted" {
		t.Errorf("body = %+v, want {Rebooted:true Status:rebooted}", got)
	}
	select {
	case sid := <-called:
		if sid != sess.ID {
			t.Errorf("RebootSessionAgent called with %q, want %q", sid, sess.ID)
		}
	default:
		t.Fatal("RebootSessionAgent was not invoked")
	}
}

// TestHandleRebootSessionAgent_BusyReturns409 maps service.ErrSessionBusy
// (a turn in flight) to HTTP 409 Conflict so the FE can tell the operator
// to retry rather than treating it as a hard failure.
func TestHandleRebootSessionAgent_BusyReturns409(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{ID: "reboot-busy", Title: "Reboot Busy"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	a.Services.Chat = &rebootChatStub{
		rebootFn: func(string) (service.RebootResult, error) {
			return service.RebootResult{}, service.ErrSessionBusy
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sess.ID+"/agent/reboot", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body: %s", w.Code, w.Body.String())
	}
}
