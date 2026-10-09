package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"
	"github.com/hollis-labs/libs/ui-go/chatstream/framing"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	agentservice "github.com/hollis-labs/substrate/agent/service"
)

// The service producer test exercises actual typed callbacks. This test covers
// both registered HTTP routes, their encoders, and shared client framing.
func TestToolResultHTTPWireOutcomes(t *testing.T) {
	a, mux := newTestAPI(t)
	streams := service.NewStreamManager()
	a.Services.Streams = streams
	session := &store.Session{}
	if err := a.store.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	for _, tc := range []struct {
		name, preview string
		failed        bool
	}{
		{"denied", "permission denied", true},
		{"failed", "execution failed", true},
		{"failed-empty-preview", "", true},
		{"success", "ok", false},
		{"success-error-looking-text", "error count: 0", false},
		{"success-empty-preview", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id := "wire-" + tc.name
			event := chat.StreamEvent{Type: "tool_result", Tool: "fixture", ToolID: "call", Summary: tc.preview, IsError: tc.failed}
			if tc.failed {
				event.Error = tc.preview
			}
			producer := streams.CreateStream(id, session.ID)
			replay, ok := streams.GetStream(id)
			if !ok {
				t.Fatal("legacy stream not found")
			}
			producer <- event
			producer <- chat.StreamEvent{Type: "stream_end"}
			close(producer)
			for {
				select {
				case _, open := <-replay:
					if !open {
						goto drained
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		drained:
			response, err := client.Get(server.URL + "/api/stream/" + id)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("legacy status %d", response.StatusCode)
			}
			var found bool
			for frame, readErr := range framing.SSE(response.Body) {
				if readErr != nil {
					t.Fatal(readErr)
				}
				if frame.Event != "tool_result" {
					continue
				}
				found = true
				var decoded chat.StreamEvent
				if err = json.Unmarshal(frame.Data, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.IsError != tc.failed || decoded.Summary != tc.preview || decoded.ToolID != "call" || decoded.Error != event.Error {
					t.Fatalf("legacy wire lost outcome: %s", frame.Data)
				}
			}
			if !found {
				t.Fatal("missing legacy result")
			}

			turns := streams.CognitiveTurns()
			run := turns.Create(session.ID, id, "fixture", "model", "live", "normal", func() (agentservice.Committed[store.Message], error) {
				content := "recovered"
				return agentservice.Committed[store.Message]{Message: &store.Message{ID: id, SessionID: session.ID}, Content: &content}, nil
			})
			for _, input := range []agentservice.Input{
				{Type: "tool_call", Tool: "fixture", ToolID: "call"},
				{Type: event.Type, Tool: event.Tool, ToolID: event.ToolID, Summary: event.Summary, Error: event.Error, IsError: event.IsError},
				{Type: "stream_end"},
			} {
				if err = run.Consume(input); err != nil {
					t.Fatal(err)
				}
			}
			if err = run.End(); err != nil {
				t.Fatal(err)
			}
			native, err := client.Get(server.URL + agentV1TurnRoutes(session.ID, id).Events)
			if err != nil {
				t.Fatal(err)
			}
			defer native.Body.Close()
			if native.StatusCode != http.StatusOK || native.Header.Get("X-Chat-Encoding") != "chatstream/v1" {
				t.Fatalf("native status=%d encoding=%q", native.StatusCode, native.Header.Get("X-Chat-Encoding"))
			}
			var canonical []chatstream.Event
			for frame, readErr := range framing.SSE(native.Body) {
				if readErr != nil {
					t.Fatal(readErr)
				}
				var decoded chatstream.Event
				if err = json.Unmarshal(frame.Data, &decoded); err != nil {
					t.Fatal(err)
				}
				canonical = append(canonical, decoded)
			}
			message, err := chatstream.Reduce(canonical, nil)
			if err != nil {
				t.Fatal(err)
			}
			found = false
			for _, part := range message.Parts {
				if part.Kind != chatstream.PartToolResult {
					continue
				}
				found = true
				var failed bool
				if err = json.Unmarshal(part.Meta[chatstream.MetaIsError], &failed); err != nil {
					t.Fatal(err)
				}
				if failed != tc.failed {
					t.Fatalf("canonical wire failure=%v want=%v", failed, tc.failed)
				}
			}
			if !found || len(canonical) == 0 || canonical[len(canonical)-1].Verb != chatstream.VerbRunFinish {
				t.Fatalf("tool result corrupted recovered turn: %+v", canonical)
			}
		})
	}
}
