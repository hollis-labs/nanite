package agent

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// Test_composeSystemPrompt_layers_profile_role_mode confirms the
// composition order: profile.SystemPrompt then role framing then mode
// framing, separated by blank lines.
func Test_composeSystemPrompt_layers_profile_role_mode(t *testing.T) {
	profile := &store.AgentProfile{
		SystemPrompt: "You are the nanite default assistant.",
	}
	got := composeSystemPrompt("orchestrator", profile, ModeSubagent)

	for _, want := range []string{
		"nanite default assistant",
		"orchestrator",
		"nested subagent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in composed prompt:\n%s", want, got)
		}
	}

	// Order: profile content appears before role framing appears before mode framing.
	idxProfile := strings.Index(got, "nanite default")
	idxRole := strings.Index(got, "orchestrator")
	idxMode := strings.Index(got, "nested subagent")
	if !(idxProfile < idxRole && idxRole < idxMode) {
		t.Errorf("composition order broken: profile=%d role=%d mode=%d", idxProfile, idxRole, idxMode)
	}
}

// Test_composeSystemPrompt_handles_nil_profile_and_unknown_role
func Test_composeSystemPrompt_handles_nil_profile_and_unknown_role(t *testing.T) {
	got := composeSystemPrompt("unknown-role", nil, ModeLongLived)
	if got != "" {
		t.Errorf("expected empty prompt, got %q", got)
	}
}

// Test_roleFraming_known_roles
func Test_roleFraming_known_roles(t *testing.T) {
	for _, role := range []string{"orchestrator", "reviewer", "planner", "executor"} {
		if got := roleFraming(role); got == "" {
			t.Errorf("roleFraming(%q) returned empty", role)
		}
	}
	if got := roleFraming("nonsense"); got != "" {
		t.Errorf("roleFraming(nonsense) = %q, want empty", got)
	}
}

// Test_modeFraming_subagent_and_background
func Test_modeFraming_subagent_and_background(t *testing.T) {
	if got := modeFraming(ModeSubagent); !strings.Contains(got, "nested") {
		t.Errorf("ModeSubagent framing missing 'nested': %q", got)
	}
	if got := modeFraming(ModeBackground); !strings.Contains(got, "background") {
		t.Errorf("ModeBackground framing missing 'background': %q", got)
	}
	if got := modeFraming(ModeLongLived); got != "" {
		t.Errorf("ModeLongLived framing = %q, want empty", got)
	}
}
