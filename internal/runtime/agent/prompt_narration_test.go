package agent

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestResolveBootPrompt_CarriesCLINarration pins the CW-20260519-0068
// narration rule at the home CW-20260910-0011 moved it to. It left
// internal/chat's universal-rules block because the harness-level half of
// that fix (subagent.Service.startHeartbeat) already covers GUI/API
// structurally; the CLI surface has no heartbeat, so the model's own
// narration is the only progress signal an operator gets there.
func TestResolveBootPrompt_CarriesCLINarration(t *testing.T) {
	got := resolveBootPrompt(&store.AgentProfile{SystemPrompt: "base prompt"}, Options{})
	for _, needle := range []string{"Narrate long waits", "still working on X"} {
		if !strings.Contains(got, needle) {
			t.Errorf("CLI boot prompt missing %q — the narration rule lost its only remaining home", needle)
		}
	}
}

// TestResolveBootPrompt_NarrationSurvivesBootPromptOverride verifies the
// rule rides the same unconditional append point as the post-compaction
// re-read instruction, so a catalog-authored BootPromptOverride cannot
// drop it.
func TestResolveBootPrompt_NarrationSurvivesBootPromptOverride(t *testing.T) {
	got := resolveBootPrompt(nil, Options{BootPromptOverride: "catalog-authored body"})
	if !strings.Contains(got, "Narrate long waits") {
		t.Error("BootPromptOverride dropped the narration rule — it must not be overridable")
	}
	if !strings.Contains(got, "re-read the project's own CLAUDE.md") {
		t.Error("BootPromptOverride dropped the post-compaction re-read instruction")
	}
}

// TestResolveSystemPrompt_CarriesCLINarration covers the second entry
// point (mid-session CLAUDE.md regeneration), which must agree with
// resolveBootPrompt or a regenerated boot dir would silently lose the rule.
func TestResolveSystemPrompt_CarriesCLINarration(t *testing.T) {
	got := ResolveSystemPrompt("", &store.AgentProfile{SystemPrompt: "base"}, ModeLongLived, "", nil)
	if !strings.Contains(got, "Narrate long waits") {
		t.Error("ResolveSystemPrompt dropped the narration rule — mid-session regeneration would lose it")
	}
}
