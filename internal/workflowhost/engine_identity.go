package workflowhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
)

type WorkflowEngineIdentity struct {
	Kind            string
	ContractVersion string
}

func (s *WorkflowStateStore) LoadRunEngineIdentity(ctx context.Context, runID workflowruntime.RunID) (WorkflowEngineIdentity, error) {
	if err := checkWorkflowContext(ctx); err != nil {
		return WorkflowEngineIdentity{}, err
	}
	var identity WorkflowEngineIdentity
	err := s.db.QueryRowContext(ctx, `SELECT engine_kind, engine_contract_version FROM workflow_runs WHERE id = ?`, runID).Scan(&identity.Kind, &identity.ContractVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowEngineIdentity{}, fmt.Errorf("%w: workflow run %q", workflowruntime.ErrNotFound, runID)
	}
	if err != nil {
		return WorkflowEngineIdentity{}, fmt.Errorf("load workflow engine identity: %w", err)
	}
	return identity, nil
}
