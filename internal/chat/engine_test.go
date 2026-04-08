package chat

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/toolclient"
)

func TestBuildToolCatalog(t *testing.T) {
	tests := []struct {
		name      string
		summaries []toolclient.ToolSummary
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
			summaries: []toolclient.ToolSummary{
				{Name: "search_files", Description: "Search for files by pattern"},
			},
			wantSub: "- search_files: Search for files by pattern",
		},
		{
			name: "includes header",
			summaries: []toolclient.ToolSummary{
				{Name: "tool_a", Description: "Does A"},
			},
			wantSub: "Available tools (use request_tools to get full details):",
		},
		{
			name: "truncates long descriptions",
			summaries: []toolclient.ToolSummary{
				{Name: "long_tool", Description: "This is a very long description that exceeds one hundred and twenty characters and should be truncated with an ellipsis at the end"},
			},
			wantSub: "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildToolCatalog(tt.summaries)
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

func TestInferProvider(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"claude-cli", "pty"},
		{"codex-cli", "pty-codex"},
		{"gemini-cli", "pty-gemini"},
		{"gpt-4", "openai"},
		{"o1-preview", "openai"},
		{"o3-mini", "openai"},
		{"llama3", "ollama"},
		{"mistral-7b", "ollama"},
		{"gemma-2b", "ollama"},
		{"claude-sonnet-4-20250514", "anthropic"},
		{"", "anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := InferProvider(tt.model)
			if got != tt.want {
				t.Errorf("InferProvider(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestIsCLIProvider(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"pty", true},
		{"pty-codex", true},
		{"sub-claude", true},
		{"anthropic", false},
		{"openai", false},
	}
	for _, tt := range tests {
		if got := IsCLIProvider(tt.name); got != tt.want {
			t.Errorf("IsCLIProvider(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTruncateStr(t *testing.T) {
	if got := TruncateStr("hello", 10); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := TruncateStr("hello world", 5); got != "hello..." {
		t.Errorf("expected 'hello...', got %q", got)
	}
}

func TestParseAgentConstraints(t *testing.T) {
	c := ParseAgentConstraints("")
	if c.MaxIterations != 0 {
		t.Errorf("expected 0, got %d", c.MaxIterations)
	}

	c = ParseAgentConstraints(`{"max_iterations":5,"max_turns":10}`)
	if c.MaxIterations != 5 {
		t.Errorf("expected 5, got %d", c.MaxIterations)
	}
	if c.MaxTurns != 10 {
		t.Errorf("expected 10, got %d", c.MaxTurns)
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
