package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/turn"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/keepalive"
)

// drain reads a stream through a turn reader with the given idle timeout, as
// a turn does, and reports the tool call and keepalive count.
func drain(t *testing.T, c *Client, model string, idle time.Duration) (*llmtypes.ToolUseBlock, int, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(keepalive.WithIdle(context.Background(), idle))
	defer cancel()
	ch, err := c.StreamChat(ctx, toolReq(model))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := turn.NewReader(ctx, ch, idle)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var tool *llmtypes.ToolUseBlock
	keepalives := 0
	for {
		ev, ok, err := reader.Next()
		if err != nil || !ok {
			return tool, keepalives, err
		}
		switch ev.Type { //nolint:exhaustive // Only the events under test.
		case keepalive.Event:
			if ev.Content != "" || ev.ToolUse != nil || ev.Usage != nil {
				t.Fatalf("a keepalive carried content: %+v", ev)
			}
			keepalives++
		case llmtypes.EventToolUse:
			tu := *ev.ToolUse
			tool = &tu
		case llmtypes.EventError:
			t.Fatal(ev.Error)
		case llmtypes.EventDone:
			return tool, keepalives, nil
		}
	}
}

// Chat completions stream a tool call's arguments in pieces and keep them
// until the end; the stream says it is busy meanwhile. The keepalive
// interval comes from the reader's idle timeout (a third of it), not only
// the 5 second default.
func TestChatToolArgumentsAreNotAStall(t *testing.T) {
	const parts = 24
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = io.WriteString(w, "data: "+s+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","function":{"name":"noop","arguments":"{\"text\":\""}}]},"finish_reason":""}]}`)
		for range parts {
			time.Sleep(25 * time.Millisecond)
			write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ab"}}]},"finish_reason":""}]}`)
		}
		write(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]},"finish_reason":"tool_calls"}]}`)
		write(`[DONE]`)
	})
	tool, keepalives, err := drain(t, c, "gpt-4o", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("a busy stream stalled: %v", err)
	}
	if tool == nil || tool.Input["text"] != strings.Repeat("ab", parts) {
		t.Fatalf("tool = %+v", tool)
	}
	if keepalives == 0 {
		t.Fatal("no keepalive while the arguments arrived")
	}
}

// The Responses API streams function_call_arguments.delta frames and the
// stream passes nothing on until the call is done; it says it is busy.
func TestResponsesFunctionArgumentsAreNotAStall(t *testing.T) {
	const parts = 24
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeResponseEvent(w, "response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"text\":\""}`)
		for range parts {
			time.Sleep(25 * time.Millisecond)
			writeResponseEvent(w, "response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"ab"}`)
		}
		args := fmt.Sprintf(`{\"text\":\"%s\"}`, strings.Repeat("ab", parts))
		writeResponseEvent(w, "response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"`+args+`"}`)
		writeResponseEvent(w, "response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"noop","arguments":"`+args+`","status":"completed"}],"usage":{"input_tokens":5,"output_tokens":5}}}`)
	})
	tool, keepalives, err := drain(t, c, "gpt-6-astra", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("a busy stream stalled: %v", err)
	}
	if tool == nil || tool.Input["text"] != strings.Repeat("ab", parts) {
		t.Fatalf("tool = %+v", tool)
	}
	if keepalives == 0 {
		t.Fatal("no keepalive while the arguments arrived")
	}
}
