package service

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
)

// fakeRequestToolsService satisfies ToolService for the reflection unit test.
// HandleRequestTools always returns no new tools so the cap-trip path fires.
type fakeRequestToolsService struct {
	stubToolService // embed the existing test stub from chat_test.go
}

// GetToolSchema satisfies the ToolService interface — stubToolService doesn't
// implement it, so we add the no-op here to keep the fake compilable.
func (f *fakeRequestToolsService) GetToolSchema(_ string) map[string]any { return nil }

// Persistence for request_tools calls used to be captured directly on this
// fake (via a LogRequestToolsCall method satisfying the now-deleted
// brokerCallPersister interface, writing to the now-dropped broker_decisions
// SQL table — TASKS/phase-0/23-export-and-drop-decision-tables.md). The
// only remaining live persistence path is the inspector ring buffer
// (chatServiceImpl.persistBrokerCallEx), so these tests now wire a real
// *inspector.Service and assert against its recorded BrokerDecisions
// instead of a fake-captured slice.

func TestHandleRequestTools_ReflectionThenHalt(t *testing.T) {
	fake := &fakeRequestToolsService{}
	insp := inspectsvc.NewService()
	s := &chatServiceImpl{tools: fake, inspector: insp}

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
	turnID := "turn1"

	// Buffered channel sized to absorb the events the helper emits per call:
	// one tool_call + one tool_result per request_tools invocation.
	ch := make(chan chat.StreamEvent, 8)

	tu := llmtypes.ToolUseBlock{
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
		turnID,
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
	snap := insp.Snapshot(sessionID, turnID)
	if snap == nil || len(snap.BrokerDecisions) != 1 || snap.BrokerDecisions[0].Outcome != "reflected" {
		t.Errorf("expected one 'reflected' broker decision recorded; got %+v", snap)
	}

	// Second call → reflection already fired; cap still tripped → hard halt.
	tu2 := tu
	tu2.ID = "call_2"
	resultBlocks2, refs2 := s.handleRequestTools(
		context.Background(), tu2, ch, nil, loadedTools,
		&consecutiveEmpty, &totalCalls, maxCalls,
		nil, nil,
		sessionID, &reflectionFired,
		turnID,
	)
	if len(resultBlocks2) != 1 || len(refs2) != 1 {
		t.Fatalf("expected 1 result block + ref; got %d / %d", len(resultBlocks2), len(refs2))
	}
	if !strings.Contains(resultBlocks2[0].Content, "Tool discovery cap reached") {
		t.Errorf("second-strike should hard-halt; got %q", resultBlocks2[0].Content)
	}
	snap = insp.Snapshot(sessionID, turnID)
	if snap == nil || len(snap.BrokerDecisions) != 2 || snap.BrokerDecisions[1].Outcome != "halted" {
		t.Errorf("expected second decision outcome=halted; got %+v", snap)
	}
}

func TestHandleRequestTools_LogsLoadedOutcome(t *testing.T) {
	fake := &fakeRequestToolsService{}
	insp := inspectsvc.NewService()
	s := &chatServiceImpl{tools: fake, inspector: insp}

	loadedTools := map[string]bool{}
	consecutiveEmpty := 0
	totalCalls := 0
	maxCalls := 5
	reflectionFired := false
	sessionID := "s2"
	turnID := "turn2"

	ch := make(chan chat.StreamEvent, 8)
	tu := llmtypes.ToolUseBlock{
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
		turnID,
	)
	snap := insp.Snapshot(sessionID, turnID)
	if snap == nil || len(snap.BrokerDecisions) != 1 {
		t.Fatalf("expected 1 recorded broker decision, got %+v", snap)
	}
	if snap.BrokerDecisions[0].Outcome != "empty" {
		t.Errorf("outcome=%q want 'empty' (stub returns no tools)", snap.BrokerDecisions[0].Outcome)
	}
	if snap.BrokerDecisions[0].TotalCalls != 1 {
		t.Errorf("totalCalls=%d want 1", snap.BrokerDecisions[0].TotalCalls)
	}
}
