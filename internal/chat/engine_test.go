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

// TestNormalizeCLIProvider exercises the canonical alias-normalization
// table. CW-20260514-0045: this is the single source of truth shared by
// chat_generate's CLI bypass, service/agent_deps.stripRegistryPrefix
// (delegates here), runtime/agent/bootdir.normalizeProviderName
// (intentional duplicate to avoid the chat→runtime import inversion), and
// runtime/agent/factory.shouldUsePTY (calls normalizeProviderName). All
// four sites must agree on the alias table; this test pins the contract.
func TestNormalizeCLIProvider(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// Dropdown-emitted prefixed aliases → bare adapter names.
		{"pty-claude", "claude"},
		{"pty-codex", "codex"},
		{"pty-opencode", "opencode"},
		// Legacy bare "pty" → "claude" (pre-CW-20260508-0002 default).
		{"pty", "claude"},
		// General PTY / subprocess prefix strip.
		{"pty-gemini", "gemini"},
		{"sub-claude", "claude"},
		{"sub-aider", "aider"},
		// Bare names pass through unchanged.
		{"claude", "claude"},
		{"codex", "codex"},
		{"opencode", "opencode"},
		// Non-CLI providers pass through unchanged.
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		// Empty input edge.
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeCLIProvider(tt.in); got != tt.want {
			t.Errorf("NormalizeCLIProvider(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestNormalizeCLIProvider_RoundTripWithIsCLIProvider verifies that every
// input IsCLIProvider classifies as CLI normalizes to a non-empty bare
// adapter name. This pins the contract that the chat_generate CLI bypass
// relies on: if IsCLIProvider(name) is true, NormalizeCLIProvider(name)
// returns a name the runtime CLI adapter index can resolve (after the
// adapter index is keyed by bare names).
func TestNormalizeCLIProvider_RoundTripWithIsCLIProvider(t *testing.T) {
	cliInputs := []string{"pty", "pty-claude", "pty-codex", "pty-opencode", "sub-claude", "sub-codex"}
	for _, name := range cliInputs {
		if !IsCLIProvider(name) {
			t.Fatalf("setup invariant violated: IsCLIProvider(%q) = false; the round-trip test only inspects CLI inputs", name)
		}
		got := NormalizeCLIProvider(name)
		if got == "" {
			t.Errorf("NormalizeCLIProvider(%q) returned empty string; CLI provider must normalize to a bare adapter name", name)
		}
		if got == name {
			// Sanity: a CLI input that round-trips unchanged means
			// normalization is a no-op for it, which defeats the bypass.
			// "pty-" / "sub-" prefixes must strip; bare "pty" must rewrite.
			t.Errorf("NormalizeCLIProvider(%q) returned the same name; expected a bare adapter name", name)
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
	// CW-20260512-0123 (SP-20260512-0011 W3): legacy keys
	// `max_iterations` / `max_time_seconds` / `retry_budget` are no
	// longer fields on AgentConstraints; json.Unmarshal silently
	// drops them. The remaining knobs are the Phase-4 chat-loop
	// runaway breakers (max_turns, hard_ceiling, *_fail_cap, idle).
	c := ParseAgentConstraints("")
	if c.MaxTurns != 0 {
		t.Errorf("empty constraints: MaxTurns = %d, want 0", c.MaxTurns)
	}

	c = ParseAgentConstraints(`{"max_iterations":5,"max_turns":10,"max_time_seconds":300,"retry_budget":2,"hard_ceiling":500}`)
	if c.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, want 10", c.MaxTurns)
	}
	if c.HardCeiling != 500 {
		t.Errorf("HardCeiling = %d, want 500", c.HardCeiling)
	}
	// Legacy keys are silently dropped by json.Unmarshal because they
	// are no longer struct fields — surfaced via validation warnings
	// at the API layer (internal/agentvalidation/validation.go).

	c = ParseAgentConstraints(`{"subagent_completion_policy":"auto_summarize"}`)
	if c.SubagentCompletionPolicy != SubagentPolicyAutoSummarize {
		t.Errorf("SubagentCompletionPolicy = %q, want %q", c.SubagentCompletionPolicy, SubagentPolicyAutoSummarize)
	}
}

func TestIsValidSubagentCompletionPolicy(t *testing.T) {
	valid := []string{SubagentPolicyRenderAndWait, SubagentPolicyAutoSummarize, SubagentPolicyBatch}
	for _, v := range valid {
		if !IsValidSubagentCompletionPolicy(v) {
			t.Errorf("IsValidSubagentCompletionPolicy(%q) = false, want true", v)
		}
	}

	invalid := []string{"", "auto-summarize", "AUTO_SUMMARIZE", "bogus"}
	for _, v := range invalid {
		if IsValidSubagentCompletionPolicy(v) {
			t.Errorf("IsValidSubagentCompletionPolicy(%q) = true, want false", v)
		}
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
