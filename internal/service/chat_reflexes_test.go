package service

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestFormatReflexReminder_ForceToolChoiceOptIn pins the soft/opt-in contract
// for force_tool_choice: by default it renders a soft preference nudge; it only
// becomes an imperative directive when the reflex explicitly opts in
// (enforce=true or mode="hard"). inject_reminder renders its body verbatim.
func TestFormatReflexReminder_ForceToolChoiceOptIn(t *testing.T) {
	cases := []struct {
		name      string
		actions   []reflexes.AppliedAction
		wantSub   []string // substrings that must appear
		wantNone  []string // substrings that must NOT appear
		wantEmpty bool
	}{
		{
			name: "inject_reminder renders body",
			actions: []reflexes.AppliedAction{{
				ReflexName: "ground",
				ActionKind: store.ReflexActionInjectReminder,
				Spec:       map[string]interface{}{"body": "re-ground before claiming"},
			}},
			wantSub: []string{"<system-reminder>", "Reflex ground: re-ground before claiming"},
		},
		{
			name: "force_tool_choice default is soft preference",
			actions: []reflexes.AppliedAction{{
				ReflexName: "use-validate",
				ActionKind: store.ReflexActionForceToolChoice,
				Spec:       map[string]interface{}{"tool_name": "tool_validate"},
			}},
			wantSub:  []string{"prefer calling the `tool_validate` tool next"},
			wantNone: []string{"now, before any other response"},
		},
		{
			name: "force_tool_choice enforce=true is hard directive",
			actions: []reflexes.AppliedAction{{
				ReflexName: "must-validate",
				ActionKind: store.ReflexActionForceToolChoice,
				Spec:       map[string]interface{}{"tool_name": "tool_validate", "enforce": true},
			}},
			wantSub:  []string{"call the `tool_validate` tool now, before any other response"},
			wantNone: []string{"prefer calling"},
		},
		{
			name: "force_tool_choice mode=hard is hard directive",
			actions: []reflexes.AppliedAction{{
				ReflexName: "must-validate",
				ActionKind: store.ReflexActionForceToolChoice,
				Spec:       map[string]interface{}{"tool_name": "tool_validate", "mode": "hard"},
			}},
			wantSub: []string{"now, before any other response"},
		},
		{
			name: "force_tool_choice without tool_name is skipped",
			actions: []reflexes.AppliedAction{{
				ReflexName: "noop",
				ActionKind: store.ReflexActionForceToolChoice,
				Spec:       map[string]interface{}{},
			}},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatReflexReminder(tc.actions)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty, got %q", got)
				}
				return
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("missing %q in:\n%s", sub, got)
				}
			}
			for _, sub := range tc.wantNone {
				if strings.Contains(got, sub) {
					t.Errorf("unexpected %q in:\n%s", sub, got)
				}
			}
		})
	}
}
