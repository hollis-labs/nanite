package api

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
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

// persistPriorAPIInstance supplies a private already-authorized owner journal,
// without invoking or softening the unsupported production issuer.
func persistPriorAPIInstance(t *testing.T, st *store.Store, inst *store.DurableAgentInstance) {
	t.Helper()
	if _, err := st.GetAgentForActor(t.Context(), inst.ProfileID); err != nil {
		t.Fatal(err)
	}
	if inst.ID == "" {
		inst.ID = "prior-api-" + inst.Slug
	}
	if inst.LifecycleClass == "" {
		inst.LifecycleClass = store.DurableAgentClassAdvisor
	}
	if inst.RuntimeKind == "" {
		inst.RuntimeKind = "api"
	}
	if inst.LaunchSourceType == "" {
		inst.LaunchSourceType = store.DurableAgentLaunchDurableAdvisor
	}
	if inst.Status == "" {
		inst.Status = store.DurableAgentStatusSleeping
	}
	if inst.MetadataJSON == "" {
		inst.MetadataJSON = "{}"
	}
	if inst.URN == "" {
		inst.URN = inst.ProfileID
	}
	_, err := st.DB.ExecContext(t.Context(), `INSERT INTO actor_instances(id,name,slug,profile_id,lifecycle_class,provider,model,runtime_kind,launch_source_type,launch_source_id,work_root,status,current_session_id,failure_reason,metadata_json,urn) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, inst.ID, inst.Name, inst.Slug, inst.ProfileID, inst.LifecycleClass, inst.Provider, inst.Model, inst.RuntimeKind, inst.LaunchSourceType, inst.LaunchSourceID, inst.WorkRoot, inst.Status, inst.CurrentSessionID, inst.FailureReason, inst.MetadataJSON, inst.URN)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.GetDurableAgentInstance(t.Context(), inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	*inst = *saved
}
