package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeRequestToolsService satisfies ToolService for the reflection unit test.
// HandleRequestTools always returns no new tools so the cap-trip path fires.
type fakeRequestToolsService struct {
	stubToolService // embed the existing test stub from chat_test.go
	logged          []store.BrokerDecisionEntry
}

// GetToolSchema satisfies the ToolService interface — stubToolService doesn't
// implement it, so we add the no-op here to keep the fake compilable.
func (f *fakeRequestToolsService) GetToolSchema(_ string) map[string]any { return nil }

// LogRequestToolsCall captures the persistence calls so the test can assert
// the right outcome strings reach the store.
func (f *fakeRequestToolsService) LogRequestToolsCall(
	sessionID, intent, outcome string,
	consecutiveEmpty, totalCalls, loadedCount int,
	reflectionQuery string,
) {
	f.logged = append(f.logged, store.BrokerDecisionEntry{
		SessionID:        sessionID,
		Intent:           intent,
		LayerReached:     "request_tools",
		Outcome:          outcome,
		ConsecutiveEmpty: consecutiveEmpty,
		TotalCalls:       totalCalls,
		LoadedCount:      loadedCount,
		ReflectionQuery:  reflectionQuery,
	})
}

func TestHandleRequestTools_ReflectionThenHalt(t *testing.T) {
	fake := &fakeRequestToolsService{}
	s := &chatServiceImpl{tools: fake}

	loadedTools := map[string]bool{
		"dev_read":      true,
		"dev_grep":      true,
		"clockwork_get": true,
	}
	consecutiveEmpty := 0
	totalCalls := 5 // already past maxCalls=3 below
	maxCalls := 3
	reflectionFired := false
	sessionID := "s1"

	// Buffered channel sized to absorb the events the helper emits per call:
	// one tool_call + one tool_result per request_tools invocation.
	ch := make(chan chat.StreamEvent, 8)

	tu := provider.ToolUseBlock{
		ID:   "call_1",
		Name: "request_tools",
		Input: map[string]any{
			"intent": "find a way to build the project",
		},
	}

	// First call → cap is tripped (totalCalls=5+1>3) and reflectionFired
	// is false → reflection prompt.
	resultBlocks, refs := s.handleRequestTools(
		context.Background(), tu, ch, nil, loadedTools,
		&consecutiveEmpty, &totalCalls, maxCalls,
		nil, nil,
		sessionID, &reflectionFired,
	)
	if len(resultBlocks) != 1 || len(refs) != 1 {
		t.Fatalf("expected 1 result block + ref; got %d / %d", len(resultBlocks), len(refs))
	}
	if !reflectionFired {
		t.Errorf("reflectionFired should be true after first cap-trip")
	}
	if !strings.Contains(resultBlocks[0].Content, "REFLECT") {
		t.Errorf("reflection prompt should ask the LLM to REFLECT; got %q", resultBlocks[0].Content)
	}
	if !strings.Contains(resultBlocks[0].Content, "Currently loaded") {
		t.Errorf("reflection prompt should list currently loaded tools; got %q", resultBlocks[0].Content)
	}
	if !strings.Contains(resultBlocks[0].Content, "dev_grep") {
		t.Errorf("reflection prompt should mention loaded dev_grep; got %q", resultBlocks[0].Content)
	}
	if len(fake.logged) != 1 || fake.logged[0].Outcome != "reflected" {
		t.Errorf("expected one 'reflected' broker_decisions row; got %v", fake.logged)
	}

	// Second call → reflection already fired; cap still tripped → hard halt.
	tu2 := tu
	tu2.ID = "call_2"
	resultBlocks2, refs2 := s.handleRequestTools(
		context.Background(), tu2, ch, nil, loadedTools,
		&consecutiveEmpty, &totalCalls, maxCalls,
		nil, nil,
		sessionID, &reflectionFired,
	)
	if len(resultBlocks2) != 1 || len(refs2) != 1 {
		t.Fatalf("expected 1 result block + ref; got %d / %d", len(resultBlocks2), len(refs2))
	}
	if !strings.Contains(resultBlocks2[0].Content, "Tool discovery cap reached") {
		t.Errorf("second-strike should hard-halt; got %q", resultBlocks2[0].Content)
	}
	if len(fake.logged) != 2 || fake.logged[1].Outcome != "halted" {
		t.Errorf("expected second row outcome=halted; got %v", fake.logged)
	}
}

func TestHandleRequestTools_LogsLoadedOutcome(t *testing.T) {
	fake := &fakeRequestToolsService{}
	s := &chatServiceImpl{tools: fake}

	loadedTools := map[string]bool{}
	consecutiveEmpty := 0
	totalCalls := 0
	maxCalls := 5
	reflectionFired := false
	sessionID := "s2"

	ch := make(chan chat.StreamEvent, 8)
	tu := provider.ToolUseBlock{
		ID:   "call_x",
		Name: "request_tools",
		Input: map[string]any{
			"intent": "anything",
		},
	}

	// stubToolService.HandleRequestTools returns nil tools + summary "" — the
	// outcome should be "empty".
	_, _ = s.handleRequestTools(
		context.Background(), tu, ch, nil, loadedTools,
		&consecutiveEmpty, &totalCalls, maxCalls,
		nil, nil,
		sessionID, &reflectionFired,
	)
	if len(fake.logged) != 1 {
		t.Fatalf("expected 1 logged row, got %d", len(fake.logged))
	}
	if fake.logged[0].Outcome != "empty" {
		t.Errorf("outcome=%q want 'empty' (stub returns no tools)", fake.logged[0].Outcome)
	}
	if fake.logged[0].TotalCalls != 1 {
		t.Errorf("totalCalls=%d want 1", fake.logged[0].TotalCalls)
	}
}
