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
// any side effects (workspace mkdir, boot dir, SessionsManager.Start).
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
	t.Run("requires_sessions_manager", func(t *testing.T) {
		_, err := Boot(context.Background(), &Dependencies{}, Options{})
		if err == nil || !strings.Contains(err.Error(), "SessionsManager") {
			t.Fatalf("expected SessionsManager error, got %v", err)
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

// Test_runtimeConfigForAdapter_LifecycleFlagsExclusive pins the
// agentsessions invariant that at most one of {PTY, StreamingStdio,
// JsonRpcStdio} is set on a Capabilities value (validateLifecycle in
// agentsessions/types.go). runtimeConfigForAdapter is the only
// insertion point setting these flags; this test guards against a
// future regression that flips two at once and gets rejected by
// NewFromAdapter at construction time. Includes every Claude mode
// post-round-1 — they all set StreamingStdio and nothing else.
func Test_runtimeConfigForAdapter_LifecycleFlagsExclusive(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		mode     Mode
	}{
		{"claude long-lived → StreamingStdio only", "claude", ModeLongLived},
		{"claude one-shot → StreamingStdio only", "claude", ModeOneShot},
		{"claude subagent → StreamingStdio only", "claude", ModeSubagent},
		{"claude background → StreamingStdio only", "claude", ModeBackground},
		{"claude resume → StreamingStdio only", "claude", ModeResume},
		{"codex long-lived → neither (subprocess-per-turn)", "codex", ModeLongLived},
		{"opencode long-lived → neither", "opencode", ModeLongLived},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := runtimeConfigForAdapter(nil, c.provider, c.mode)
			n := 0
			if cfg.Caps.PTY {
				n++
			}
			if cfg.Caps.StreamingStdio {
				n++
			}
			if cfg.Caps.JsonRpcStdio {
				n++
			}
			if n > 1 {
				t.Errorf("Caps has %d lifecycle flags set; agentsessions allows at most 1: PTY=%v StreamingStdio=%v JsonRpcStdio=%v",
					n, cfg.Caps.PTY, cfg.Caps.StreamingStdio, cfg.Caps.JsonRpcStdio)
			}
		})
	}
}

// Test_runtimeConfigForAdapter_ClaudeAllModesSetStreamingStdio is the
// c204/c205 regression pin, expanded across every Mode Claude can
// boot in. The factory's Caps output for ("claude", <any mode>) must
// declare StreamingStdio=true; the underlying adapter argv is the
// same long-lived NDJSON shape across modes, so the runtime kind
// must be too. Pre-round-0 the Caps had everything false → the
// library selected its subprocess-per-turn adapter runtime → claude
// was launched per-turn with the long-lived argv, stdin closed
// before any per-turn payload made sense, no parseable output ever
// surfaced. Pre-round-1 only ModeLongLived was covered; the other
// modes silently kept the broken behavior.
func Test_runtimeConfigForAdapter_ClaudeAllModesSetStreamingStdio(t *testing.T) {
	modes := []Mode{ModeLongLived, ModeOneShot, ModeSubagent, ModeBackground, ModeResume}
	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			cfg := runtimeConfigForAdapter(nil, "claude", mode)
			if !cfg.Caps.StreamingStdio {
				t.Fatalf("Caps.StreamingStdio = false, want true (c204/c205 regression + round-1 mode coverage — every Claude Boot site must select the StreamingStdio runtime)")
			}
			if cfg.Caps.PTY {
				t.Errorf("Caps.PTY = true, want false (StreamingStdio is non-PTY)")
			}
		})
	}
}

// Test_shouldUsePTY pins the post-CW-20260515-0004 contract: no
// supported provider currently requires a PTY. claude long-lived was
// the only true case in the prior matrix; it moved to Streaming Input
// Mode (NDJSON over regular stdio pipes) after c202 showed that the
// PTY-allocated TUI couldn't surface assistant deltas through any
// existing parser. Codex / opencode / gemini were already false.
//
// The helper stays defined as the single insertion point for any
// future adapter that genuinely needs a PTY; this table is its
// regression net.
func Test_shouldUsePTY(t *testing.T) {
	cases := []struct {
		provider string
		mode     Mode
		want     bool
	}{
		{"claude", ModeLongLived, false},
		{"claude-code", ModeLongLived, false},
		{"claudecode", ModeLongLived, false},
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
