package deskpilot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// The crash point must not stall the chat/planner, must cancel promptly, and
// must allow recovery to reach the real provider again. This test is offline.
func TestRestartBarrierAndUsage(t *testing.T) {
	response := fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventDelta, Content: "answer"},
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 11, OutputTokens: 2, CostUSD: 0.001}},
		{Type: llmtypes.EventDone},
	}}
	p := &observed{Provider: fake.New(response, response), barrier: make(chan struct{})}
	req := llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{{Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", Content: "fixture contents"}}}}}
	if got, err := p.Complete(t.Context(), req); err != nil || got != "answer" {
		t.Fatalf("chat: %q %v", got, err)
	}
	select {
	case <-p.barrier:
		t.Fatal("chat triggered subtask barrier")
	default:
	}
	req.Tools = []llmtypes.ToolDefinition{{Name: "return_result"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := p.StreamChat(ctx, req); result <- err }()
	select {
	case <-p.barrier:
	case <-time.After(time.Second):
		t.Fatal("barrier did not engage")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("barrier did not cancel")
	}
	if got, err := p.Complete(t.Context(), req); err != nil || got != "answer" {
		t.Fatalf("recovery: %q %v", got, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls != 2 || p.missing != 0 || p.usage.InputTokens != 22 || p.usage.OutputTokens != 4 || p.usage.CostUSD != 0.002 {
		t.Fatalf("usage: calls=%d missing=%d %+v", p.calls, p.missing, p.usage)
	}
}
