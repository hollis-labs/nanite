package fake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

func TestScriptedResponses(t *testing.T) {
	fixture := errors.New("fixture error")
	for _, tc := range []struct {
		name          string
		response      Response
		text, failure string
	}{
		{"text", Text("hello"), "hello", ""},
		{"startup error", Response{Err: fixture}, "", "fixture error"},
		{"stream error", Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: "failed"}}}, "", "failed"},
		{"truncated", Response{}, "", "without terminal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.response)
			got, err := p.Complete(context.Background(), llmtypes.ChatRequest{})
			if got != tc.text || (tc.failure == "" && err != nil) || (tc.failure != "" && (err == nil || !strings.Contains(err.Error(), tc.failure))) {
				t.Fatalf("Complete = %q, %v", got, err)
			}
			if _, err := p.StreamChat(context.Background(), llmtypes.ChatRequest{}); err == nil {
				t.Fatal("script exhausted without an error")
			}
		})
	}
}

func TestToolCallAndStallCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := New(Response{Stall: true, Events: []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "t1", Name: "lookup"}}}})
	stream, err := p.StreamChat(ctx, llmtypes.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	event := <-stream
	if event.ToolUse == nil || event.ToolUse.Name != "lookup" {
		t.Fatalf("tool event: %+v", event)
	}
	cancel()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("expected cancelled stream to close")
		}
	case <-time.After(time.Second):
		t.Fatal("stream stayed open after cancellation")
	}
}
