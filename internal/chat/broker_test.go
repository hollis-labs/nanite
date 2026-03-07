package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

func newTestBroker(t *testing.T) (*ContextBroker, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewContextBroker(s), s
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

	systemPrompt, messages, err := cb.AssembleContext(sess, agent, mode, workspace)
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

	_, messages, err := cb.AssembleContext(sess, agent, &store.AgentMode{}, nil)
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
