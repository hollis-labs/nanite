package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
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
