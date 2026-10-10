package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
)

// Exercise the actual message endpoint with a broken connection. The HTTP
// reader must detach, while generation and replay remain owned by the service.
func TestChatSSETransportFailureRetainsReplay(t *testing.T) {
	for _, tc := range []struct {
		name             string
		writeAt, flushAt int
	}{
		{name: "opening flush", flushAt: 1},
		{name: "event write", writeAt: 1},
		{name: "event flush", flushAt: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streams := service.NewStreamManager()
			a := &API{Services: &service.Container{Streams: streams}}
			producer := streams.CreateStream("message", "session")
			initial, _, _ := streams.Subscribe("message", 0)
			producer <- chat.StreamEvent{Type: "delta", Content: "still generating"}
			<-initial // Synchronize with the pump before opening the failed reader.
			req := httptest.NewRequest(http.MethodGet, "/api/stream/message", nil)
			req.SetPathValue("messageID", "message")
			w := &failedSSEConnection{ResponseRecorder: httptest.NewRecorder(), writeAt: tc.writeAt, flushAt: tc.flushAt}
			returned := make(chan struct{})
			go func() { a.handleStream(w, req); close(returned) }()
			select {
			case <-returned:
			case <-time.After(2 * time.Second):
				close(producer)
				t.Fatal("handler kept reading after transport failed")
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			// The producer still owns its channel and can finish after HTTP detaches.
			producer <- chat.StreamEvent{Type: "stream_end"}
			close(producer)
			replay, _, ok := streams.Subscribe("message", 0)
			if !ok {
				t.Fatal("transport failure removed message stream")
			}
			var events []chat.StreamEvent
			for event := range replay {
				events = append(events, event)
			}
			if len(events) != 2 || events[0].Content != "still generating" || events[1].Type != "stream_end" {
				t.Fatalf("replay = %+v", events)
			}
		})
	}
}

type failedSSEConnection struct {
	*httptest.ResponseRecorder
	writeAt, flushAt int
	writes, flushes  int
}

func (w *failedSSEConnection) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.writeAt {
		return 0, errors.New("disconnected writer")
	}
	return w.ResponseRecorder.Write(p)
}
func (w *failedSSEConnection) FlushError() error {
	w.flushes++
	if w.flushes == w.flushAt {
		return errors.New("disconnected flush")
	}
	w.Flush()
	return nil
}

func TestChatSSEEnvelopeAndTerminalWire(t *testing.T) {
	streams := service.NewStreamManager()
	a := &API{Services: &service.Container{Streams: streams}}
	producer := streams.CreateStream("message", "session")
	initial, _, _ := streams.Subscribe("message", 0)
	events := []chat.StreamEvent{
		{Type: "plugin_envelope", PluginID: "test-plugin", Envelope: `{"type":"card","body":"line\nnext"}`},
		{Type: "stream_end"},
	}
	for _, event := range events {
		producer <- event
	}
	close(producer)
	for range initial {
	} // Completed replay includes the terminal.
	req := httptest.NewRequest(http.MethodGet, "/api/stream/message", nil)
	req.SetPathValue("messageID", "message")
	w := httptest.NewRecorder()
	a.handleStream(w, req)
	var want strings.Builder
	for i, event := range events {
		event.EventID = uint64(i + 1)
		if event.Type == "stream_end" {
			event.Termination = &chat.StreamTermination{Reason: "completed", Outcome: "success"}
		}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		want.WriteString("id: " + strconv.Itoa(i+1) + "\nevent: " + event.Type + "\ndata: " + string(data) + "\n\n")
	}
	if w.Body.String() != want.String() {
		t.Fatalf("wire = %q; want %q", w.Body.String(), want.String())
	}
	if w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("Connection") != "keep-alive" || w.Header().Get("X-Accel-Buffering") != "" {
		t.Fatalf("headers = %v", w.Header())
	}
}

// Use a real socket and a delegating middleware writer: recorder-only tests
// cannot detect a server deadline cutting an otherwise healthy stream.
func TestSSEPresenceSurvivesServerTimeouts(t *testing.T) {
	streams := service.NewStreamManager()
	a := &API{Services: &service.Container{Streams: streams}}
	streams.SetActivePresence("initial", chat.PresenceEvent{Type: "stream_start", SessionID: "initial"})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.handlePresenceStream(&sseMiddlewareWriter{ResponseWriter: w}, r)
	}))
	server.Config.ReadTimeout = 150 * time.Millisecond
	server.Config.WriteTimeout = 150 * time.Millisecond
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if _, initialErr := readHostRuntimeSSEFrame(reader); initialErr != nil {
		t.Fatalf("initial presence: %v", initialErr)
	}
	time.Sleep(350 * time.Millisecond)
	streams.BroadcastPresence(chat.PresenceEvent{Type: "stream_start", SessionID: "after-timeouts"})
	frame, err := readHostRuntimeSSEFrame(reader)
	if err != nil || !strings.Contains(frame, `"session_id":"after-timeouts"`) {
		t.Fatalf("presence after both server timeouts: frame=%q err=%v", frame, err)
	}
}

type sseMiddlewareWriter struct{ http.ResponseWriter }

func (w *sseMiddlewareWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *sseMiddlewareWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func TestSSEEndpointBufferingAndFailedWrite(t *testing.T) {
	a, mux, _ := newTestAPIWithSharedSurface(t)
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	a.SetPluginHost(host)
	feed := service.NewHostRuntimeFeed(a.store)
	a.Services.RuntimeFeed = feed
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = feed.Close(ctx)
	})
	response := workflowAPIRequest(t, mux, http.MethodPost, "/api/workflows/runs?engine=hadron&locator=pilot.workflow.yaml", hadronAPITestSource, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("create workflow: %d %s", response.Code, response.Body.String())
	}
	a.Services.Streams.SetActivePresence("active", chat.PresenceEvent{Type: "stream_start", SessionID: "active"})
	producer := a.Services.Streams.CreateStream("transport-message", "transport-session")
	initial, _, _ := a.Services.Streams.Subscribe("transport-message", 0)
	producer <- chat.StreamEvent{Type: "delta", Content: "first"}
	producer <- chat.StreamEvent{Type: "delta", Content: "second"}
	close(producer)
	for range initial {
	}
	for _, tc := range []struct {
		name, path, buffering string
		handler               http.HandlerFunc
	}{
		{"events", "/api/events", "no", a.handleUnifiedEvents},
		{"plugins events", "/api/plugins/events", "no", func(w http.ResponseWriter, r *http.Request) { handlePluginsEvents(w, r, host) }},
		{"host runtime feed", "/api/sessions/session/runtime-events", "no", a.handleHostRuntimeFeed},
		{"presence", "/api/presence", "", a.handlePresenceStream},
		{"workflows events", "/api/workflows/events?engine=hadron&after=0", "", a.handleWorkflowEvents},
		{"message stream", "/api/stream/transport-message", "", a.handleStream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx)
			req.SetPathValue("id", "session")
			req.SetPathValue("messageID", "transport-message")
			w := &failedSSEConnection{ResponseRecorder: httptest.NewRecorder(), writeAt: 1}
			returned := make(chan struct{})
			go func() { tc.handler(w, req); close(returned) }()
			select {
			case <-returned:
			case <-time.After(2 * time.Second):
				cancel()
				<-returned
				t.Fatal("handler kept reading after failed write")
			}
			if got := w.Header().Get("X-Accel-Buffering"); got != tc.buffering {
				t.Errorf("buffering = %q; want %q", got, tc.buffering)
			}
			if w.Code != http.StatusOK || w.writes != 1 {
				t.Fatalf("status=%d writes=%d; want 200 and exactly one failed write", w.Code, w.writes)
			}
		})
	}
}
