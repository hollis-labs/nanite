package selftools

// Retained mutable dispatch rules are historical data; the matcher must leave them inert.

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestMatchDispatchToAgentReflex_RecurrenceOverride_SuppressesRefire(t *testing.T) {
	for _, lastFired := range []string{"", "2099-10-10T00:00:00Z", "2020-01-01T00:00:00Z"} {
		t.Run("last-fired-"+lastFired, func(t *testing.T) {
			s := newTestStore(t)
			historical := retainedSelftoolsDispatchProfile(t, s, "retained-dispatch-cooldown")
			override := int64(3600)
			row := store.AgentReflex{ID: "retained-cooldown", Name: "retained-cooldown", AgentID: historical.ID, RecurrenceOverrideSeconds: &override, LastFiredAt: lastFired, FiredCount: 17}
			retainedSelftoolsDispatchRule(t, s, row)
			st := newTestSelfToolsTransport(s)
			before := selftoolsDispatchSnapshot(t, s)
			for range 3 {
				if hints := st.matchDispatchToAgentReflex(t.Context(), "sess-cooldown", historical.ID, "probe-cooldown-token please route this"); hints != nil {
					t.Fatalf("historical cooldown rearmed routing: %+v", hints)
				}
			}
			if id, err := s.InsertAgentReflex(t.Context(), row); id != "" || !errors.Is(err, store.ErrImmutableAgentProfile) {
				t.Fatalf("retired rule authoring = %q, %v", id, err)
			}
			if err := s.UpdateAgentReflex(t.Context(), row); !errors.Is(err, store.ErrImmutableAgentProfile) {
				t.Fatalf("retired cooldown update = %v", err)
			}
			requireSelftoolsDispatchUnchanged(t, s, before)
		})
	}
}
