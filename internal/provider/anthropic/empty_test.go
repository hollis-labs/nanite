package anthropic

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
	for name, content := range map[string]string{
		"no blocks":  `[]`,
		"tool only":  `[{"type":"tool_use","id":"t1","name":"x","input":{}}]`,
		"whitespace": `[{"type":"text","text":"  \n"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			c := newStreamingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0},"content":`+content+`}`)
			})
			got, err := c.Complete(context.Background(), llmtypes.ChatRequest{Model: "claude-sonnet-5-5", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
			if !errors.Is(err, failure.ErrEmptyResponse) || got != "" {
				t.Fatalf("Complete = %q, %v; want ErrEmptyResponse", got, err)
			}
		})
	}
}

func TestStreamChatEmptyStreamEndsCleanly(t *testing.T) {
	c := newStreamingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []struct{ name, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			_, _ = io.WriteString(w, "event: "+event.name+"\ndata: "+event.data+"\n\n")
		}
	})
	stream, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{Model: "claude-sonnet-5-5", Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
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
