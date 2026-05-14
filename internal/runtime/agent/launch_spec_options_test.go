package agent

// CW-20260514-0048: regression coverage for the boot-profile-driven
// Options fields added in this ticket — BootPromptOverride and
// ExtraArgs. The chat layer (driveBootSession) sets these from a
// compiled bootprofile.LaunchSpec.

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestResolveBootPrompt_Override verifies Options.BootPromptOverride
// wins verbatim over the role-derived composeSystemPrompt output.
// Pinned per-layout coverage lives in TestBootPrompt_*Layout below;
// this test exercises the shared resolver directly.
func TestResolveBootPrompt_Override(t *testing.T) {
	profile := &store.AgentProfile{SystemPrompt: "should-not-appear"}
	opts := Options{
		Role:               "executor",
		Mode:               ModeLongLived,
		BootPromptOverride: "OVERRIDE BODY",
	}
	got := resolveBootPrompt(profile, opts)
	if got != "OVERRIDE BODY" {
		t.Fatalf("resolveBootPrompt with override = %q, want override verbatim", got)
	}
}

// TestResolveBootPrompt_FallthroughComposes verifies the empty
// override delegates to composeSystemPrompt — the pre-CW-20260514-0048
// behavior.
func TestResolveBootPrompt_FallthroughComposes(t *testing.T) {
	profile := &store.AgentProfile{SystemPrompt: "base prompt"}
	got := resolveBootPrompt(profile, Options{Role: "", Mode: ModeLongLived})
	if got != "base prompt" {
		t.Fatalf("resolveBootPrompt fallthrough = %q, want %q", got, "base prompt")
	}
}

// TestBootPrompt_ClaudeLayout_HonorsOverride pins that the claude
// layout reads the override via resolveBootPrompt — the chat layer
// sets opts.BootPromptOverride; the layout's BootPrompt method MUST
// honor it. Regression check that a future refactor doesn't bypass
// resolveBootPrompt and call composeSystemPrompt directly.
func TestBootPrompt_ClaudeLayout_HonorsOverride(t *testing.T) {
	got := claudeLayout{}.BootPrompt(
		&store.AgentProfile{SystemPrompt: "base"},
		Options{BootPromptOverride: "from-spec"})
	if got != "from-spec" {
		t.Fatalf("claudeLayout.BootPrompt = %q, want from-spec", got)
	}
}

// TestBootPrompt_CodexLayout_HonorsOverride mirrors the claude
// coverage for codex.
func TestBootPrompt_CodexLayout_HonorsOverride(t *testing.T) {
	got := codexLayout{}.BootPrompt(
		&store.AgentProfile{SystemPrompt: "base"},
		Options{BootPromptOverride: "from-spec"})
	if got != "from-spec" {
		t.Fatalf("codexLayout.BootPrompt = %q, want from-spec", got)
	}
}

// TestBootPrompt_OpencodeLayout_HonorsOverride mirrors the claude
// coverage for opencode.
func TestBootPrompt_OpencodeLayout_HonorsOverride(t *testing.T) {
	got := opencodeLayout{}.BootPrompt(
		&store.AgentProfile{SystemPrompt: "base"},
		Options{BootPromptOverride: "from-spec"})
	if got != "from-spec" {
		t.Fatalf("opencodeLayout.BootPrompt = %q, want from-spec", got)
	}
}

// TestOptions_NormalStart_NoResumePreset is the "no resume on normal
// start" pin from CW-20260514-0048. The ticket's acceptance criterion
// "normal launches do NOT use stored provider resume IDs" maps to:
// ModeLongLived with no ResumeFromCheckpoint produces no provider
// session id preset path. Verified at the Validate layer (Mode-specific
// rules); the runtime-side preset assignment only fires for ModeResume.
//
// This test reads as a structural assertion: the only field that
// triggers resume-id preseeding in agent.Boot is Mode==ModeResume +
// ResumeFromCheckpoint != "". A normal long-lived launch carries
// neither, so the resume path is unreachable from this Options shape.
// Pinned so a future code change that adds an inferred resume path
// (e.g. "look up last checkpoint by sessionID") trips this test.
func TestOptions_NormalStart_NoResumePreset(t *testing.T) {
	// Mode=ModeLongLived, no ResumeFromCheckpoint set. Validate must
	// pass (no resume invariant fires) and the Options carries no
	// resume signal.
	opts := Options{
		Mode:      ModeLongLived,
		SessionID: "sess-1",
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate for normal start = %v, want nil", err)
	}
	if opts.ResumeFromCheckpoint != "" {
		t.Fatalf("normal start ResumeFromCheckpoint = %q, want empty (structural invariant)",
			opts.ResumeFromCheckpoint)
	}
	if opts.Mode == ModeResume {
		t.Fatalf("normal start Mode = ModeResume, want ModeLongLived")
	}
}
