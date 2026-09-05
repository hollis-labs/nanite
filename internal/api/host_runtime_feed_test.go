package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestHostRuntimeFeedSSEReplaysAfterCommittedCursor(t *testing.T) {
	s, openErr := store.New(context.Background(), filepath.Join(t.TempDir(), "runtime-feed.db"))
	if openErr != nil {
		t.Fatalf("new store: %v", openErr)
	}
	defer func() { _ = s.Close(context.Background()) }()
	if generation, err := s.ReserveHostRuntimeRun(context.Background(), "session-a", "run-a"); err != nil || generation != 1 {
		t.Fatalf("reserve runtime run = %d, %v", generation, err)
	}
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
	headFrame, err := readHostRuntimeSSEFrame(reader)
	if err != nil {
		t.Fatalf("read SSE head: %v", err)
	}
	eventFrame, err := readHostRuntimeSSEFrame(reader)
	if err != nil {
		t.Fatalf("read SSE event: %v", err)
	}
	cancel()
	_ = response.Body.Close()
	if strings.Contains(headFrame, "id:") || !strings.Contains(headFrame, "event: host_runtime.head.v1\n") || !strings.Contains(headFrame, `"runtime_generation_floor":1`) {
		t.Fatalf("head frame = %q", headFrame)
	}
	if got := eventFrame; !strings.Contains(got, "id: 2\n") || !strings.Contains(got, "event: host_runtime.v1\n") || strings.Contains(got, "process.started") {
		t.Fatalf("replayed frame = %q, want only cursor 2 named event", got)
	}
}

func TestHostRuntimeFeedSSEHeadOrdersFreshAndLiveReplacementWithoutSpam(t *testing.T) {
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "runtime-head.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	genA, err := s.ReserveHostRuntimeRun(context.Background(), "session-head", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	appendA := func(sourceID, kind string) {
		t.Helper()
		_, _, appendErr := s.AppendHostRuntimeEvent(context.Background(), store.HostRuntimeEvent{
			SessionID: "session-head", RuntimeRunID: "run-a", RuntimeGeneration: genA,
			SourceEventID: sourceID, SourceSequence: 1, Kind: kind, OccurredAt: "2026-09-05T12:00:00Z",
			Source: store.HostRuntimeEventSource{Channel: "jsonrpc"}, Payload: json.RawMessage(`{}`), PayloadVisibility: "public_metadata",
		}, 10)
		if appendErr != nil {
			t.Fatalf("append %s: %v", sourceID, appendErr)
		}
	}
	appendA("a-ready", "session.ready")
	if genB, reserveErr := s.ReserveHostRuntimeRun(context.Background(), "session-head", "run-b"); reserveErr != nil || genB != 2 {
		t.Fatalf("reserve run-b = %d, %v", genB, reserveErr)
	}
	appendA("a-late-exit", "process.exited")

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
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sessions/session-head/runtime-events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	headB, err := readHostRuntimeSSEFrame(reader)
	if err != nil || strings.Contains(headB, "id:") || !strings.Contains(headB, `"runtime_generation_floor":2`) || !strings.Contains(headB, `"current_runtime_run_id":"run-b"`) {
		t.Fatalf("fresh successor head = %q, %v", headB, err)
	}
	for cursor := 1; cursor <= 2; cursor++ {
		frame, readErr := readHostRuntimeSSEFrame(reader)
		if readErr != nil || !strings.Contains(frame, "id: "+strconv.Itoa(cursor)+"\n") || strings.Contains(frame, "host_runtime.head.v1") {
			t.Fatalf("fresh event %d = %q, %v", cursor, frame, readErr)
		}
	}

	type frameResult struct {
		frame string
		err   error
	}
	nextFrame := make(chan frameResult, 1)
	go func() {
		frame, readErr := readHostRuntimeSSEFrame(reader)
		nextFrame <- frameResult{frame: frame, err: readErr}
	}()
	select {
	case duplicate := <-nextFrame:
		t.Fatalf("identical polling head was repeated: %q (%v)", duplicate.frame, duplicate.err)
	case <-time.After(250 * time.Millisecond):
	}
	if genC, reserveErr := s.ReserveHostRuntimeRun(context.Background(), "session-head", "run-c"); reserveErr != nil || genC != 3 {
		t.Fatalf("reserve run-c = %d, %v", genC, reserveErr)
	}
	appendA("a-even-later-exit", "process.exited")
	select {
	case result := <-nextFrame:
		if result.err != nil || strings.Contains(result.frame, "id:") || !strings.Contains(result.frame, `"runtime_generation_floor":3`) || !strings.Contains(result.frame, `"current_runtime_run_id":"run-c"`) {
			t.Fatalf("live successor head = %q, %v", result.frame, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live reservation head was not emitted")
	}
	lateA, err := readHostRuntimeSSEFrame(reader)
	if err != nil || !strings.Contains(lateA, "id: 3\n") || !strings.Contains(lateA, "process.exited") {
		t.Fatalf("late predecessor after live head = %q, %v", lateA, err)
	}
}

func TestHostRuntimeFeedSSECursorAheadOrdersHeadGapAndReplay(t *testing.T) {
	s, openErr := store.New(context.Background(), filepath.Join(t.TempDir(), "runtime-rewind.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = s.Close(context.Background()) }()
	if generation, reserveErr := s.ReserveHostRuntimeRun(context.Background(), "session-rewind", "run-restored"); reserveErr != nil || generation != 1 {
		t.Fatalf("reserve restored run = %d, %v", generation, reserveErr)
	}
	if _, _, err := s.AppendHostRuntimeEvent(context.Background(), store.HostRuntimeEvent{
		SessionID: "session-rewind", RuntimeRunID: "run-restored", RuntimeGeneration: 1,
		SourceEventID: "restored-ready", SourceSequence: 1, Kind: "session.ready", OccurredAt: "2026-09-05T12:00:00Z",
		Source: store.HostRuntimeEventSource{Channel: "jsonrpc"}, Payload: json.RawMessage(`{}`), PayloadVisibility: "public_metadata",
	}, 10); err != nil {
		t.Fatal(err)
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sessions/session-rewind/runtime-events?after=99", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	headFrame, headErr := readHostRuntimeSSEFrame(reader)
	gapFrame, gapErr := readHostRuntimeSSEFrame(reader)
	eventFrame, eventErr := readHostRuntimeSSEFrame(reader)
	cancel()
	_ = response.Body.Close()
	if headErr != nil || strings.Contains(headFrame, "id:") || !strings.Contains(headFrame, "host_runtime.head.v1") || !strings.Contains(headFrame, `"latest_cursor":1`) {
		t.Fatalf("rewind head = %q, %v", headFrame, headErr)
	}
	if gapErr != nil || !strings.Contains(gapFrame, "id: 0\n") || !strings.Contains(gapFrame, "host_runtime.gap.v1") || !strings.Contains(gapFrame, `"requested_cursor":99`) {
		t.Fatalf("rewind gap = %q, %v", gapFrame, gapErr)
	}
	if eventErr != nil || !strings.Contains(eventFrame, "id: 1\n") || !strings.Contains(eventFrame, "session.ready") {
		t.Fatalf("rewind event = %q, %v", eventFrame, eventErr)
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

func readHostRuntimeSSEFrame(reader *bufio.Reader) (string, error) {
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		frame.WriteString(line)
		if line == "\n" {
			return frame.String(), nil
		}
	}
}
