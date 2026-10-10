package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// This test-only lifecycle port supplies a private, already authorized owner
// journal. It exercises shared-host execution without adopting a production
// issuer, promoting historical profiles, or softening DurableAgentService.Create.
type priorWorkflowLifecycleFixture struct {
	DurableAgentService
	st *store.Store
}

func newPriorWorkflowLifecycleFixture(st *store.Store) DurableAgentService {
	return &priorWorkflowLifecycleFixture{DurableAgentService: NewDurableAgentService(st), st: st}
}
func (f *priorWorkflowLifecycleFixture) Create(ctx context.Context, inst *store.DurableAgentInstance) error {
	return persistTestDurableInstance(ctx, f.st, inst)
}

func (f *priorWorkflowLifecycleFixture) CheckInstanceCreation(ctx context.Context, actor string) error {
	_, err := f.st.GetAgentForActor(ctx, actor)
	return err
}
