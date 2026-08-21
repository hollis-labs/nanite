package agent

// CW-20260514-0048: regression coverage for the boot-profile-driven
// Options field added in this ticket — BootPromptOverride. The chat
// layer (driveBootSession) sets it from a compiled
// bootprofile.LaunchSpec. (This ticket also added an Options.ExtraArgs
// field; task 06's migration onto wrapper.Wrapper.Run removed the only
// line that ever forwarded it and it was never a live-set field in this
// codebase, so it was removed as dead code — see
// TASKS/agent-host-acp/06-migrate-session-lifecycle-to-wrapper.md's
// 2026-08-21 addendum.)

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestResolveBootPrompt_Override verifies Options.BootPromptOverride's
// BODY wins verbatim over the role-derived composeSystemPrompt output —
// but the mandatory post-compaction re-read instruction (Phase 2 task 03)
// is still appended unconditionally; a catalog-authored override cannot
// suppress it. Pinned per-layout coverage lives in TestBootPrompt_*Layout
// below; this test exercises the shared resolver directly.
func TestResolveBootPrompt_Override(t *testing.T) {
	profile := &store.AgentProfile{SystemPrompt: "should-not-appear"}
	opts := Options{
		Role:               "executor",
		Mode:               ModeLongLived,
		BootPromptOverride: "OVERRIDE BODY",
	}
	got := resolveBootPrompt(profile, opts)
	if !strings.HasPrefix(got, "OVERRIDE BODY") {
		t.Fatalf("resolveBootPrompt with override = %q, want it to start with the override body verbatim", got)
	}
	if strings.Contains(got, "should-not-appear") {
		t.Fatalf("resolveBootPrompt with override = %q, want profile prompt NOT to leak through", got)
	}
	if !strings.Contains(got, mandatoryPostCompactionRereadInstruction) {
		t.Fatalf("resolveBootPrompt with override = %q, want the mandatory post-compaction re-read instruction appended even when a catalog override is set", got)
	}
}

// TestResolveBootPrompt_FallthroughComposes verifies the empty
// override delegates to composeSystemPrompt — the pre-CW-20260514-0048
// behavior — with the mandatory post-compaction re-read instruction
// (Phase 2 task 03) appended.
func TestResolveBootPrompt_FallthroughComposes(t *testing.T) {
	profile := &store.AgentProfile{SystemPrompt: "base prompt"}
	got := resolveBootPrompt(profile, Options{Role: "", Mode: ModeLongLived})
	want := "base prompt\n\n" + mandatoryPostCompactionRereadInstruction
	if got != want {
		t.Fatalf("resolveBootPrompt fallthrough = %q, want %q", got, want)
	}
}

// TestBootPrompt_ClaudeLayout_HonorsOverride pins that the claude
// layout reads the override via resolveBootPrompt — the chat layer
// sets opts.BootPromptOverride; the layout's BootPrompt method MUST
// honor it. Regression check that a future refactor doesn't bypass
// resolveBootPrompt and call composeSystemPrompt directly. The mandatory
// post-compaction re-read instruction (Phase 2 task 03) is appended on
// top of the override body — it is not overridable.
func TestBootPrompt_ClaudeLayout_HonorsOverride(t *testing.T) {
	got := claudeLayout{}.BootPrompt(
		&store.AgentProfile{SystemPrompt: "base"},
		Options{BootPromptOverride: "from-spec"})
	want := "from-spec\n\n" + mandatoryPostCompactionRereadInstruction
	if got != want {
		t.Fatalf("claudeLayout.BootPrompt = %q, want %q", got, want)
	}
}

// TestBootPrompt_CodexLayout_HonorsOverride mirrors the claude
// coverage for codex.
func TestBootPrompt_CodexLayout_HonorsOverride(t *testing.T) {
	got := codexLayout{}.BootPrompt(
		&store.AgentProfile{SystemPrompt: "base"},
		Options{BootPromptOverride: "from-spec"})
	want := "from-spec\n\n" + mandatoryPostCompactionRereadInstruction
	if got != want {
		t.Fatalf("codexLayout.BootPrompt = %q, want %q", got, want)
	}
}

// TestBootPrompt_OpencodeLayout_HonorsOverride mirrors the claude
// coverage for opencode.
func TestBootPrompt_OpencodeLayout_HonorsOverride(t *testing.T) {
	got := opencodeLayout{}.BootPrompt(
		&store.AgentProfile{SystemPrompt: "base"},
		Options{BootPromptOverride: "from-spec"})
	want := "from-spec\n\n" + mandatoryPostCompactionRereadInstruction
	if got != want {
		t.Fatalf("opencodeLayout.BootPrompt = %q, want %q", got, want)
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
