package workflowhost

import (
	"context"
	"fmt"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// reconcileExternalOperations advances the shared runtime's durable external
// operations. The subprocess Observer is called only through this coordinator,
// after SuspendExternalOperation has atomically released the worker claim.
func (e *Engine) reconcileExternalOperations(
	ctx context.Context,
	runID workflowruntime.RunID,
	registry *frozenRegistry,
	limit int,
) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	verifiers, _, _, err := currentVerifierRegistry()
	if err != nil {
		return 0, err
	}
	coordinator, err := workflowruntime.NewExternalOperationCoordinator(workflowruntime.ExternalOperationOptions{
		Store: e.store, Registry: registry, Verifiers: verifiers,
	})
	if err != nil {
		return 0, err
	}
	operations, err := coordinator.Recover(ctx, workflowruntime.ExternalOperationQuery{RunID: runID, Limit: limit})
	if err != nil {
		return 0, err
	}
	terminal := 0
	for _, operation := range operations {
		result, reconcileErr := coordinator.Reconcile(ctx, operation.Attempt)
		if reconcileErr != nil {
			if concurrentRuntimeProgress(reconcileErr) {
				continue
			}
			return terminal, fmt.Errorf("reconcile external execution %s: %w", operation.Ref.ID, reconcileErr)
		}
		if result.Operation.Status != stepkind.ObservationPending {
			terminal++
		}
	}
	if terminal == 0 {
		return 0, nil
	}
	run, err := e.store.LoadRun(context.WithoutCancel(ctx), runID)
	if err != nil {
		return terminal, err
	}
	if run.Status == workflowruntime.RunWaiting {
		_, err = e.store.TransitionRun(context.WithoutCancel(ctx), workflowruntime.RunTransitionRequest{
			RunID: run.ID, ExpectedGeneration: run.Generation,
			To: workflowruntime.RunRunning, At: maxTime(time.Now().UTC(), run.UpdatedAt),
		})
		if err != nil && !concurrentRuntimeProgress(err) {
			return terminal, err
		}
	}
	return terminal, nil
}

// ResolveExternalExecution records one authenticated operator decision for an
// ambiguous subprocess receipt, then uses the shared runtime coordinator to
// close the external operation and continue the run. Replays with the exact
// resolution idempotency key are safe; conflicting assertions fail closed.
func (e *Engine) ResolveExternalExecution(
	ctx context.Context,
	request ExternalExecutionResolutionRequest,
	exec agentworkflow.StepExecutor,
) (agentworkflow.WorkflowResult, error) {
	receipt, err := e.store.resolveExternalExecution(ctx, request)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	registry, err := e.registryForRun(ctx, workflowruntime.RunID(receipt.RunID), exec)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if _, err := e.reconcileExternalOperations(ctx, workflowruntime.RunID(receipt.RunID), registry, 100); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	return e.Resume(ctx, receipt.RunID, exec)
}

// AmbiguousExternalExecutions is the operator discovery seam. The returned
// receipts contain persisted private request data and must remain behind
// Nanite's authenticated administration boundary.
func (e *Engine) AmbiguousExternalExecutions(ctx context.Context, limit int) ([]ExternalExecutionReceipt, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > workflowruntime.MaximumRunQueryLimit {
		return nil, fmt.Errorf("external execution receipt limit %d exceeds maximum %d", limit, workflowruntime.MaximumRunQueryLimit)
	}
	rows, err := e.store.db.QueryContext(ctx, externalExecutionReceiptSelect+`
WHERE state=? ORDER BY updated_at,idempotency_key LIMIT ?`, ExternalExecutionAmbiguous, limit)
	if err != nil {
		return nil, fmt.Errorf("list ambiguous external executions: %w", err)
	}
	defer closeRows(rows)
	result := make([]ExternalExecutionReceipt, 0)
	for rows.Next() {
		receipt, scanErr := scanExternalExecutionReceipt(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list ambiguous external executions: %w", err)
	}
	return result, nil
}
