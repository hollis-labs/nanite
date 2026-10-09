package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/adapters/provider/events"
)

func TestAgentEventBridgeToolResultOutcomes(t *testing.T) {
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
			streams := NewStreamManager()
			turns := streams.CognitiveTurns()
			run := turns.create("view", "turn", "fixture", "model", chat.DeltaModeLive, "normal", func() (*store.Message, error) {
				return &store.Message{ID: "turn", SessionID: "view", Content: chat.WrapResponse("recovered", "default", nil, nil, false, false).MarshalContent()}, nil
			})
			producer := streams.createCognitiveStream("turn", "view", run)
			legacy, ok := streams.GetStream("turn")
			if !ok {
				t.Fatal("missing stream")
			}
			bridge := &agentEventBridge{streams: streams}
			bridge.typedCallback("view")(events.ToolResult{ID: "call", ContentPreview: tc.preview, IsError: tc.failed})
			producer <- chat.StreamEvent{Type: "stream_end"}
			close(producer)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var result chat.StreamEvent
			for {
				select {
				case event, open := <-legacy:
					if !open {
						goto finished
					}
					if event.Type == "tool_result" {
						result = event
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		finished:
			// Marshal/decode exercises the retained JSON discriminator, including omitempty.
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var decoded chat.StreamEvent
			if err = json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Type != "tool_result" || decoded.ToolID != "call" || decoded.IsError != tc.failed || decoded.Summary != tc.preview {
				t.Fatalf("lost tool outcome: %s", raw)
			}
			expectedError := ""
			if tc.failed {
				expectedError = tc.preview
			}
			if decoded.Error != expectedError {
				t.Fatalf("compatibility error=%q want=%q", decoded.Error, expectedError)
			}
			sub, err := turns.Subscribe(ctx, "view", "turn", 0)
			if err != nil {
				t.Fatal(err)
			}
			canonical := readCognitiveEvents(t, sub)
			message, err := chatstream.Reduce(canonical, nil)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
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
					t.Fatalf("canonical failure=%v want=%v", failed, tc.failed)
				}
			}
			if !found || canonical[len(canonical)-1].Verb != chatstream.VerbRunFinish {
				t.Fatalf("tool failure changed recovered turn outcome: %+v", canonical)
			}
		})
	}
}
