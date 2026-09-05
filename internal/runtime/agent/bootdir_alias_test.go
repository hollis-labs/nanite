package agent

import "testing"

// TestNormalizeProviderName_AliasTable pins the alias-normalization
// rules the bootdir / factory layers apply. CW-20260514-0045:
// internal/chat/engine.go's NormalizeCLIProvider is the canonical rule
// table; this duplicated helper exists so internal/runtime/agent stays
// free of an internal/chat dependency. The two must stay in lock-step;
// this test pins the runtime-side contract.
func TestNormalizeProviderName_AliasTable(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// Legacy bare "pty" → "claude" (pre-CW-20260508-0002 default).
		{"pty", "claude"},
		// Dropdown prefixed aliases → bare adapter names.
		{"pty-claude", "claude"},
		{"pty-codex", "codex"},
		{"pty-opencode", "opencode"},
		// Generic subprocess prefix strip.
		{"sub-claude", "claude"},
		{"sub-codex", "codex"},
		// Bare names untouched.
		{"claude", "claude"},
		{"codex", "codex"},
		{"opencode", "opencode"},
		// Non-CLI providers untouched.
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		// Empty input.
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeProviderName(tt.in); got != tt.want {
			t.Errorf("normalizeProviderName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestBootdirLayoutFor_AcceptsCLIAliases verifies the bootdir dispatch
// resolves a real Layout (not unsupportedLayout) for every dropdown-shape
// CLI alias. Before CW-20260514-0045, "pty-claude" / "pty-codex" /
// "pty-opencode" / legacy "pty" would all fall to unsupportedLayout and
// agent.Boot would fail on the bootdir.Setup error. This regression
// guards the runtime/bootdir layer per the ticket's normalization
// requirement.
func TestBootdirLayoutFor_AcceptsCLIAliases(t *testing.T) {
	tests := []struct {
		in       string
		wantType string
	}{
		{"pty", "claudeLayout"},
		{"pty-claude", "claudeLayout"},
		{"claude", "claudeLayout"},
		{"claude-code", "claudeLayout"},
		{"pty-codex", "codexLayout"},
		{"codex", "codexLayout"},
		{"pty-opencode", "opencodeLayout"},
		{"opencode", "opencodeLayout"},
		// Generic subprocess prefix strip — sub-claude must reach
		// claudeLayout, not unsupportedLayout.
		{"sub-claude", "claudeLayout"},
		{"sub-codex", "codexLayout"},
	}
	for _, tt := range tests {
		layout := bootdirLayoutFor(tt.in)
		if _, ok := layout.(unsupportedLayout); ok {
			t.Errorf("bootdirLayoutFor(%q) returned unsupportedLayout; want %s", tt.in, tt.wantType)
			continue
		}
		// Type-switch on the concrete layout types; this catches a
		// future refactor that swaps one layout for another by
		// mistake.
		switch tt.wantType {
		case "claudeLayout":
			if _, ok := layout.(claudeLayout); !ok {
				t.Errorf("bootdirLayoutFor(%q) returned %T, want claudeLayout", tt.in, layout)
			}
		case "codexLayout":
			if _, ok := layout.(codexLayout); !ok {
				t.Errorf("bootdirLayoutFor(%q) returned %T, want codexLayout", tt.in, layout)
			}
		case "opencodeLayout":
			if _, ok := layout.(opencodeLayout); !ok {
				t.Errorf("bootdirLayoutFor(%q) returned %T, want opencodeLayout", tt.in, layout)
			}
		}
	}
}

// TestHasBootdirLayout_MatchesLayoutDispatch is the regression pin for
// CW-20260815-0024: HasBootdirLayout must agree with bootdirLayoutFor —
// true iff the provider resolves to a real Layout, not unsupportedLayout —
// including CLI-alias normalization (pty-claude etc.) and the plain HTTP
// API providers (anthropic, openai, ...) that were never meant to go
// through the CLI-boot recovery path.
func TestHasBootdirLayout_MatchesLayoutDispatch(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"claude", true},
		{"claude-code", true},
		{"claudecode", true},
		{"codex", true},
		{"opencode", true},
		{"pty", true},
		{"pty-claude", true},
		{"pty-codex", true},
		{"pty-opencode", true},
		{"sub-claude", true},
		{"sub-codex", true},
		// Plain HTTP API providers — no CLI subprocess, no bootdir.
		{"anthropic", false},
		{"openai", false},
		{"gemini-api", false},
		{"openrouter", false},
		// CLI tool names with no Layout implemented yet.
		{"gemini", false},
		{"copilot", false},
		{"aider", false},
		{"junie", false},
		{"", false},
	}
	for _, tt := range tests {
		got := HasBootdirLayout(tt.in)
		if got != tt.want {
			t.Errorf("HasBootdirLayout(%q) = %v, want %v", tt.in, got, tt.want)
		}
		// Cross-check against the real dispatch table so this can never
		// silently drift from bootdirLayoutFor's actual behavior.
		_, isUnsupported := bootdirLayoutFor(tt.in).(unsupportedLayout)
		wantFromDispatch := !isUnsupported
		if got != wantFromDispatch {
			t.Errorf("HasBootdirLayout(%q) = %v, disagrees with bootdirLayoutFor's unsupportedLayout check (%v)", tt.in, got, wantFromDispatch)
		}
	}
}
