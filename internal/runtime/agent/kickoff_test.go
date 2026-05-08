package agent

import (
	"strings"
	"testing"
)

// Test_composeKickoff_returns_at_pointer confirms the tiny-IPC kickoff
// matches the cross-app convention.
func Test_composeKickoff_returns_at_pointer(t *testing.T) {
	got := composeKickoff("orchestrator", "sess-1", "")
	if got != "Boot @./boot.md" {
		t.Errorf("composeKickoff = %q, want %q", got, "Boot @./boot.md")
	}
}

// Test_composeBootContent_emits_role_session_mode confirms the planted
// boot.md surfaces enough identity for the agent to anchor on.
func Test_composeBootContent_emits_role_session_mode(t *testing.T) {
	got := composeBootContent(Options{
		Role:            "reviewer",
		AgentProfile:    "review-bot",
		SessionID:       "sess-42",
		ParentSessionID: "sess-parent",
		Mode:            ModeSubagent,
	})
	for _, want := range []string{
		"reviewer", "review-bot", "sess-42", "sess-parent",
		"subagent", "nested subagent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("composeBootContent missing %q in:\n%s", want, got)
		}
	}
}

// Test_composeBootContent_one_shot_includes_task_when_provided
func Test_composeBootContent_one_shot_includes_task_when_provided(t *testing.T) {
	got := composeBootContent(Options{
		Mode:          ModeOneShot,
		OneShotPrompt: "Refactor handler.go to use the new context type.",
	})
	if !strings.Contains(got, "Refactor handler.go") {
		t.Errorf("OneShotPrompt missing from boot.md:\n%s", got)
	}
	if !strings.Contains(got, "one-shot") {
		t.Errorf("ModeOneShot framing missing:\n%s", got)
	}
}

// Test_composeKickoffRaw_falls_back_to_content_when_at_unsupported
// confirms the raw-content fallback never returns empty.
func Test_composeKickoffRaw_falls_back_to_content_when_at_unsupported(t *testing.T) {
	got := composeKickoffRaw(Options{Role: "executor", SessionID: "sess-x"})
	if got == "" {
		t.Fatal("composeKickoffRaw returned empty payload")
	}
	if !strings.Contains(got, "executor") || !strings.Contains(got, "sess-x") {
		t.Errorf("raw kickoff missing identity: %s", got)
	}
}

// Test_composeKickoffRaw_defensive_default keeps the payload non-empty
// when opts have no role/session/etc.
func Test_composeKickoffRaw_defensive_default(t *testing.T) {
	got := composeKickoffRaw(Options{})
	if got == "" {
		t.Fatal("composeKickoffRaw returned empty payload on zero-value Options")
	}
}
