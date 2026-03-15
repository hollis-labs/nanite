package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/store"
)

func newTestBroker(t *testing.T) (*ContextClient, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewContextClient(s), s
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"Hi", 1},       // 2 chars / 4 = 0, but min 1
		{"Hello world", 2}, // 11 chars / 4 = 2
		{strings.Repeat("a", 400), 100}, // 400 / 4 = 100
	}

	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestAssembleContext(t *testing.T) {
	cb, s := newTestBroker(t)

	// Set up workspace, session, agent, mode.
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	agent := &store.AgentProfile{
		Name:         "Test",
		Slug:         "test",
		SystemPrompt: "You are a test agent.",
	}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	mode := &store.AgentMode{PromptAddendum: "Be concise."}
	workspace := &store.Workspace{Name: "Test WS", Description: "Testing"}

	// Add a few messages.
	for i := 0; i < 3; i++ {
		msg := &store.Message{SessionID: sess.ID, Role: "user", Content: "test message"}
		if err := s.CreateMessage(msg); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	systemPrompt, messages, err := cb.AssembleContext(context.Background(), sess, agent, mode, workspace)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	if systemPrompt == "" {
		t.Error("expected non-empty system prompt")
	}
	if !containsStr(systemPrompt, "You are a test agent.") {
		t.Error("system prompt should contain agent prompt")
	}
	if !containsStr(systemPrompt, "Be concise.") {
		t.Error("system prompt should contain mode addendum")
	}
	if len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}
}

func TestAssembleContextBudgetEnforcement(t *testing.T) {
	cb, s := newTestBroker(t)

	// Use a very tight budget.
	cb.BudgetPct = 0.001 // 200000 * 0.001 = 200 tokens budget

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	agent := &store.AgentProfile{
		Name:         "Test",
		Slug:         "test-budget",
		SystemPrompt: "Short prompt.",
	}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Create many long messages that exceed the budget.
	for i := 0; i < 20; i++ {
		msg := &store.Message{
			SessionID: sess.ID,
			Role:      "user",
			Content:   strings.Repeat("Long message content. ", 50),
		}
		if err := s.CreateMessage(msg); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	_, messages, err := cb.AssembleContext(context.Background(), sess, agent, &store.AgentMode{}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	// With a tight budget, older messages should have been dropped.
	if len(messages) >= 20 {
		t.Errorf("expected budget enforcement to drop messages, but got all %d", len(messages))
	}

	// Should always keep at least 1 message.
	if len(messages) < 1 {
		t.Error("expected at least 1 message to remain after budget enforcement")
	}
}

func TestEnforceTokenBudget_UnderCeiling(t *testing.T) {
	sys := "You are a helpful assistant."
	msgs := []provider.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}
	tools := []provider.ToolDefinition{
		{Name: "test_tool", Description: "A test tool"},
	}

	outMsgs, outTools, bd, err := EnforceTokenBudget(sys, msgs, tools, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outMsgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(outMsgs))
	}
	if len(outTools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(outTools))
	}
	if bd.Total > bd.Ceiling {
		t.Errorf("total %d should be under ceiling %d", bd.Total, bd.Ceiling)
	}
}

func TestEnforceTokenBudget_PrunesToolResults(t *testing.T) {
	sys := "Short."
	// Build messages with old tool results that should get pruned.
	bigResult := strings.Repeat("x", 4000) // ~1000 tokens
	msgs := []provider.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu1", Content: bigResult},
		}},
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu2", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu2", Content: bigResult},
		}},
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu3", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu3", Content: bigResult},
		}},
		{Role: "user", Content: "last question"},
	}

	// Use a tight ceiling to force pruning.
	outMsgs, _, _, err := EnforceTokenBudget(sys, msgs, nil, 2000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The oldest tool result (tu1) should have been pruned.
	for _, m := range outMsgs {
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_result" && b.ToolUseID == "tu1" {
				if len(b.Content) > 200 {
					t.Errorf("old tool result tu1 should be pruned, but has %d chars", len(b.Content))
				}
			}
		}
	}
}

func TestEnforceTokenBudget_RefusesOversize(t *testing.T) {
	// System prompt alone exceeds the ceiling.
	sys := strings.Repeat("a", 4000) // ~1000 tokens
	msgs := []provider.ChatMessage{
		{Role: "user", Content: strings.Repeat("b", 4000)},
	}

	_, _, _, err := EnforceTokenBudget(sys, msgs, nil, 100) // ceiling of 100 tokens
	if err == nil {
		t.Error("expected error for oversized context, got nil")
	}
}

func TestPruneToolResultsInMemory(t *testing.T) {
	big := strings.Repeat("x", 1000)
	msgs := []provider.ChatMessage{
		// Round 1 (old — should be pruned)
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu1", Content: big},
		}},
		// Round 2 (old — should be pruned)
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu2", Name: "fetch"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu2", Content: big},
		}},
		// Round 3 (recent — keep)
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu3", Name: "write"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu3", Content: big},
		}},
		// Round 4 (recent — keep)
		{Role: "assistant", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_use", ID: "tu4", Name: "read"},
		}},
		{Role: "user", ContentBlocks: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu4", Content: big},
		}},
	}

	result := pruneToolResultsInMemory(msgs)

	// tu1 should be pruned (round 1, >2 rounds old)
	for _, m := range result {
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_result" && b.ToolUseID == "tu1" {
				if !strings.HasPrefix(b.Content, "[pruned:") {
					t.Errorf("tu1 should be pruned, got: %s", b.Content[:50])
				}
			}
			// tu3 and tu4 should be kept
			if b.Type == "tool_result" && (b.ToolUseID == "tu3" || b.ToolUseID == "tu4") {
				if strings.HasPrefix(b.Content, "[pruned:") {
					t.Errorf("%s should NOT be pruned", b.ToolUseID)
				}
			}
		}
	}
}
