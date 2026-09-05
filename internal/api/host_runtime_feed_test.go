package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestHostRuntimeFeedSSEReplaysAfterCommittedCursor(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "runtime-feed.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	for i, kind := range []string{"process.started", "session.ready"} {
		_, _, err := s.AppendHostRuntimeEvent(context.Background(), store.HostRuntimeEvent{
			SessionID:         "session-a",
			RuntimeRunID:      "run-a",
			RuntimeGeneration: 1,
			SourceEventID:     "source-" + kind,
			SourceSequence:    uint64(i + 20),
			Kind:              kind,
			OccurredAt:        "2026-09-05T12:00:00Z",
			Source:            store.HostRuntimeEventSource{Channel: "jsonrpc"},
			Payload:           json.RawMessage(`{"state":"ready"}`),
			PayloadVisibility: "public_metadata",
		}, 10)
		if err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}
	feed := service.NewHostRuntimeFeed(s)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = feed.Close(ctx)
	}()
	a := New(&service.Container{Store: s, RuntimeFeed: feed})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/runtime-events", a.handleHostRuntimeFeed)
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sessions/session-a/runtime-events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Last-Event-ID", "1")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(response.Body)
	var frame strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			cancel()
			_ = response.Body.Close()
			t.Fatalf("read SSE frame: %v", readErr)
		}
		frame.WriteString(line)
		if line == "\n" {
			break
		}
	}
	cancel()
	_ = response.Body.Close()
	if got := frame.String(); !strings.Contains(got, "id: 2\n") || !strings.Contains(got, "event: host_runtime.v1\n") || strings.Contains(got, "process.started") {
		t.Fatalf("replayed frame = %q, want only cursor 2 named event", got)
	}
}

func TestHostRuntimeCursorQueryPrecedesHeaderAndRejectsMalformed(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/sessions/s/runtime-events?after=7", nil)
	request.Header.Set("Last-Event-ID", "99")
	if got, err := hostRuntimeCursor(request); err != nil || got != 7 {
		t.Fatalf("cursor = %d, %v, want query cursor 7", got, err)
	}
	bad := httptest.NewRequest(http.MethodGet, "/api/sessions/s/runtime-events", nil)
	bad.Header.Set("Last-Event-ID", "not-a-cursor")
	if _, err := hostRuntimeCursor(bad); err == nil {
		t.Fatal("malformed Last-Event-ID accepted")
	}
}
