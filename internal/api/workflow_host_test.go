package api

import (
	"context"
	"testing"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

type apiTestTeamStepHost struct{}

func (apiTestTeamStepHost) ResolveWorkflowTeamStep(context.Context, workflowhost.TeamStepResolveRequest) (workflowhost.TeamStepResolveResult, error) {
	return workflowhost.TeamStepResolveResult{}, nil
}

func newAPITestWorkflowHost(t *testing.T, product *store.Store) *workflowhost.Engine {
	t.Helper()
	state, err := workflowhost.NewWorkflowStateStore(product)
	if err != nil {
		t.Fatalf("NewWorkflowStateStore: %v", err)
	}
	engine, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	engine.WithWaitCoordinator(&workflowruntime.WaitCoordinator{
		Store: state, Authorizer: workflowhost.NaniteResponderAuthorizer{},
	}).WithTeamStepHost(apiTestTeamStepHost{})
	return engine
}
