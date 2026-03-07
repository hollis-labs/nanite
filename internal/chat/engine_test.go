package chat

import (
	"path/filepath"
	"testing"

	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	reg := provider.NewRegistry()
	return NewEngine(s, reg)
}

func TestNewEngine(t *testing.T) {
	e := newTestEngine(t)

	if e.Store == nil {
		t.Error("expected Store to be set")
	}
	if e.Providers == nil {
		t.Error("expected Providers to be set")
	}
	if e.Broker == nil {
		t.Error("expected Broker to be set")
	}
}

func TestAssembleSystemPrompt(t *testing.T) {
	tests := []struct {
		name      string
		agent     *store.AgentProfile
		mode      *store.AgentMode
		workspace *store.Workspace
		wantSub   string // substring expected in the result
	}{
		{
			name:    "agent only",
			agent:   &store.AgentProfile{SystemPrompt: "You are a helpful bot."},
			mode:    nil,
			wantSub: "You are a helpful bot.",
		},
		{
			name:    "agent with mode addendum",
			agent:   &store.AgentProfile{SystemPrompt: "Base prompt."},
			mode:    &store.AgentMode{PromptAddendum: "Focus on architecture."},
			wantSub: "Focus on architecture.",
		},
		{
			name:      "agent with workspace",
			agent:     &store.AgentProfile{SystemPrompt: "Base prompt."},
			mode:      nil,
			workspace: &store.Workspace{Name: "Tiamat", Description: "AI portfolio"},
			wantSub:   "Workspace: Tiamat - AI portfolio",
		},
		{
			name:      "all components",
			agent:     &store.AgentProfile{SystemPrompt: "Base."},
			mode:      &store.AgentMode{PromptAddendum: "Mode addendum."},
			workspace: &store.Workspace{Name: "WS"},
			wantSub:   "Workspace: WS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := assembleSystemPrompt(tt.agent, tt.mode, tt.workspace)

			if result == "" {
				t.Fatal("expected non-empty system prompt")
			}

			// Verify it contains the agent's base prompt.
			if tt.agent.SystemPrompt != "" {
				if !containsStr(result, tt.agent.SystemPrompt) {
					t.Errorf("expected system prompt to contain agent prompt %q", tt.agent.SystemPrompt)
				}
			}

			// Verify expected substring.
			if !containsStr(result, tt.wantSub) {
				t.Errorf("expected system prompt to contain %q, got: %s", tt.wantSub, result)
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
