package api

import (
	"context"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"testing"
)

// fixtureNativeModels is explicit private host model authorization, independent
// of definition claims and the operator's real provider configuration.
func fixtureNativeModels() service.ModelAuthorizer {
	return service.ModelAuthorizerFunc(func(_ context.Context, _ service.DefinitionRef, requested *service.ModelSelection) (service.ModelSelection, error) {
		selection := service.ModelSelection{Provider: "private-api-fixture", Model: "private-model"}
		if requested != nil && *requested != selection {
			return service.ModelSelection{}, service.ErrUnsupportedModel
		}
		return selection, nil
	})
}

// bindFixtureSessionActor supplies a prior-authorized binding in a private DB.
// It is neither production enrollment nor conversion of an old profile.
func bindFixtureSessionActor(t *testing.T, st *store.Store, session string) *store.AgentProfile {
	t.Helper()
	actor := &store.AgentProfile{Name: "Private compaction actor", Slug: "private-compact-" + session, SystemPrompt: "Private immutable context.", DefaultProvider: "private-api-fixture", DefaultModel: "private-model"}
	if err := storetest.PriorAuthorizedActor(t.Context(), st, actor); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSessionAgent(t.Context(), session, actor.ID, "", true); err != nil {
		t.Fatal(err)
	}
	return actor
}
