package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// persistTestActor supplies an ALREADY host-authorized binding to a private
// database. Production has no actor issuer port; this fixture does not claim
// enrollment or convert retained rows. The tested reads use real fresh FKs.
func persistTestActor(ctx context.Context, st *store.Store, p *store.AgentProfile) error {
	return storetest.PriorAuthorizedActor(ctx, st, p)
}

// grantTestActorTool represents a prior host-issued grant; it deliberately does
// not call or soften the refused production grant issuer.
func grantTestActorTool(ctx context.Context, st *store.Store, actor, toolID, via string) error {
	_, err := st.DB.ExecContext(ctx, `INSERT INTO actor_granted_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,?,datetime('now'))`, actor, toolID, via)
	return err
}

// persistTestDurableInstance supplies a previously host-authorized instance
// journal in a private DB. It never calls the unsupported production issuer or
// promotes historical profiles. ProfileID must already be a verified actor.
func persistTestDurableInstance(ctx context.Context, st *store.Store, inst *store.DurableAgentInstance) error {
	if _, err := st.GetAgentForActor(ctx, inst.ProfileID); err != nil {
		return err
	}
	if inst.ID == "" {
		inst.ID = "private-instance-" + inst.Slug
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
	_, err := st.DB.ExecContext(ctx, `INSERT INTO actor_instances(id,name,slug,profile_id,lifecycle_class,provider,model,runtime_kind,launch_source_type,launch_source_id,work_root,status,current_session_id,failure_reason,metadata_json,urn) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		inst.ID, inst.Name, inst.Slug, inst.ProfileID, inst.LifecycleClass, inst.Provider, inst.Model, inst.RuntimeKind, inst.LaunchSourceType, inst.LaunchSourceID, inst.WorkRoot, inst.Status, inst.CurrentSessionID, inst.FailureReason, inst.MetadataJSON, inst.URN)
	if err != nil {
		return err
	}
	saved, err := st.GetDurableAgentInstance(ctx, inst.ID)
	if err == nil {
		*inst = *saved
	}
	return err
}
