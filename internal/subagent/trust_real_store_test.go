package subagent

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestAuthorizerReturnsExplicitRefusalForMissingAuthority(t *testing.T) {
	_, st := newTestDB(t)
	for _, authorizer := range []Authorizer{{}, {st}, {failingSpawnResolver{}}} {
		for _, id := range []string{"", "claimed-host-uuid", "msg://agent/claimed"} {
			decision, err := authorizer.AuthorizeSpawn(t.Context(), id)
			if (err != nil && !errors.Is(err, store.ErrVerifiedActorRequired)) || decision.BypassApproval || !errors.Is(decision.Refusal, store.ErrVerifiedActorRequired) {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
		}
	}
}

type failingSpawnResolver struct{}

func (failingSpawnResolver) ResolveTrust(context.Context, string) (dispatch.TrustTier, error) {
	return dispatch.TrustTrusted, store.ErrVerifiedActorRequired
}

func TestAuthorizerPreservesResolverFailureWithExplicitRefusal(t *testing.T) {
	decision, err := (Authorizer{failingSpawnResolver{}}).AuthorizeSpawn(t.Context(), "msg://agent/claimed")
	if !errors.Is(err, store.ErrVerifiedActorRequired) || !errors.Is(decision.Refusal, err) || decision.BypassApproval {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
