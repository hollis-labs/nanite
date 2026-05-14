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

// TestShouldUsePTY_AcceptsCLIAliases verifies the PTY-mode gate
// recognizes dropdown / legacy aliases. Before CW-20260514-0045, a
// profile carrying "pty-claude" / "pty" as default_provider would fall
// through the bare-name switch and shouldUsePTY would return false,
// silently degrading to the subprocess fallback for the chat case
// (long-lived mode) which is the wrong runtime for claude.
func TestShouldUsePTY_AcceptsCLIAliases(t *testing.T) {
	ptyExpected := []string{"pty", "pty-claude", "claude", "claude-code", "claudecode", "sub-claude"}
	for _, name := range ptyExpected {
		if !shouldUsePTY(name, ModeLongLived) {
			t.Errorf("shouldUsePTY(%q, ModeLongLived) = false, want true", name)
		}
	}

	// Codex / opencode are subprocess-per-turn (PTY not yet wired) —
	// dropdown aliases must NOT silently flip them to PTY.
	notPTY := []string{"codex", "pty-codex", "opencode", "pty-opencode", "sub-codex"}
	for _, name := range notPTY {
		if shouldUsePTY(name, ModeLongLived) {
			t.Errorf("shouldUsePTY(%q, ModeLongLived) = true, want false", name)
		}
	}

	// Non-long-lived modes never PTY regardless of alias.
	for _, mode := range []Mode{ModeOneShot, ModeSubagent, ModeBackground, ModeResume} {
		if shouldUsePTY("pty-claude", mode) {
			t.Errorf("shouldUsePTY(pty-claude, %v) = true, want false (only ModeLongLived may PTY)", mode)
		}
	}
}
