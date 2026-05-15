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

// TestShouldUsePTY_AllProvidersFalse pins the c202 follow-up
// (CW-20260515-0004). Before this change, claude + ModeLongLived
// returned true → agentsessions allocated a PTY → claude TUI ran in
// the PTY → its ANSI/screen-redraw output had no parser in
// pty_claude_events.go → sessions sat in state=running forever with
// zero assistant deltas (c202's "no errors, but nothing else either"
// symptom). The Streaming Input Mode adapter we ship now wants
// regular stdio pipes (NDJSON in, stream-json out), not a PTY.
//
// New contract: shouldUsePTY returns FALSE for every supported
// provider. The helper is retained for future adapters that genuinely
// need a PTY (none today). If a future change reintroduces a PTY
// case, both the function body and this regression test must update
// in lock-step.
func TestShouldUsePTY_AllProvidersFalse(t *testing.T) {
	// Today every name — claude family, codex, opencode, every alias
	// shape — must return false in long-lived mode. Pin the full
	// alias table from the prior contract so a regression that
	// re-routes any single alias surfaces here.
	allNames := []string{
		"pty", "pty-claude", "claude", "claude-code", "claudecode", "sub-claude",
		"codex", "pty-codex", "sub-codex",
		"opencode", "pty-opencode", "sub-opencode",
	}
	for _, name := range allNames {
		if shouldUsePTY(name, ModeLongLived) {
			t.Errorf("shouldUsePTY(%q, ModeLongLived) = true, want false (CW-20260515-0004: claude moved to StreamingStdio; no provider currently requires a PTY)", name)
		}
	}

	// Non-long-lived modes were never PTY and still aren't.
	for _, mode := range []Mode{ModeOneShot, ModeSubagent, ModeBackground, ModeResume} {
		if shouldUsePTY("pty-claude", mode) {
			t.Errorf("shouldUsePTY(pty-claude, %v) = true, want false (only ModeLongLived may PTY)", mode)
		}
	}
}
