package store

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

func TestTrustCannotComeFromHostDefinitionOrClaimedIdentity(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "trust-host")
	a := makeTestAgent(t, s, "trust-actor")
	for _, id := range []string{h.ID, a.ID, "missing", "msg://agent/claimed/actor"} {
		tier, err := s.ResolveTrust(t.Context(), id)
		if !errors.Is(err, ErrVerifiedActorRequired) || tier != dispatch.TrustUntrusted {
			t.Fatal("trust authority fabricated", id, tier, err)
		}
		if err = s.SetAgentDefaultTrustTier(t.Context(), id, "trusted"); !errors.Is(err, ErrImmutableAgentProfile) {
			t.Fatal("trust writer admitted", err)
		}
	}
}
