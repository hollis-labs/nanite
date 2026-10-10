package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredPromptrouterPhrasesCannotReviveHistoricalDispatch(t *testing.T) {
	cases := []struct{ name, message, target string }{
		{"background-long-task", "index the whole codebase in the background", "worker"},
		{"planner-mention", "let's plan out the full migration end-to-end starting next sprint", "planner"},
		{"planner-large-task", "refactor this into multiple phases, it's a big project", "planner"},
		{"researcher-mention", "please look into the auth flow errors", "researcher"},
		{"reviewer-mention", "can you review this PR", "reviewer"},
		{"worker-execute", "please fix the login bug", "worker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, actor, session := newRetiredDispatchFixture(t, "retired-"+tc.name)
			insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "dispatch_to_agent_" + tc.name, ActionSpec: `{"agent_slug":"` + tc.target + `","confidence":0.99,"reason":"retained phrase rule"}`, TriggerSpec: `{"kind":"user_regex_window","window":1,"pattern":".*"}`, Priority: 99})
			assertRetainedDispatchInert(t, st, actor.ID, session.ID, tc.message, classifiedRetiredDispatchTurn(t, session.ID, tc.message))
		})
	}
}
