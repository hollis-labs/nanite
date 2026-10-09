package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// flakyTransport fails the first failCount RoundTrip calls with a network
// error (simulating a connection blip or a server mid-restart) before
// delegating to inner. CW-20260813-0008.
type flakyTransport struct {
	failCount int32
	inner     http.RoundTripper
}

func (t *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if atomic.AddInt32(&t.failCount, -1) >= 0 {
		return nil, errors.New("simulated connection failure")
	}
	return t.inner.RoundTrip(req)
}

// newTestAgentServer wires a minimal stand-in for internal/api/agent_v1.go's
// routes, just enough to exercise agentClient's request/response and SSE
// parsing without pulling in the full service.Container.
func newTestAgentServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var lastAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/v1/initialize", func(w http.ResponseWriter, r *http.Request) {
		d, err := service.EmbeddedDefinition()
		if err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"default_definition_ref": d.Ref})
	})
	mux.HandleFunc("POST /api/agent/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if u, _, ok := r.BasicAuth(); ok {
			lastAuth = u
		}
		var req agentCreateSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(agentSessionResponse{
			Session: &store.Session{ID: "sess-1", Title: req.Title},
		})
	})

	mux.HandleFunc("GET /api/agent/v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "missing" {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(agentSessionResponse{
			Session: &store.Session{ID: id},
		})
	})

	mux.HandleFunc("POST /api/agent/v1/sessions/{id}/turns", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(agentTurnResponse{
			SessionID: id,
			MessageID: "msg-1", TurnID: "msg-1",
			StreamURL: fmt.Sprintf("/api/agent/v1/sessions/%s/turns/msg-1/events", id),
		})
	})

	mux.HandleFunc("POST /api/agent/v1/sessions/{id}/turns/{turnId}/cancel", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(agentCancelResponse{SessionID: r.PathValue("id"), Status: "canceled"})
	})

	mux.HandleFunc("GET /api/agent/v1/sessions/{id}/turns/{turnId}/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeAgentCanonicalFixture(w, true)
		flusher.Flush()
	})

	return httptest.NewServer(mux), &lastAuth
}

func TestAgentClient_CreateAndGetSession(t *testing.T) {
	srv, _ := newTestAgentServer(t)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	ctx := context.Background()

	sess, err := client.CreateSession(ctx, agentCreateSessionRequest{Title: "test"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID != "sess-1" {
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

// TestAgentClient_CreateSession_RequiresWorkspace was removed by Phase 0
// item 20 (retire workspaces,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
// workspace_id is no longer required (or accepted) on session creation.

func TestAgentClient_SendTurnAndStreamEvents(t *testing.T) {
	srv, _ := newTestAgentServer(t)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	ctx := context.Background()

	turn, err := client.SendTurn(ctx, "sess-1", "hi")
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if turn.MessageID != "msg-1" {
		t.Fatalf("unexpected message id: %q", turn.MessageID)
	}

	events, err := client.StreamEvents(ctx, turn.StreamURL, turn.TurnID)
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

// TestAgentClient_StreamEvents_SurfacesScanError pins a Copilot PR#221
// review finding: a scan failure (network read error, or here
// bufio.ErrTooLong from a token exceeding the decoder's max buffer)
// otherwise terminated the parsing goroutine silently — the channel just
// closed with no hint why. It must now surface as a synthetic "error"
// stream event instead.
func TestAgentClient_StreamEvents_SurfacesScanError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/v1/sessions/sess-1/turns/msg-1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// A single line longer than ssestream.Decoder's ~32MB
		// (bufio.MaxScanTokenSize<<9) max buffer, with no terminating
		// newline, forces its internal bufio.Scanner to fail with
		// bufio.ErrTooLong once it can no longer grow its token buffer.
		// Streamed in fixed-size chunks — rather than building one big
		// string via concatenation — to keep peak test memory low.
		io.WriteString(w, "data: ")
		oversize := (bufio.MaxScanTokenSize << 9) + 1024
		const chunkSize = 64 * 1024
		chunk := strings.Repeat("x", chunkSize)
		for written := 0; written < oversize; written += chunkSize {
			n := chunkSize
			if remaining := oversize - written; remaining < chunkSize {
				n = remaining
			}
			io.WriteString(w, chunk[:n])
		}
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	events, err := client.StreamEvents(context.Background(), "/api/agent/v1/sessions/sess-1/turns/msg-1/events", "msg-1")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	deadline := time.After(5 * time.Second)
	select {
	case evt, ok := <-events:
		if !ok {
			t.Fatal("channel closed with no error event; scanner.Err() was silently dropped")
		}
		if evt.Type != "error" {
			t.Fatalf("got event type %q, want %q", evt.Type, "error")
		}
		if evt.Error == "" {
			t.Fatal("error event has an empty Error field")
		}
	case <-deadline:
		t.Fatal("timed out waiting for the error event")
	}
}

// TestAgentClient_StreamEvents_SurfacesMalformedEvent pins CW-20260813-0004
// item 2: a json.Unmarshal failure on a data: payload must surface as a
// synthetic "error" stream event — mirroring how a decoder read failure
// already surfaces — rather than being silently dropped. The stream must
// keep going afterward so a single malformed frame doesn't kill the turn.
func TestAgentClient_StreamEvents_SurfacesMalformedEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/v1/sessions/sess-1/turns/msg-1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: not valid json\n\n")
		fmt.Fprint(w, `data: {"type":"delta","content":"still here"}`+"\n\n")
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	events, err := client.StreamEvents(context.Background(), "/api/agent/v1/sessions/sess-1/turns/msg-1/events", "msg-1")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	deadline := time.After(5 * time.Second)
	var got []string
	for len(got) < 1 {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatalf("channel closed early after %v events", got)
			}
			got = append(got, evt.Type)
			if evt.Type == "error" && evt.Error == "" {
				t.Fatal("error event has an empty Error field")
			}
		case <-deadline:
			t.Fatalf("timed out waiting for events, got %v so far", got)
		}
	}
	want := []string{"error"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %q, want %q (got %v)", i, got[i], want[i], got)
		}
	}
}

func TestAgentClient_Cancel(t *testing.T) {
	srv, _ := newTestAgentServer(t)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	resp, err := client.Cancel(context.Background(), "sess-1", "msg-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != "canceled" {
		t.Fatalf("unexpected status: %q", resp.Status)
	}
}

func TestAgentClient_BasicAuth(t *testing.T) {
	srv, lastAuth := newTestAgentServer(t)
	defer srv.Close()

	t.Setenv("NANITE_AUTH_USER", "alice")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")

	client := newAgentClient(srv.URL)
	if _, err := client.CreateSession(context.Background(), agentCreateSessionRequest{}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if *lastAuth != "alice" {
		t.Fatalf("expected request to carry basic auth user 'alice', got %q", *lastAuth)
	}
}

// An unsuccessful POST has an uncertain outcome: never repeat it automatically.
func TestAgentClient_SendTurn_DoesNotRetryTransportFailure(t *testing.T) {
	srv, _ := newTestAgentServer(t)
	defer srv.Close()
	transport := &flakyTransport{failCount: 2, inner: http.DefaultTransport}
	client := &agentClient{baseURL: srv.URL, http: &http.Client{Transport: transport}}
	_, err := client.SendTurn(context.Background(), "sess-1", "hi")
	if err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("SendTurn = %v, want uncertain outcome", err)
	}
	if transport.failCount != 1 {
		t.Fatalf("POST was retried: remaining failures %d", transport.failCount)
	}
}

// TestAgentClient_StreamEvents_RetriesConnectionOpen mirrors the SendTurn
// case for StreamEvents' initial GET that opens the SSE connection.
func TestAgentClient_StreamEvents_RetriesConnectionOpen(t *testing.T) {
	srv, _ := newTestAgentServer(t)
	defer srv.Close()

	client := &agentClient{
		baseURL: srv.URL,
		http:    &http.Client{Transport: &flakyTransport{failCount: 2, inner: http.DefaultTransport}},
	}

	events, err := client.StreamEvents(context.Background(), "/api/agent/v1/sessions/sess-1/turns/msg-1/events", "msg-1")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	var gotStreamEnd bool
	deadline := time.After(5 * time.Second)
	for !gotStreamEnd {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("channel closed before stream_end")
			}
			if evt.Type == "stream_end" {
				gotStreamEnd = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for stream_end")
		}
	}
}

// TestAgentClient_SendTurn_GivesUpAfterMaxAttempts pins the "bounded
// number of retries, not infinite" requirement: a connection that never
// succeeds must fail with a clear error in bounded time, not hang.
func TestAgentClient_SendTurn_GivesUpAfterMaxAttempts(t *testing.T) {
	client := &agentClient{
		baseURL: "http://127.0.0.1:1",
		http:    &http.Client{Transport: &flakyTransport{failCount: 1000, inner: http.DefaultTransport}},
	}

	start := time.Now()
	_, err := client.SendTurn(context.Background(), "sess-1", "hi")
	if err == nil {
		t.Fatal("SendTurn: expected an error after exhausting retries, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("SendTurn took %s to give up, want well under 5s", elapsed)
	}
}

// TestAgentClient_SendTurn_DoesNotRetryServerError pins the other half of
// the CW-20260813-0008 distinction: a definitive response from the server
// (even an error one) means the connection was established — it must not
// be retried, since the server may have already acted on the request.
func TestAgentClient_SendTurn_DoesNotRetryServerError(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agent/v1/sessions/{id}/turns", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	if _, err := client.SendTurn(context.Background(), "sess-1", "hi"); err == nil {
		t.Fatal("SendTurn: expected an error for a 500 response, got nil")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server called %d times, want exactly 1 (a definitive server response must not be retried)", got)
	}
}

// TestAgentClient_StreamEvents_DoesNotRetryMidStreamFailure pins the core
// distinction CW-20260813-0008 is about: once StreamEvents has opened the
// connection and started delivering events, a failure (here, the server
// dropping the connection mid-stream) must surface as a synthetic error
// event, never as a reopened/retried connection.
func TestAgentClient_StreamEvents_DoesNotRetryMidStreamFailure(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/v1/sessions/sess-1/turns/msg-1/events", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeAgentCanonicalFixture(w, false)
		flusher.Flush()

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("test server response writer does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		conn.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newAgentClient(srv.URL)
	events, err := client.StreamEvents(context.Background(), "/api/agent/v1/sessions/sess-1/turns/msg-1/events", "msg-1")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	var gotDelta, gotError bool
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				break loop
			}
			switch evt.Type {
			case "delta":
				gotDelta = true
			case "error":
				gotError = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for the stream to close")
		}
	}
	if !gotDelta {
		t.Fatal("expected at least one delta event before the connection dropped")
	}
	if !gotError {
		t.Fatal("expected a synthetic error event for the mid-stream read failure")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GET /events called %d times, want exactly 1 (mid-stream failures must not retry the connection)", got)
	}
}

func writeAgentCanonicalFixture(w io.Writer, terminal bool) {
	events := []chatstream.Event{{Verb: chatstream.VerbRunStart}, {Verb: chatstream.VerbMessageStart, MessageID: "msg-1", Role: "assistant"}, {Verb: chatstream.VerbPartStart, PartID: "text", Kind: "text"}, {Verb: chatstream.VerbPartDelta, PartID: "text", Text: "hello "}, {Verb: chatstream.VerbPartDelta, PartID: "text", Text: "world"}}
	if terminal {
		events = append(events, chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: "text"}, chatstream.Event{Verb: chatstream.VerbPartStart, PartID: "tool", Kind: "tool_call", Meta: map[string]json.RawMessage{"name": json.RawMessage(`"grep"`), "detail": json.RawMessage(`"pattern"`)}}, chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: "tool"}, chatstream.Event{Verb: chatstream.VerbMessageEnd, MessageID: "msg-1"}, chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: "stop"})
	}
	for i, event := range events {
		event.V = "1"
		event.Seq = uint64(i + 1)
		event.RunID = "msg-1"
		event.Time = time.Now().UTC()
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event.Verb, event.Seq, data)
	}
}
