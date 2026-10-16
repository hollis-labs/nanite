package store

import (
	"errors"
	"testing"
)

func TestFreshAgentFilterRefusesRetiredActivationAndTagSemantics(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "filter-host")
	profiles, err := s.ListAgentsFilter(t.Context(), "advisor", "", "active", nil)
	if err != nil || len(profiles) != 1 || profiles[0].ID != h.ID {
		t.Fatal(profiles, err)
	}
	for _, input := range []struct {
		activation string
		tags       []string
	}{{"singleton", nil}, {"", []string{"trusted"}}} {
		if _, err := s.ListAgentsFilter(t.Context(), "", input.activation, "", input.tags); !errors.Is(err, ErrImmutableAgentProfile) {
			t.Fatal("retired selection silently interpreted", err)
		}
	}
}
