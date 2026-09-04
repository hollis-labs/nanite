package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ResolveWorkflowDefinitionRevision identifies the sole immutable revision
// matching exact authored name, compiled plan, and engine contract. Hosts use
// this tuple inside the same transaction that inserts workflow_runs; they must
// never recover identity through the mutable definition head.
func (s *Store) ResolveWorkflowDefinitionRevision(
	ctx context.Context,
	definitionName string,
	compiledPlanDigest string,
	engine WorkflowEngineIdentity,
) (WorkflowDefinitionRevision, error) {
	if strings.TrimSpace(definitionName) == "" || strings.TrimSpace(compiledPlanDigest) == "" ||
		!SupportedEmbeddedWorkflowEngineIdentity(engine) {
		return WorkflowDefinitionRevision{}, fmt.Errorf("resolve workflow definition revision: exact definition, plan digest, and supported engine are required")
	}
	revision, err := scanWorkflowDefinitionRevision(s.DB.QueryRowContext(ctx, `
SELECT `+workflowDefinitionRevisionColumns+`
FROM workflow_definition_revisions
WHERE definition_name = ? AND compiled_plan_digest = ?
  AND engine_kind = ? AND engine_contract_version = ?`,
		definitionName, compiledPlanDigest, engine.Kind, engine.ContractVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowDefinitionRevision{}, ErrWorkflowDefinitionRevisionNotFound
	}
	if err != nil {
		return WorkflowDefinitionRevision{}, fmt.Errorf("resolve workflow definition revision: %w", err)
	}
	return revision, nil
}

// GetWorkflowRunDefinitionRevision follows the immutable run association.
// A NULL association is reported as ErrWorkflowDefinitionRevisionNotFound;
// callers must not fall back to the mutable definition head.
func (s *Store) GetWorkflowRunDefinitionRevision(ctx context.Context, runID string) (WorkflowDefinitionRevision, error) {
	if strings.TrimSpace(runID) == "" {
		return WorkflowDefinitionRevision{}, ErrWorkflowDefinitionRevisionNotFound
	}
	revision, err := scanWorkflowDefinitionRevision(s.DB.QueryRowContext(ctx, `
SELECT `+workflowDefinitionRevisionColumns+`
FROM workflow_definition_revisions
WHERE revision_id = (
    SELECT definition_revision_id FROM workflow_runs WHERE id = ?
)`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowDefinitionRevision{}, ErrWorkflowDefinitionRevisionNotFound
	}
	if err != nil {
		return WorkflowDefinitionRevision{}, fmt.Errorf("get workflow run definition revision: %w", err)
	}
	return revision, nil
}
