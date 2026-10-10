package loop

import (
	"context"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// This private test port represents an already authorized lifecycle owner.
// It neither enables the production issuer nor promotes retained profiles.
type priorLoopLifecycleFixture struct {
	service.DurableAgentService
	st *store.Store
}

func newPriorLoopLifecycleFixture(st *store.Store) service.DurableAgentService {
	return &priorLoopLifecycleFixture{service.NewDurableAgentService(st), st}
}
func (f *priorLoopLifecycleFixture) Create(ctx context.Context, inst *store.DurableAgentInstance) error {
	return persistPriorLoopInstance(ctx, f.st, inst)
}
func (f *priorLoopLifecycleFixture) CheckInstanceCreation(ctx context.Context, actor string) error {
	_, err := f.st.GetAgentForActor(ctx, actor)
	return err
}

func persistPriorLoopInstance(ctx context.Context, st *store.Store, inst *store.DurableAgentInstance) error {
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
