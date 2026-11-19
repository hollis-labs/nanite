package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/failure"
)

func TestCompleteEmptyOutputIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"no choices":       `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[]}`,
		"empty content":    `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
		"whitespace":       `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":" \n"},"finish_reason":"stop"}]}`,
		"responses: empty": `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}]}`,
		"responses: none":  `{"id":"resp_1","status":"completed","output":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			})
			ctx := context.Background()
			model := "gpt-4o"
			if len(name) > 10 && name[:10] == "responses:" {
				// A reasoning model with a budget routes to the Responses API.
				ctx, model = reasoningContext(8000), "gpt-5.6"
			}
			got, err := c.Complete(ctx, llmtypes.ChatRequest{Model: model, Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
			if !errors.Is(err, failure.ErrEmptyResponse) || got != "" {
				t.Fatalf("Complete = %q, %v; want ErrEmptyResponse", got, err)
			}
		})
	}
}

func TestStreamChatEmptyStreamEndsCleanly(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	stream, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{Model: "gpt-4o", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyStream(t, stream)
}

// assertEmptyStream checks the streaming half of the empty-output contract:
// no text, no tool calls, no error, and a final EventDone.
func assertEmptyStream(t *testing.T, stream <-chan llmtypes.StreamEvent) {
	t.Helper()
	var last llmtypes.EventType
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				if last != llmtypes.EventDone {
					t.Fatalf("stream closed after %q, want %q", last, llmtypes.EventDone)
				}
				return
			}
			switch ev.Type {
			case llmtypes.EventError, llmtypes.EventToolUse:
				t.Fatalf("unexpected %s event: %+v", ev.Type, ev)
			case llmtypes.EventDelta:
				if ev.Content != "" {
					t.Fatalf("unexpected text %q", ev.Content)
				}
			case llmtypes.EventUsage, llmtypes.EventDone, llmtypes.EventSessionID, llmtypes.EventThinking:
			}
			last = ev.Type
		case <-timeout:
			t.Fatal("stream did not close")
		}
	}
}
