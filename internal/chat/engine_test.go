package chat

import (
	"path/filepath"
	"testing"

	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
	"github.com/hollis-labs/mentat-chat/internal/toolbroker"
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

func TestBuildToolCatalog(t *testing.T) {
	tests := []struct {
		name      string
		summaries []toolbroker.ToolSummary
		wantEmpty bool
		wantSub   string
	}{
		{
			name:      "empty summaries",
			summaries: nil,
			wantEmpty: true,
		},
		{
			name: "single tool",
			summaries: []toolbroker.ToolSummary{
				{Name: "search_files", Description: "Search for files by pattern"},
			},
			wantSub: "- search_files: Search for files by pattern",
		},
		{
			name: "includes header",
			summaries: []toolbroker.ToolSummary{
				{Name: "tool_a", Description: "Does A"},
			},
			wantSub: "Available tools (use request_tools to get full details):",
		},
		{
			name: "truncates long descriptions",
			summaries: []toolbroker.ToolSummary{
				{Name: "long_tool", Description: "This is a very long description that exceeds one hundred and twenty characters and should be truncated with an ellipsis at the end"},
			},
			wantSub: "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildToolCatalog(tt.summaries)
			if tt.wantEmpty {
				if result != "" {
					t.Errorf("expected empty catalog, got %q", result)
				}
				return
			}
			if !containsStr(result, tt.wantSub) {
				t.Errorf("expected catalog to contain %q, got:\n%s", tt.wantSub, result)
			}
		})
	}
}

func TestRequestToolsDef_HasCorrectSchema(t *testing.T) {
	if requestToolsDef.Name != "request_tools" {
		t.Errorf("expected name=request_tools, got %s", requestToolsDef.Name)
	}
	if requestToolsDef.Description == "" {
		t.Error("expected non-empty description")
	}
	if requestToolsDef.InputSchema == nil {
		t.Fatal("expected non-nil InputSchema")
	}

	// Verify schema structure.
	props, ok := requestToolsDef.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in input schema")
	}
	toolNames, ok := props["tool_names"].(map[string]any)
	if !ok {
		t.Fatal("expected tool_names in properties")
	}
	if toolNames["type"] != "array" {
		t.Errorf("expected tool_names type=array, got %v", toolNames["type"])
	}

	required, ok := requestToolsDef.InputSchema["required"].([]any)
	if !ok {
		t.Fatal("expected required field in schema")
	}
	if len(required) != 1 || required[0] != "tool_names" {
		t.Errorf("expected required=[tool_names], got %v", required)
	}
}

func TestProgressiveDiscoveryThreshold(t *testing.T) {
	if ProgressiveDiscoveryThreshold != 20 {
		t.Errorf("expected threshold=20, got %d", ProgressiveDiscoveryThreshold)
	}
}

func TestExtractIntent(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		wantIntent string
		wantHints  int // minimum number of hints expected
	}{
		{
			name:       "simple code request",
			message:    "Please search for files matching the pattern",
			wantIntent: "search files matching",
			wantHints:  3,
		},
		{
			name:       "empty message returns general",
			message:    "",
			wantIntent: "general",
			wantHints:  0,
		},
		{
			name:       "only stop words returns general",
			message:    "hi, I would just to",
			wantIntent: "general",
			wantHints:  0,
		},
		{
			name:       "filters short words",
			message:    "go to the file and read it",
			wantIntent: "file read",
			wantHints:  2,
		},
		{
			name:       "deduplicates keywords",
			message:    "search search search for files",
			wantIntent: "search files",
			wantHints:  2,
		},
		{
			name:       "limits to 10 hints",
			message:    "alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november",
			wantIntent: "alpha bravo charlie",
			wantHints:  10,
		},
		{
			name:       "strips punctuation",
			message:    "What's the database schema? Check the migrations!",
			wantIntent: "database schema check",
			wantHints:  3,
		},
		{
			name:       "never returns wildcard",
			message:    "* do something",
			wantIntent: "something",
			wantHints:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent, hints := ExtractIntent(tt.message)

			if intent != tt.wantIntent {
				t.Errorf("intent = %q, want %q", intent, tt.wantIntent)
			}

			if len(hints) < tt.wantHints {
				t.Errorf("got %d hints, want at least %d (hints: %v)", len(hints), tt.wantHints, hints)
			}

			// Verify intent is never wildcard or empty.
			if intent == "*" || intent == "" {
				t.Errorf("intent must never be wildcard or empty, got %q", intent)
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
