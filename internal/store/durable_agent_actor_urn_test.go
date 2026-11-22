package store

import (
	"errors"
	"testing"
)

func TestDurableActorCreationAndClaimedIdentityRefuseWithoutIssuer(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "durable-refusal")
	for _, claimed := range []string{"", "msg://agent/nanite/claimed", "msg://agent/agent-mux/claimed"} {
		input := &DurableAgentInstance{Name: "Claimed", Slug: "claimed", ProfileID: h.ID, URN: claimed}
		if err := s.CreateDurableAgentInstance(t.Context(), input); !errors.Is(err, ErrVerifiedActorRequired) {
			t.Fatal(err)
		}
		if _, err := s.SyncDurableAgentInstanceConfig(t.Context(), input); !errors.Is(err, ErrVerifiedActorRequired) {
			t.Fatal(err)
		}
		if input.ID != "" || input.URN != claimed {
			t.Fatal("refusal minted or changed identity", input)
		}
	}
	instances, err := s.ListDurableAgentInstances(t.Context(), true)
	if err != nil || len(instances) != 0 {
		t.Fatal(instances, err)
	}
}
