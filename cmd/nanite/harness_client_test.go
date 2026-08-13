package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// newTestHarnessServer wires a minimal stand-in for internal/api/harness_v1.go's
// routes, just enough to exercise harnessClient's request/response and SSE
// parsing without pulling in the full service.Container.
func newTestHarnessServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var lastAuth string
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/harness/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if u, _, ok := r.BasicAuth(); ok {
			lastAuth = u
		}
		var req harnessCreateSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.WorkspaceID == "" {
			http.Error(w, "workspace_id is required", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(harnessSessionResponse{
			Session: &store.Session{ID: "sess-1", WorkspaceID: req.WorkspaceID, Title: req.Title},
		})
	})

	mux.HandleFunc("GET /api/harness/v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "missing" {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(harnessSessionResponse{
			Session: &store.Session{ID: id, WorkspaceID: "ws-1"},
		})
	})

	mux.HandleFunc("POST /api/harness/v1/sessions/{id}/turns", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(harnessTurnResponse{
			SessionID: id,
			MessageID: "msg-1",
			StreamURL: fmt.Sprintf("/api/harness/v1/sessions/%s/events?message_id=msg-1", id),
		})
	})

	mux.HandleFunc("POST /api/harness/v1/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(harnessCancelResponse{SessionID: r.PathValue("id"), Status: "cancelled"})
	})

	mux.HandleFunc("GET /api/harness/v1/sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("message_id") == "" {
			http.Error(w, "message_id query parameter is required", http.StatusBadRequest)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, evt := range []string{
			`{"type":"delta","content":"hello "}`,
			`{"type":"delta","content":"world"}`,
			`{"type":"tool_call","tool":"grep","detail":"pattern"}`,
			`{"type":"stream_end"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", evt)
			flusher.Flush()
		}
	})

	return httptest.NewServer(mux), &lastAuth
}

func TestHarnessClient_CreateAndGetSession(t *testing.T) {
	srv, _ := newTestHarnessServer(t)
	defer srv.Close()

	client := newHarnessClient(srv.URL)
	ctx := context.Background()

	sess, err := client.CreateSession(ctx, harnessCreateSessionRequest{WorkspaceID: "ws-1", Title: "test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID != "sess-1" || sess.WorkspaceID != "ws-1" {
		t.Fatalf("unexpected session: %+v", sess)
	}

	got, err := client.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != "sess-1" {
		t.Fatalf("unexpected session id: %q", got.ID)
	}

	if _, err := client.GetSession(ctx, "missing"); err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
}

func TestHarnessClient_CreateSession_RequiresWorkspace(t *testing.T) {
	srv, _ := newTestHarnessServer(t)
	defer srv.Close()

	client := newHarnessClient(srv.URL)
	if _, err := client.CreateSession(context.Background(), harnessCreateSessionRequest{}); err == nil {
		t.Fatal("expected error when workspace_id is empty, got nil")
	}
}

func TestHarnessClient_SendTurnAndStreamEvents(t *testing.T) {
	srv, _ := newTestHarnessServer(t)
	defer srv.Close()

	client := newHarnessClient(srv.URL)
	ctx := context.Background()

	turn, err := client.SendTurn(ctx, "sess-1", "hi")
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if turn.MessageID != "msg-1" {
		t.Fatalf("unexpected message id: %q", turn.MessageID)
	}

	events, err := client.StreamEvents(ctx, "sess-1", turn.MessageID)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	var got []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				goto done
			}
			got = append(got, evt.Type)
			if evt.Type == "delta" && evt.Content == "" {
				t.Fatalf("delta event missing content: %+v", evt)
			}
			if evt.Type == "tool_call" && evt.Tool != "grep" {
				t.Fatalf("unexpected tool_call event: %+v", evt)
			}
		case <-deadline:
			t.Fatal("timed out waiting for stream events")
		}
	}
done:
	want := []string{"delta", "delta", "tool_call", "stream_end"}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHarnessClient_Cancel(t *testing.T) {
	srv, _ := newTestHarnessServer(t)
	defer srv.Close()

	client := newHarnessClient(srv.URL)
	resp, err := client.Cancel(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != "cancelled" {
		t.Fatalf("unexpected status: %q", resp.Status)
	}
}

func TestHarnessClient_BasicAuth(t *testing.T) {
	srv, lastAuth := newTestHarnessServer(t)
	defer srv.Close()

	t.Setenv("NANITE_AUTH_USER", "alice")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")

	client := newHarnessClient(srv.URL)
	if _, err := client.CreateSession(context.Background(), harnessCreateSessionRequest{WorkspaceID: "ws-1"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if *lastAuth != "alice" {
		t.Fatalf("expected request to carry basic auth user 'alice', got %q", *lastAuth)
	}
}
