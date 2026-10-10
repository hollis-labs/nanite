package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredDispatchMultipleCandidatesDoNotFailOpenUnderDegradedCombiner(t *testing.T) {
	st, actor, session := newRetiredDispatchFixture(t, "retired-multi")
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "first-retained-candidate", Priority: 99, FiredCount: 2})
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "second-retained-candidate", Priority: 98, FiredCount: 5})
	// A degraded private combining policy cannot turn retained content into candidates.
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE reflex_action_kinds SET combining_algorithm='all_applicable' WHERE name='dispatch_to_agent'`); err != nil {
		t.Fatal(err)
	}
	const message = "probe-multi-candidate-token please route this"
	assertRetainedDispatchInert(t, st, actor.ID, session.ID, message, classifiedRetiredDispatchTurn(t, session.ID, message))
}

func TestRetiredDispatchMalformedRulesAndClassificationStayInert(t *testing.T) {
	st, actor, session := newRetiredDispatchFixture(t, "retired-malformed")
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "invalid-trigger", TriggerSpec: `not-json`, Priority: 999})
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "invalid-action", ActionSpec: `not-json`, Priority: 998})
	insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "unknown-predicate", TriggerSpec: `{"kind":"unknown-private-predicate"}`, Priority: 997})
	assertRetainedDispatchInert(t, st, actor.ID, session.ID, "please route this", classifiedRetiredDispatchTurn(t, session.ID, "please route this"))
	// The pre-loop classification guard remains a no-op when it is absent.
	assertRetainedDispatchInert(t, st, actor.ID, session.ID, "please route this", nil)
}
