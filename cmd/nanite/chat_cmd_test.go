package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunChatTurn_CtxCancelInvokesCancelOnce pins CW-20260813-0003: when the
// turn-scoped context fires (simulating a Ctrl-C signal via
// signal.NotifyContext in cmdChat), runChatTurn must call harnessClient.Cancel
// exactly once with the right session id and return promptly instead of
// blocking on the abandoned event stream.
func TestRunChatTurn_CtxCancelInvokesCancelOnce(t *testing.T) {
	var cancelCalls int32
	var lastCancelSessionID string

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/harness/v1/sessions/{id}/turns", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		json.NewEncoder(w).Encode(harnessTurnResponse{
			SessionID: id,
			MessageID: "msg-1",
			StreamURL: fmt.Sprintf("/api/harness/v1/sessions/%s/events?message_id=msg-1", id),
		})
	})
	mux.HandleFunc("GET /api/harness/v1/sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", `{"type":"delta","content":"hello"}`)
		flusher.Flush()
		// Hold the connection open — simulating a long-running turn — until
		// the client gives up, mirroring a real turn interrupted by Ctrl-C.
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /api/harness/v1/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cancelCalls, 1)
		lastCancelSessionID = r.PathValue("id")
		json.NewEncoder(w).Encode(harnessCancelResponse{SessionID: r.PathValue("id"), Status: "cancelled"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newHarnessClient(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- runChatTurn(ctx, client, "sess-1", "hi")
	}()

	// Give the stream time to deliver its first event before simulating the
	// Ctrl-C signal mid-turn.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runChatTurn: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runChatTurn did not return promptly after ctx cancellation")
	}

	if got := atomic.LoadInt32(&cancelCalls); got != 1 {
		t.Fatalf("Cancel called %d times, want 1", got)
	}
	if lastCancelSessionID != "sess-1" {
		t.Fatalf("Cancel called with session id %q, want %q", lastCancelSessionID, "sess-1")
	}
}

// TestRunChatTurn_SessionTakeover pins CW-20260813-0004 item 1: a
// session_takeover event (fired when a second client, e.g. the GUI, attaches
// to the same session) must not be treated as a normal completion —
// runChatTurn returns a non-nil sentinel error instead of nil.
func TestRunChatTurn_SessionTakeover(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/harness/v1/sessions/{id}/turns", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		json.NewEncoder(w).Encode(harnessTurnResponse{
			SessionID: id,
			MessageID: "msg-1",
			StreamURL: fmt.Sprintf("/api/harness/v1/sessions/%s/events?message_id=msg-1", id),
		})
	})
	mux.HandleFunc("GET /api/harness/v1/sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", `{"type":"delta","content":"partial"}`)
		fmt.Fprintf(w, "data: %s\n\n", `{"type":"session_takeover","content":"This session is now active in another tab"}`)
		flusher.Flush()
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newHarnessClient(srv.URL)
	err := runChatTurn(context.Background(), client, "sess-1", "hi")
	if !errors.Is(err, errSessionTakeover) {
		t.Fatalf("runChatTurn error = %v, want errSessionTakeover", err)
	}
}
