package fake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/provider/failure"

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

func TestScriptedFailures(t *testing.T) {
	p := New(Retryable(3*time.Second), FailAfter(failure.Retryable, llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "par"}), Truncated("cut"))

	_, err := p.StreamChat(t.Context(), llmtypes.ChatRequest{})
	var classified *failure.Error
	if !errors.As(err, &classified) || classified.Class != failure.Retryable || classified.RetryAfter != 3*time.Second {
		t.Fatalf("Retryable: %v", err)
	}

	stream, err := p.StreamChat(t.Context(), llmtypes.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var events []llmtypes.StreamEvent
	for ev := range stream {
		events = append(events, ev)
	}
	if len(events) != 2 || events[0].Content != "par" || events[1].Type != llmtypes.EventError {
		t.Fatalf("FailAfter: %+v", events)
	}
	if err := failure.FromEvent(events[1]); !errors.As(err, &classified) || classified.Class != failure.Retryable {
		t.Fatalf("FailAfter error: %v", err)
	}

	stream, err = p.StreamChat(t.Context(), llmtypes.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	for ev := range stream {
		events = append(events, ev)
	}
	if len(events) != 1 || events[0].Content != "cut" {
		t.Fatalf("Truncated: %+v", events)
	}
}
