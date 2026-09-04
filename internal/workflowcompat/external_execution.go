package workflowcompat

import (
	"context"
	"errors"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"

	workflowapi "github.com/hollis-labs/nanite/internal/workflowapi"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

// ExternalExecutionSummary is the deliberately redacted operator view of an
// ambiguous subprocess receipt. Params, session identity, and any output are
// private workflow data and never cross the list boundary.
type ExternalExecutionSummary struct {
	ReceiptID     string    `json:"receipt_id"`
	RunID         string    `json:"run_id"`
	NodeID        string    `json:"node_id"`
	Engine        string    `json:"engine"`
	WorkflowName  string    `json:"workflow_name"`
	RequestDigest string    `json:"request_digest"`
	State         string    `json:"state"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExternalExecutionResolution is the authenticated API assertion used to
// resolve an otherwise permanently ambiguous effect.
type ExternalExecutionResolution struct {
	ReceiptID      string
	Action         string
	Output         string
	Principal      string
	Reason         string
	IdempotencyKey string
	ResolvedAt     time.Time
}

func (h *SharedSurface) ListAmbiguousExternalExecutions(ctx context.Context, limit int) ([]ExternalExecutionSummary, error) {
	receipts, err := h.engine.AmbiguousExternalExecutions(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]ExternalExecutionSummary, 0, len(receipts))
	for _, receipt := range receipts {
		result = append(result, ExternalExecutionSummary{
			ReceiptID: receipt.ID, RunID: receipt.RunID, NodeID: receipt.NodeID,
			Engine: receipt.Request.Engine, WorkflowName: receipt.Request.WorkflowName,
			RequestDigest: receipt.RequestDigest, State: string(receipt.State), Reason: receipt.Ambiguity,
			CreatedAt: receipt.CreatedAt, UpdatedAt: receipt.UpdatedAt,
		})
	}
	return result, nil
}

func (h *SharedSurface) ResolveExternalExecution(ctx context.Context, resolution ExternalExecutionResolution) (*workflowapi.RunRecord, error) {
	result, err := h.engine.ResolveExternalExecution(ctx, workflowhost.ExternalExecutionResolutionRequest{
		ReceiptID: resolution.ReceiptID, Action: resolution.Action, Output: resolution.Output,
		AuthenticatedPrincipal: resolution.Principal, Reason: resolution.Reason,
		IdempotencyKey: resolution.IdempotencyKey, ResolvedAt: resolution.ResolvedAt,
	}, h.executor)
	if err != nil {
		switch {
		case errors.Is(err, workflowruntime.ErrNotFound):
			return nil, ErrNotFound
		case errors.Is(err, workflowruntime.ErrIdempotencyConflict), errors.Is(err, workflowruntime.ErrTransitionConflict):
			return nil, ErrConflict
		case errors.Is(err, workflowruntime.ErrInvalidRecord):
			return nil, ErrInvalidSource
		default:
			return nil, err
		}
	}
	return h.Get(context.WithoutCancel(ctx), result.RunID)
}
