package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
)

func newTestBroker(t *testing.T) (*ContextClient, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
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
		{"Hi", 1},                       // 2 chars / 4 = 0, but min 1
		{"Hello world", 2},              // 11 chars / 4 = 2
		{strings.Repeat("a", 400), 100}, // 400 / 4 = 100
	}

	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// TestAssembleSlotSources_AgentPromptAndMessageCount covers the same
// agent-prompt + message-count parity the deleted TestAssembleContext
// exercised against the removed legacy AssembleContext path
// (CW-20260814-0005) — ported to the slot-based AssembleSlotSources so
// the coverage survives against the code Chat/GUI/CLI turns and workflow
// steps (via AssembleSlots) actually call.
//
// Phase 0 item 21 ("Cut Modes, in full") deleted store.AgentMode and the
// mode-addendum assertion this test used to carry (mode-addendum content
// no longer exists — SlotMode is permanently empty/inert per INV4's
// post-cut definition; see internal/context/INVARIANTS.md).
func TestAssembleSlotSources_AgentPromptAndMessageCount(t *testing.T) {
	cb, s := newTestBroker(t)

	// Set up workspace, session, agent.
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

	workspace := &store.Workspace{Name: "Test WS", Description: "Testing"}

	// Add a few messages.
	for i := 0; i < 3; i++ {
		msg := &store.Message{SessionID: sess.ID, Role: "user", Content: "test message"}
		if err := s.CreateMessage(msg); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	sources, err := cb.AssembleSlotSources(context.Background(), sess, agent, workspace)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	if sources.Agent == "" {
		t.Error("expected non-empty agent slot content")
	}
	if !containsStr(sources.Agent, "You are a test agent.") {
		t.Error("agent slot should contain agent prompt")
	}
	if sources.Mode != "" {
		t.Errorf("SlotMode content should always be empty post-cut, got %q", sources.Mode)
	}
	if len(sources.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(sources.Messages))
	}
}

func TestEnforceTokenBudget_UnderCeiling(t *testing.T) {
	sys := "You are a helpful assistant."
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}
	tools := []llmtypes.ToolDefinition{
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
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu1", Content: bigResult},
		}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu2", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu2", Content: bigResult},
		}},
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu3", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
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
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: strings.Repeat("b", 4000)},
	}

	_, _, _, err := EnforceTokenBudget(sys, msgs, nil, 100) // ceiling of 100 tokens
	if err == nil {
		t.Error("expected error for oversized context, got nil")
	}
}

// TestEnforceTokenBudget_CeilingOverridePerModel verifies that the
// ceilingOverride parameter correctly threads the per-model window size into
// EnforceTokenBudget, so that Gemini (1M) and Anthropic (200K) sessions use
// different ceilings. This is the regression test for CW-20260426-0031.
func TestEnforceTokenBudget_CeilingOverridePerModel(t *testing.T) {
	defaultCeiling := int(float64(DefaultContextWindow) * HardCeilingPct)
	sys := "system"
	msgs := []llmtypes.ChatMessage{{Role: "user", Content: "hello"}}

	tests := []struct {
		name        string
		windowSize  int // 0 = pass 0 to EnforceTokenBudget (use built-in default)
		wantCeiling int
	}{
		{
			name:        "default (0) uses 200K * HardCeilingPct",
			windowSize:  0,
			wantCeiling: defaultCeiling,
		},
		{
			name:        "Anthropic 200K coincides with default",
			windowSize:  200_000,
			wantCeiling: int(float64(200_000) * HardCeilingPct),
		},
		{
			name:        "Gemini 1M ceiling is larger than 200K default",
			windowSize:  1_000_000,
			wantCeiling: int(float64(1_000_000) * HardCeilingPct),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ceiling int
			if tc.windowSize > 0 {
				ceiling = int(float64(tc.windowSize) * HardCeilingPct)
			}
			_, _, bd, err := EnforceTokenBudget(sys, msgs, nil, ceiling)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bd.Ceiling != tc.wantCeiling {
				t.Errorf("Ceiling = %d, want %d", bd.Ceiling, tc.wantCeiling)
			}
		})
	}

	// Explicit assertion: Gemini ceiling must differ from the 200K default ceiling.
	geminiCeiling := int(float64(1_000_000) * HardCeilingPct)
	if geminiCeiling == defaultCeiling {
		t.Errorf("Gemini ceiling %d unexpectedly equals the 200K default ceiling %d", geminiCeiling, defaultCeiling)
	}
}

func TestPruneToolResultsInMemory(t *testing.T) {
	big := strings.Repeat("x", 1000)
	msgs := []llmtypes.ChatMessage{
		// Round 1 (old — should be pruned)
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu1", Name: "search"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu1", Content: big},
		}},
		// Round 2 (old — should be pruned)
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu2", Name: "fetch"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu2", Content: big},
		}},
		// Round 3 (recent — keep)
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu3", Name: "write"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu3", Content: big},
		}},
		// Round 4 (recent — keep)
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: "tool_use", ID: "tu4", Name: "read"},
		}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{
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
