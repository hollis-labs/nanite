package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredDispatchCooldownStateCannotRearmHistoricalRule(t *testing.T) {
	for _, lastFired := range []string{"", "2099-10-10T00:00:00Z", "2020-01-01T00:00:00Z"} {
		t.Run("last-fired-"+lastFired, func(t *testing.T) {
			st, actor, session := newRetiredDispatchFixture(t, "retired-cooldown")
			override := int64(3600)
			insertRetainedDispatchRule(t, st, store.AgentReflex{Name: "retained-cooldown", RecurrenceOverrideSeconds: &override, LastFiredAt: lastFired, FiredCount: 17})
			const message = "probe-cooldown-token please route this"
			for range 3 {
				assertRetainedDispatchInert(t, st, actor.ID, session.ID, message, classifiedRetiredDispatchTurn(t, session.ID, message))
			}
		})
	}
}
