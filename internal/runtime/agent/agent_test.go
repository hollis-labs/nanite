package agent

import (
	"context"
	"strings"
	"testing"
)

// TestMode_String verifies the canonical persisted identifiers.
func TestMode_String(t *testing.T) {
	cases := []struct {
		mode Mode
		want string
	}{
		{ModeLongLived, "long_lived"},
		{ModeOneShot, "one_shot"},
		{ModeResume, "resume"},
		{ModeSubagent, "subagent"},
		{ModeBackground, "background"},
		{Mode(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.mode.String(); got != c.want {
			t.Errorf("Mode(%d).String() = %q, want %q", c.mode, got, c.want)
		}
	}
}

// TestOptions_Validate enforces the mode-specific invariants Boot
// will rely on once Phase 3 wires the body.
func TestOptions_Validate(t *testing.T) {
	t.Run("ModeSubagent_requires_ParentSessionID", func(t *testing.T) {
		err := Options{Mode: ModeSubagent}.Validate()
		if err == nil || !strings.Contains(err.Error(), "ParentSessionID") {
			t.Fatalf("expected ParentSessionID error, got %v", err)
		}
		if err := (Options{Mode: ModeSubagent, ParentSessionID: "p1"}).Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("ModeResume_requires_ResumeFromCheckpoint", func(t *testing.T) {
		err := Options{Mode: ModeResume}.Validate()
		if err == nil || !strings.Contains(err.Error(), "ResumeFromCheckpoint") {
			t.Fatalf("expected ResumeFromCheckpoint error, got %v", err)
		}
		if err := (Options{Mode: ModeResume, ResumeFromCheckpoint: "cp1"}).Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("default_mode_is_long_lived", func(t *testing.T) {
		var opts Options // zero value = ModeLongLived
		if opts.Mode != ModeLongLived {
			t.Fatalf("zero-value Mode = %v, want ModeLongLived", opts.Mode)
		}
		if err := opts.Validate(); err != nil {
			t.Fatalf("zero-value Options should validate, got %v", err)
		}
	})
}

// TestBoot_PhaseTwoStub confirms the skeleton compiles and Boot returns
// a clear "not yet implemented" error rather than panicking. Phase 3
// replaces this with real Boot lifecycle tests.
func TestBoot_PhaseTwoStub(t *testing.T) {
	_, err := Boot(context.Background(), nil, Options{})
	if err == nil {
		t.Fatal("expected skeleton error, got nil")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("expected skeleton sentinel, got %v", err)
	}
}

// Test_shouldUsePTY validates the runtime selection matrix locked at
// decisions.nanite.architecture.cli_pty_long_lived_default.
func Test_shouldUsePTY(t *testing.T) {
	cases := []struct {
		provider string
		mode     Mode
		want     bool
	}{
		{"claude", ModeLongLived, true},
		{"claude-code", ModeLongLived, true},
		{"claudecode", ModeLongLived, true},
		{"claude", ModeOneShot, false},
		{"claude", ModeSubagent, false},
		{"codex", ModeLongLived, false},
		{"opencode", ModeLongLived, false},
		{"gemini", ModeLongLived, false},
	}
	for _, c := range cases {
		if got := shouldUsePTY(c.provider, c.mode); got != c.want {
			t.Errorf("shouldUsePTY(%q, %v) = %v, want %v", c.provider, c.mode, got, c.want)
		}
	}
}

// Test_shouldAutoFireFirstTurn validates the auto-fire matrix.
func Test_shouldAutoFireFirstTurn(t *testing.T) {
	cases := []struct {
		mode Mode
		want bool
	}{
		{ModeLongLived, false},
		{ModeOneShot, true},
		{ModeResume, false},
		{ModeSubagent, true},
		{ModeBackground, true},
	}
	for _, c := range cases {
		if got := shouldAutoFireFirstTurn(c.mode); got != c.want {
			t.Errorf("shouldAutoFireFirstTurn(%v) = %v, want %v", c.mode, got, c.want)
		}
	}
}

// Test_bootdirLayoutFor_unsupported confirms unsupported providers
// surface a clear-error stub rather than a nil panic.
func Test_bootdirLayoutFor_unsupported(t *testing.T) {
	for _, p := range []string{"gemini", "copilot", "aider", "junie", "exotic"} {
		layout := bootdirLayoutFor(p)
		if _, err := layout.Setup(SetupParams{}); err == nil {
			t.Errorf("provider %q: expected unsupported-stub error, got nil", p)
		}
	}
}
