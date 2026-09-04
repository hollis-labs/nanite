package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	workflowapi "github.com/hollis-labs/nanite/internal/workflowapi"
	"github.com/hollis-labs/nanite/internal/workflowcompat"
)

type workflowExternalExecutionAdmin interface {
	ListAmbiguousExternalExecutions(context.Context, int) ([]workflowcompat.ExternalExecutionSummary, error)
	ResolveExternalExecution(context.Context, workflowcompat.ExternalExecutionResolution) (*workflowapi.RunRecord, error)
}

type workflowExternalExecutionResolutionBody struct {
	Action string `json:"action"`
	Output string `json:"output"`
	Reason string `json:"reason"`
}

func (a *API) handleListAmbiguousWorkflowExternalExecutions(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateWorkflowOperator(w, r); !ok {
		return
	}
	if a.workflowExternalExecutionAdmin == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "workflow external execution administration is not configured")
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			a.errorResp(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	receipts, err := a.workflowExternalExecutionAdmin.ListAmbiguousExternalExecutions(r.Context(), limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list ambiguous workflow external executions")
		return
	}
	if receipts == nil {
		receipts = []workflowcompat.ExternalExecutionSummary{}
	}
	a.jsonResp(w, http.StatusOK, receipts)
}

func (a *API) handleResolveWorkflowExternalExecution(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticateWorkflowOperator(w, r)
	if !ok {
		return
	}
	if a.workflowExternalExecutionAdmin == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "workflow external execution administration is not configured")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		a.errorResp(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}
	var body workflowExternalExecutionResolutionBody
	if err := a.decode(r, &body); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid workflow external execution resolution body")
		return
	}
	body.Action, body.Reason = strings.TrimSpace(body.Action), strings.TrimSpace(body.Reason)
	if (body.Action != "complete_success" && body.Action != "complete_failure") || body.Reason == "" {
		a.errorResp(w, http.StatusBadRequest, "action must be complete_success or complete_failure and reason is required")
		return
	}
	record, err := a.workflowExternalExecutionAdmin.ResolveExternalExecution(r.Context(), workflowcompat.ExternalExecutionResolution{
		ReceiptID: r.PathValue("receiptId"), Action: body.Action, Output: body.Output,
		Principal: principal, Reason: body.Reason, IdempotencyKey: idempotencyKey,
		ResolvedAt: time.Now().UTC(),
	})
	if err != nil {
		switch {
		case errors.Is(err, workflowcompat.ErrNotFound):
			a.errorResp(w, http.StatusNotFound, "workflow external execution receipt not found")
		case errors.Is(err, workflowcompat.ErrConflict):
			a.errorResp(w, http.StatusConflict, "workflow external execution resolution conflicts with durable state")
		case errors.Is(err, workflowcompat.ErrInvalidSource):
			a.errorResp(w, http.StatusBadRequest, "invalid workflow external execution resolution")
		default:
			a.errorResp(w, http.StatusInternalServerError, "failed to resolve workflow external execution")
		}
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"run_id": record.Run.RunID, "pipeline_id": record.Run.PipelineID,
		"status": string(record.Run.Status),
	})
}

func (a *API) authenticateWorkflowOperator(w http.ResponseWriter, r *http.Request) (string, bool) {
	if a.workflowResponderAuthenticator == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "workflow responder authentication is not configured")
		return "", false
	}
	principal, authenticated := a.workflowResponderAuthenticator(r)
	principal = strings.TrimSpace(principal)
	if !authenticated || principal == "" {
		a.errorResp(w, http.StatusUnauthorized, "workflow responder authentication required")
		return "", false
	}
	return principal, true
}
