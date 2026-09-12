package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestSessionExposesActiveMessageForRecovery(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Model: "test-model", Provider: "test"}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	producer := a.Services.Streams.CreateStream("in-flight", sess.ID)
	defer close(producer)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID, nil))
	var response struct {
		MessageID string `json:"active_message_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.MessageID != "in-flight" {
		t.Fatalf("active message = %q; response %s", response.MessageID, w.Body.String())
	}
}

func TestChatReconnectAdvancesBeyondInitialURLCursor(t *testing.T) {
	streams := service.NewStreamManager()
	a := &API{Services: &service.Container{Streams: streams}}
	producer := streams.CreateStream("message", "session")
	initial, _, _ := streams.Subscribe("message", 0)
	producer <- chat.StreamEvent{Type: "delta", Content: "first"}
	producer <- chat.StreamEvent{Type: "delta", Content: "second"}
	producer <- chat.StreamEvent{Type: "delta", Content: "third"}
	close(producer)
	for range initial {
	}
	req := httptest.NewRequest(http.MethodGet, "/api/stream/message?from=1", nil)
	req.SetPathValue("messageID", "message")
	req.Header.Set("Last-Event-ID", "2")
	w := httptest.NewRecorder()
	a.handleStream(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"content":"third"`) || strings.Contains(w.Body.String(), `"content":"second"`) {
		t.Fatalf("unexpected reconnect response: %d %s", w.Code, w.Body.String())
	}
}

func TestChatStreamTakeoverIsNotTransportEOF(t *testing.T) {
	streams := service.NewStreamManager()
	a := &API{Services: &service.Container{Streams: streams}}
	producer := streams.CreateStream("message", "session")
	defer close(producer)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream/{messageID}", a.handleStream)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	first, err := client.Get(server.URL + "/api/stream/message")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	second, err := client.Get(server.URL + "/api/stream/message")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	body, err := io.ReadAll(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "event: session_takeover") {
		t.Fatalf("takeover became a reconnectable EOF: %q", body)
	}
}
