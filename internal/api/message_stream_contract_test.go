package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
)

// These assertions read the HTTP wire independently of the producer DTO, so
// observer notices cannot silently inherit a run event ID or success outcome.
type messageContractFrame struct {
	ID      string
	Event   string
	Comment string
	Data    map[string]json.RawMessage
}

func readMessageContractFrame(reader *bufio.Reader) (messageContractFrame, error) {
	var frame messageContractFrame
	var data []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return frame, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if len(data) != 0 {
				if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &frame.Data); err != nil {
					return frame, fmt.Errorf("invalid stream JSON: %w", err)
				}
			}
			return frame, nil
		}
		if strings.HasPrefix(line, ":") {
			frame.Comment += strings.TrimSpace(strings.TrimPrefix(line, ":"))
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			frame.ID = value
		case "event":
			frame.Event = value
		case "data":
			data = append(data, value)
		}
	}
}

func messageContractFrames(t *testing.T, body io.Reader) []messageContractFrame {
	t.Helper()
	reader := bufio.NewReader(body)
	var frames []messageContractFrame
	for {
		frame, err := readMessageContractFrame(reader)
		if errors.Is(err, io.EOF) {
			return frames
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
}

func messageContractString(t *testing.T, data map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(data[key], &value); err != nil {
		t.Fatalf("%s: %v; data=%s", key, err, data[key])
	}
	return value
}

func messageContractID(t *testing.T, frame messageContractFrame) uint64 {
	t.Helper()
	id, err := strconv.ParseUint(frame.ID, 10, 64)
	if err != nil || id == 0 {
		t.Fatalf("producer frame lacks ID: %+v (%v)", frame, err)
	}
	var jsonID uint64
	if err := json.Unmarshal(frame.Data["event_id"], &jsonID); err != nil || jsonID != id {
		t.Fatalf("SSE/JSON ID mismatch: %+v (%v)", frame, err)
	}
	return id
}

func messageContractAPI(streams *service.StreamManager) (*API, *http.ServeMux) {
	a := &API{Services: &service.Container{Streams: streams}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream/{messageID}", a.handleStream)
	return a, mux
}

func messageContractReplay(t *testing.T, mux *http.ServeMux, path, lastID string) []messageContractFrame {
	t.Helper()
	server := httptest.NewServer(mux)
	defer server.Close()
	response := messageContractGet(t, &http.Client{Timeout: 3 * time.Second}, server.URL+path, lastID)
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream content type=%q", response.Header.Get("Content-Type"))
	}
	return messageContractFrames(t, response.Body)
}

func TestMessageStreamContractProducerCompletion(t *testing.T) {
	for _, test := range []struct {
		name, outcome, reason string
		explicitEnd           bool
	}{
		{name: "explicit success", outcome: "success", explicitEnd: true},
		{name: "unexplained EOF", outcome: "unknown", reason: "producer_closed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			streams := service.NewStreamManager()
			_, mux := messageContractAPI(streams)
			producer := streams.CreateStream("completion", "session")
			observer, _, ok := streams.Subscribe("completion", 0)
			if !ok {
				t.Fatal("missing stream")
			}
			producer <- chat.StreamEvent{Type: "delta", Content: "retained reply"}
			if test.explicitEnd {
				producer <- chat.StreamEvent{Type: "stream_end"}
			}
			close(producer)
			for range observer {
			}
			frames := messageContractReplay(t, mux, "/api/stream/completion", "")
			var terminals int
			var reply bool
			for _, frame := range frames {
				if frame.Event == "delta" {
					reply = messageContractString(t, frame.Data, "content") == "retained reply"
					messageContractID(t, frame)
				}
				if frame.Event != "stream_end" {
					continue
				}
				terminals++
				var termination map[string]json.RawMessage
				if err := json.Unmarshal(frame.Data["termination"], &termination); err != nil {
					t.Fatalf("missing explicit terminal outcome: %+v (%v)", frame, err)
				}
				if outcome := messageContractString(t, termination, "outcome"); outcome != test.outcome {
					t.Fatalf("outcome=%q want %q", outcome, test.outcome)
				}
				if test.reason != "" && messageContractString(t, termination, "reason") != test.reason {
					t.Fatalf("unexplained EOF lost its reason: %+v", frame)
				}
				var retryable bool
				if err := json.Unmarshal(termination["retryable"], &retryable); err != nil || retryable {
					t.Fatalf("run terminal incorrectly requests transport retry: %+v (%v)", frame, err)
				}
			}
			if !reply || terminals != 1 {
				t.Fatalf("reply=%v terminal count=%d frames=%+v", reply, terminals, frames)
			}
		})
	}
}

func assertMessageObserverNotice(t *testing.T, frame messageContractFrame) {
	t.Helper()
	if frame.ID != "" || frame.Data["event_id"] != nil {
		t.Fatalf("observer notice advanced producer cursor: %+v", frame)
	}
}

func TestMessageStreamContractReplayGap(t *testing.T) {
	streams := service.NewStreamManager()
	_, mux := messageContractAPI(streams)
	producer := streams.CreateStream("retention", "session")
	// More events than retention and transport headroom. Sending this burst
	// before subscribing exercises the production pump without a test-only
	// capacity override or a socket timing assumption.
	const count = 1024
	for i := 0; i < count; i++ {
		producer <- chat.StreamEvent{Type: "delta", Content: strconv.Itoa(i)}
	}
	producer <- chat.StreamEvent{Type: "stream_end"}
	close(producer)
	completion, _, ok := streams.Subscribe("retention", count)
	if !ok {
		t.Fatal("missing stream")
	}
	for range completion {
	}
	frames := messageContractReplay(t, mux, "/api/stream/retention?from=1", "2")
	if len(frames) < 3 || frames[0].Event != "gap" {
		t.Fatalf("retention loss was silent: %+v", frames)
	}
	assertMessageObserverNotice(t, frames[0])
	var gap struct {
		From, To uint64
		Reason   string
	}
	if err := json.Unmarshal(frames[0].Data["gap"], &gap); err != nil {
		t.Fatal(err)
	}
	firstID := messageContractID(t, frames[1])
	if gap.Reason != "retention" || gap.From != 3 || gap.To+1 != firstID {
		t.Fatalf("gap=%+v first replay=%d", gap, firstID)
	}
	last := firstID - 1
	for _, frame := range frames[1:] {
		id := messageContractID(t, frame)
		if id != last+1 {
			t.Fatalf("replay omitted/repeated event: last=%d frame=%+v", last, frame)
		}
		last = id
	}
	if last != count+1 || frames[len(frames)-1].Event != "stream_end" {
		t.Fatalf("replay failed to retain producer completion: %+v", frames)
	}
	// A client claiming a cursor beyond this run cannot establish success or
	// silently clamp that claim to the newest retained ID.
	ahead := messageContractReplay(t, mux, "/api/stream/retention?from=2048", "")
	if len(ahead) != 2 || ahead[0].Event != "gap" || ahead[1].Event != "stream_end" {
		t.Fatalf("ahead cursor response=%+v", ahead)
	}
	assertMessageObserverNotice(t, ahead[0])
	if err := json.Unmarshal(ahead[0].Data["gap"], &gap); err != nil {
		t.Fatal(err)
	}
	if gap.Reason != "cursor_ahead" || gap.From != count+2 || gap.To != 2048 {
		t.Fatalf("ahead gap=%+v", gap)
	}
	var end map[string]json.RawMessage
	if err := json.Unmarshal(ahead[1].Data["termination"], &end); err != nil {
		t.Fatal(err)
	}
	if messageContractString(t, end, "outcome") != "unknown" {
		t.Fatalf("cursor-ahead EOF invented a run outcome: %+v", ahead[1])
	}
}

func TestMessageStreamContractProducerFailureWire(t *testing.T) {
	for _, test := range []struct {
		name, reason, outcome string
		fallback              bool
	}{
		{"error before EOF", "producer_closed", "error", false},
		{"generation failure", "generation_failed", "error", true},
		{"generation canceled", "canceled", "canceled", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			streams := service.NewStreamManager()
			_, mux := messageContractAPI(streams)
			producer := streams.CreateStream("failed", "session")
			observer, _, _ := streams.Subscribe("failed", 0)
			producer <- chat.StreamEvent{Type: "status", Content: "working"}
			producer <- chat.StreamEvent{Type: "error", Error: "original provider failure"}
			if test.fallback {
				// Generator behavior is tested at its service-owned boundary.
				// Here its actual DTO is transported through the production pump
				// and HTTP encoder, including the internal fallback marker.
				producer <- chat.StreamEvent{Type: "stream_end", MessageID: "failed", Termination: &chat.StreamTermination{Reason: test.reason, Outcome: test.outcome}, RetainedTerminal: true}
			}
			close(producer)
			for range observer {
			}
			frames := messageContractReplay(t, mux, "/api/stream/failed", "")
			if len(frames) != 3 || frames[0].Event != "status" || frames[1].Event != "error" || frames[2].Event != "stream_end" {
				t.Fatalf("lost status/error/terminal: %+v", frames)
			}
			if messageContractString(t, frames[0].Data, "content") != "working" || messageContractString(t, frames[1].Data, "error") != "original provider failure" {
				t.Fatalf("original producer diagnostics changed: %+v", frames)
			}
			var termination map[string]json.RawMessage
			if err := json.Unmarshal(frames[2].Data["termination"], &termination); err != nil {
				t.Fatal(err)
			}
			if messageContractString(t, termination, "reason") != test.reason || messageContractString(t, termination, "outcome") != test.outcome {
				t.Fatalf("failure became success: %+v", frames[2])
			}
			for _, frame := range frames {
				for key := range frame.Data {
					if strings.EqualFold(key, "retainedterminal") || key == "retained_terminal" {
						t.Fatalf("internal fallback marker leaked: %+v", frame)
					}
				}
			}
		})
	}
}

// Block an actual HTTP writer, rather than filling a fake subscriber channel.
// The pump must keep producing while the observer is unable to write bytes.
type messageContractBlockedWriter struct {
	http.ResponseWriter
	ctx     context.Context
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (w *messageContractBlockedWriter) Write(data []byte) (int, error) {
	var wait bool
	w.once.Do(func() {
		wait = true
		close(w.entered)
	})
	if wait {
		select {
		case <-w.release:
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		}
	}
	return w.ResponseWriter.Write(data)
}
func (w *messageContractBlockedWriter) Flush() {
	_ = w.FlushError()
}
func (w *messageContractBlockedWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *messageContractBlockedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func awaitMessageContract(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not reach synchronized observer boundary")
	}
}

func messageContractGet(t *testing.T, client *http.Client, address, lastID string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastID != "" {
		request.Header.Set("Last-Event-ID", lastID)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestMessageStreamContractSlowReaderResume(t *testing.T) {
	streams := service.NewStreamManager()
	a, _ := messageContractAPI(streams)
	producer := streams.CreateStream("slow", "same-run-session")
	var producerClosed bool
	defer func() {
		if !producerClosed {
			close(producer)
		}
	}()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var first atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream/{messageID}", func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			w = &messageContractBlockedWriter{ResponseWriter: w, ctx: r.Context(), entered: entered, release: release}
		}
		a.handleStream(w, r)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	response := messageContractGet(t, client, server.URL+"/api/stream/slow", "")
	producer <- chat.StreamEvent{Type: "delta", Content: "first"}
	awaitMessageContract(t, entered)
	const burst = 1024
	for i := 1; i < burst; i++ {
		producer <- chat.StreamEvent{Type: "delta", Content: strconv.Itoa(i)}
	}
	// The HTTP observer is blocked throughout the burst; channel headroom
	// cannot absorb it, and generation is still active when it disconnects.
	unblock()
	frames := messageContractFrames(t, response.Body)
	var lastID uint64
	var closure *messageContractFrame
	for i := range frames {
		frame := frames[i]
		switch frame.Event {
		case "delta":
			id := messageContractID(t, frame)
			if id != lastID+1 {
				t.Fatalf("queued frames were dropped before closure: last=%d frame=%+v", lastID, frame)
			}
			lastID = id
		case "stream_closed":
			closure = &frames[i]
		default:
			t.Fatalf("slow observer claimed producer completion: %+v", frame)
		}
	}
	if closure == nil || lastID < 2 || lastID >= burst || frames[len(frames)-1].Event != "stream_closed" {
		t.Fatalf("slow-reader closure/order missing: last=%d frames=%+v", lastID, frames)
	}
	assertMessageObserverNotice(t, *closure)
	var end struct {
		Reason      string `json:"reason"`
		Outcome     string `json:"outcome"`
		Retryable   bool   `json:"retryable"`
		ResumeAfter uint64 `json:"resume_after"`
	}
	if err := json.Unmarshal(closure.Data["termination"], &end); err != nil {
		t.Fatal(err)
	}
	if end.Reason != "slow_consumer" || !end.Retryable || end.Outcome != "" || end.ResumeAfter != lastID {
		t.Fatalf("observer closure confused queued/pump IDs with written IDs: %+v last written=%d", end, lastID)
	}
	var closureFields map[string]json.RawMessage
	if err := json.Unmarshal(closure.Data["termination"], &closureFields); err != nil {
		t.Fatal(err)
	}
	if closureFields["outcome"] != nil {
		t.Fatalf("observer closure exposed a run outcome: %+v", *closure)
	}
	// Continue the same producer after its reader has closed, then reconnect
	// to the same message. No new generation or durable-completion claim.
	producer <- chat.StreamEvent{Type: "delta", Content: "after observer closure"}
	producer <- chat.StreamEvent{Type: "stream_end"}
	close(producer)
	producerClosed = true
	completion, _, _ := streams.Subscribe("slow", burst)
	for range completion {
	}
	resumed := messageContractGet(t, client, server.URL+"/api/stream/slow", strconv.FormatUint(lastID, 10))
	replay := messageContractFrames(t, resumed.Body)
	if len(replay) < 3 || replay[0].Event != "gap" {
		t.Fatalf("resume silently lost retention: %+v", replay)
	}
	assertMessageObserverNotice(t, replay[0])
	var gap struct {
		From, To uint64
		Reason   string
	}
	if err := json.Unmarshal(replay[0].Data["gap"], &gap); err != nil {
		t.Fatal(err)
	}
	firstID := messageContractID(t, replay[1])
	if gap.Reason != "retention" || gap.From != lastID+1 || gap.To+1 != firstID {
		t.Fatalf("resume gap=%+v written=%d first replay=%d", gap, lastID, firstID)
	}
	var continued bool
	previous := firstID - 1
	for _, frame := range replay[1:] {
		id := messageContractID(t, frame)
		if id != previous+1 {
			t.Fatalf("noncontiguous resume: previous=%d frame=%+v", previous, frame)
		}
		previous = id
		if frame.Event == "delta" && messageContractString(t, frame.Data, "content") == "after observer closure" {
			continued = true
		}
	}
	if !continued || previous != burst+2 || replay[len(replay)-1].Event != "stream_end" {
		t.Fatalf("same-run continuation lost: %+v", replay)
	}
	if session, ok := streams.GetSessionForMessage("slow"); !ok || session != "same-run-session" {
		t.Fatalf("resume retargeted run: %q,%v", session, ok)
	}
}

func TestMessageStreamContractHeartbeat(t *testing.T) {
	streams := service.NewStreamManager()
	a, _ := messageContractAPI(streams)
	producer := streams.CreateStream("idle", "session")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream/{messageID}", func(w http.ResponseWriter, r *http.Request) {
		a.streamMessageEventsWithKeepalive(w, r, r.PathValue("messageID"), "", 2*time.Millisecond)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	response := messageContractGet(t, &http.Client{Timeout: 3 * time.Second}, server.URL+"/api/stream/idle", "")
	reader := bufio.NewReader(response.Body)
	frame, err := readMessageContractFrame(reader)
	if err != nil || frame.Comment != "keepalive" || frame.Event != "" || frame.Data != nil {
		close(producer)
		t.Fatalf("idle heartbeat is not a comment: %+v,%v", frame, err)
	}
	assertMessageObserverNotice(t, frame)
	producer <- chat.StreamEvent{Type: "delta", Content: "after idle"}
	producer <- chat.StreamEvent{Type: "stream_end"}
	close(producer)
	var lastID uint64
	for {
		frame, err = readMessageContractFrame(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if frame.Comment != "" {
			assertMessageObserverNotice(t, frame)
			continue
		}
		id := messageContractID(t, frame)
		if id != lastID+1 {
			t.Fatalf("heartbeat advanced run cursor: previous=%d frame=%+v", lastID, frame)
		}
		lastID = id
	}
	if lastID != 2 {
		t.Fatalf("idle stream lost numbered producer frames: last=%d", lastID)
	}
}

func TestMessageStreamContractTakeover(t *testing.T) {
	streams := service.NewStreamManager()
	a, _ := messageContractAPI(streams)
	producer := streams.CreateStream("takeover", "session")
	var closed bool
	defer func() {
		if !closed {
			close(producer)
		}
	}()
	firstReturned := make(chan struct{})
	var connections atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream/{messageID}", func(w http.ResponseWriter, r *http.Request) {
		if connections.Add(1) == 1 {
			defer close(firstReturned)
		}
		a.handleStream(w, r)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	first := messageContractGet(t, client, server.URL+"/api/stream/takeover?from=50", "")
	firstReader := bufio.NewReader(first.Body)
	gap, err := readMessageContractFrame(firstReader)
	if err != nil || gap.Event != "gap" {
		t.Fatalf("ahead cursor notice=%+v,%v", gap, err)
	}
	assertMessageObserverNotice(t, gap)
	second := messageContractGet(t, client, server.URL+"/api/stream/takeover", "")
	frames := messageContractFrames(t, firstReader)
	awaitMessageContract(t, firstReturned)
	if len(frames) != 2 || frames[0].Event != "session_takeover" || frames[1].Event != "stream_closed" {
		t.Fatalf("takeover appeared as ordinary EOF: %+v", frames)
	}
	for _, frame := range frames {
		assertMessageObserverNotice(t, frame)
	}
	var end map[string]json.RawMessage
	if err := json.Unmarshal(frames[1].Data["termination"], &end); err != nil {
		t.Fatal(err)
	}
	var retryable bool
	if err := json.Unmarshal(end["retryable"], &retryable); err != nil || retryable || end["outcome"] != nil || end["resume_after"] != nil || messageContractString(t, end, "reason") != "session_takeover" {
		t.Fatalf("takeover requests retry or claims run outcome: %+v (%v)", frames[1], err)
	}
	// Old observer cleanup must detach exactly itself, preserving the newly
	// installed connection and the producer it shares with the first one.
	producer <- chat.StreamEvent{Type: "delta", Content: "new observer still attached"}
	producer <- chat.StreamEvent{Type: "stream_end"}
	close(producer)
	closed = true
	current := messageContractFrames(t, second.Body)
	if len(current) != 2 || current[0].Event != "delta" || current[1].Event != "stream_end" || messageContractString(t, current[0].Data, "content") != "new observer still attached" {
		t.Fatalf("old cleanup detached new observer: %+v", current)
	}
	if messageContractID(t, current[0]) != 1 || messageContractID(t, current[1]) != 2 {
		t.Fatalf("takeover changed producer IDs: %+v", current)
	}
}

type messageContractFailedWriter struct{ http.ResponseWriter }

func (w *messageContractFailedWriter) Write([]byte) (int, error) {
	return 0, errors.New("contract observer write failure")
}
func (w *messageContractFailedWriter) Flush() { _ = w.FlushError() }
func (w *messageContractFailedWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *messageContractFailedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestMessageStreamContractObserverDetach(t *testing.T) {
	for _, failure := range []string{"request cancellation", "write failure"} {
		t.Run(failure, func(t *testing.T) {
			streams := service.NewStreamManager()
			a, _ := messageContractAPI(streams)
			producer := streams.CreateStream("detach", "session")
			var closed bool
			defer func() {
				if !closed {
					close(producer)
				}
			}()
			returned := make(chan struct{})
			var first atomic.Bool
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/stream/{messageID}", func(w http.ResponseWriter, r *http.Request) {
				if first.CompareAndSwap(false, true) {
					defer close(returned)
					if failure == "write failure" {
						w = &messageContractFailedWriter{ResponseWriter: w}
					}
				}
				a.handleStream(w, r)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			client := &http.Client{Timeout: 3 * time.Second}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/stream/detach", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			producer <- chat.StreamEvent{Type: "delta", Content: "before detach"}
			if failure == "request cancellation" {
				frame, readErr := readMessageContractFrame(bufio.NewReader(response.Body))
				if readErr != nil || frame.Event != "delta" || messageContractID(t, frame) != 1 {
					t.Fatalf("pre-cancellation frame=%+v,%v", frame, readErr)
				}
				cancel()
			}
			awaitMessageContract(t, returned)
			// There is no promise of a notice on a dead socket. The concrete
			// guarantee is that observer failure cannot cancel execution or
			// remove events from the service-owned continuation/replay.
			producer <- chat.StreamEvent{Type: "delta", Content: "after detach"}
			producer <- chat.StreamEvent{Type: "stream_end"}
			close(producer)
			closed = true
			completion, _, _ := streams.Subscribe("detach", 0)
			for range completion {
			}
			replayResponse := messageContractGet(t, client, server.URL+"/api/stream/detach", "")
			replay := messageContractFrames(t, replayResponse.Body)
			if len(replay) != 3 || replay[0].Event != "delta" || replay[1].Event != "delta" || replay[2].Event != "stream_end" || messageContractString(t, replay[1].Data, "content") != "after detach" {
				t.Fatalf("observer failure lost continuing producer: %+v", replay)
			}
			for i, frame := range replay {
				if messageContractID(t, frame) != uint64(i+1) {
					t.Fatalf("observer failure changed retained IDs: %+v", replay)
				}
			}
		})
	}
}
