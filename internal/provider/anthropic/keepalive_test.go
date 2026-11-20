package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/turn"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/keepalive"
)

// sseToolStream serves a tool call whose input arrives as parts input_json_delta
// frames, gap apart, after an optional silence before the first frame.
func sseToolStream(parts int, gap, silence time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(lines ...string) {
			for _, line := range lines {
				_, _ = io.WriteString(w, line+"\n")
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		write(`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_k","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250514","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":0}}}`, ``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_k","name":"write_doc","input":{}}}`, ``)
		time.Sleep(silence)
		write(`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\""}}`, ``)
		for range parts {
			time.Sleep(gap)
			write(`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"ab"}}`, ``)
		}
		write(`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"}"}}`, ``,
			`event: content_block_stop`, `data: {"type":"content_block_stop","index":0}`, ``,
			`event: message_delta`, `data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":5}}`, ``,
			`event: message_stop`, `data: {"type":"message_stop"}`, ``)
	}
}

func readAll(t *testing.T, c *Client, idle time.Duration) (tool *llmtypes.ToolUseBlock, keepalives int, err error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := c.StreamChat(ctx, llmtypes.ChatRequest{Model: "claude-sonnet-4-5-20250514",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "write"}},
		Tools:    []llmtypes.ToolDefinition{{Name: "write_doc", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := turn.NewReader(ctx, ch, idle)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for {
		ev, ok, err := reader.Next()
		if err != nil || !ok {
			return tool, keepalives, err
		}
		switch ev.Type { //nolint:exhaustive // Only the events under test.
		case keepalive.Event:
			if ev.Content != "" || ev.ToolUse != nil || ev.Usage != nil || ev.ThinkingBlock != nil {
				t.Fatalf("a keepalive carried content: %+v", ev)
			}
			keepalives++
		case llmtypes.EventToolUse:
			tu := *ev.ToolUse
			tool = &tu
		case llmtypes.EventDone:
			return tool, keepalives, nil
		}
	}
}

// A model writing a long tool input sends input_json_delta frames, which the
// stream keeps until the block ends. The stream says it is busy, so a turn's
// idle timer does not give up while the model is still writing.
func TestLongToolInputIsNotAStall(t *testing.T) {
	old := keepalive.SetEvery(20 * time.Millisecond)
	t.Cleanup(func() { keepalive.SetEvery(old) })

	c := newStreamingTestClient(t, sseToolStream(12, 25*time.Millisecond, 0))
	tool, keepalives, err := readAll(t, c, 120*time.Millisecond)
	if err != nil {
		t.Fatalf("a busy stream stalled: %v", err)
	}
	if tool == nil || tool.Input["text"] != strings.Repeat("ab", 12) {
		t.Fatalf("tool input = %+v", tool)
	}
	if keepalives == 0 {
		t.Fatal("no keepalive was sent while the tool input arrived")
	}
}

// A stream that receives no frames sends no keepalives, so it still stalls.
func TestSilentStreamStillStalls(t *testing.T) {
	old := keepalive.SetEvery(20 * time.Millisecond)
	t.Cleanup(func() { keepalive.SetEvery(old) })

	c := newStreamingTestClient(t, sseToolStream(0, 0, 400*time.Millisecond))
	_, _, err := readAll(t, c, 120*time.Millisecond)
	if !errors.Is(err, turn.ErrTurnStalled) {
		t.Fatalf("a silent stream did not stall: %v", err)
	}
}
