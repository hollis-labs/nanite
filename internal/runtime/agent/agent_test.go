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
// will rely on once Phase 3b wires the body.
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

// TestBoot_EarlyValidation covers the validation guards that run before
// any side effects (workspace mkdir, boot dir, wrapper.Run).
// Mode-dispatch + happy-path coverage lives in boot_test.go with fakes.
func TestBoot_EarlyValidation(t *testing.T) {
	t.Run("validate_first", func(t *testing.T) {
		_, err := Boot(context.Background(), nil, Options{Mode: ModeSubagent})
		if err == nil || !strings.Contains(err.Error(), "ParentSessionID") {
			t.Fatalf("expected validate-first error, got %v", err)
		}
	})
	t.Run("requires_dependencies", func(t *testing.T) {
		_, err := Boot(context.Background(), nil, Options{})
		if err == nil || !strings.Contains(err.Error(), "Dependencies is required") {
			t.Fatalf("expected Dependencies error, got %v", err)
		}
	})
	t.Run("requires_manager", func(t *testing.T) {
		_, err := Boot(context.Background(), &Dependencies{}, Options{})
		if err == nil || !strings.Contains(err.Error(), "Manager") {
			t.Fatalf("expected Manager error, got %v", err)
		}
	})
}

// Test_shouldUseStreamingStdio pins the c204/c205 regression
// (CW-20260515-0006) AND its round-1 mode-coverage extension. cmd/
// nanite registers exactly one Claude adapter (StreamingStdio shape)
// for every Boot site, so EVERY mode that can boot Claude must get
// the StreamingStdio runtime — not just ModeLongLived. Round-1
// Copilot caught that a mode gate here left ModeResume / ModeSubagent
// / ModeBackground / ModeOneShot Claude boots on the wrong runtime
// (same hang, different code path). The StreamingStdio runtime honors
// AutoFireFirstTurn so kickoff modes (OneShot/Subagent/Background)
// work, and ModeResume spawns with --resume and skips kickoff — also
// fine.
//
// The table is "Claude across every mode" + "other adapters never
// streaming."
func Test_shouldUseStreamingStdio(t *testing.T) {
	cases := []struct {
		provider string
		mode     Mode
		want     bool
	}{
		// Every Mode that can boot Claude returns true. Lifting the
		// ModeLongLived gate is what fixes the round-1 finding.
		{"claude", ModeLongLived, true},
		{"claude", ModeOneShot, true},
		{"claude", ModeSubagent, true},
		{"claude", ModeBackground, true},
		{"claude", ModeResume, true},
		// Alias coverage (claude-code / claudecode / dropdown shapes
		// normalize to "claude" via normalizeProviderName).
		{"claude-code", ModeLongLived, true},
		{"claudecode", ModeLongLived, true},
		{"pty", ModeLongLived, true},
		{"pty-claude", ModeLongLived, true},
		{"sub-claude", ModeLongLived, true},
		// Subagent kickoff modes hit Claude too in production — pin
		// those aliases.
		{"pty-claude", ModeSubagent, true},
		// Other adapters stay on subprocess-per-turn across every mode.
		{"codex", ModeLongLived, false},
		{"codex", ModeSubagent, false},
		{"opencode", ModeLongLived, false},
		{"opencode", ModeOneShot, false},
		{"gemini", ModeLongLived, false},
	}
	for _, c := range cases {
		if got := shouldUseStreamingStdio(c.provider, c.mode); got != c.want {
			t.Errorf("shouldUseStreamingStdio(%q, %v) = %v, want %v", c.provider, c.mode, got, c.want)
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

// Test_bootdirLayoutFor_supported confirms the three implemented adapters
// resolve to their concrete layouts.
func Test_bootdirLayoutFor_supported(t *testing.T) {
	cases := []struct {
		provider string
		want     Layout
	}{
		{"claude", claudeLayout{}},
		{"claude-code", claudeLayout{}},
		{"claudecode", claudeLayout{}},
		{"codex", codexLayout{}},
		{"opencode", opencodeLayout{}},
	}
	for _, c := range cases {
		got := bootdirLayoutFor(c.provider)
		if _, ok := got.(interface{ BootMode() string }); !ok {
			t.Errorf("provider %q: layout missing BootMode()", c.provider)
		}
	}
}
