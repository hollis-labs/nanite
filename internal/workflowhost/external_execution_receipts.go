package workflowhost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
)

// ExternalExecutionState describes the Nanite subprocess boundary, not the
// shared runtime operation lifecycle. Prepared means no effect has started;
// pending means the call may have escaped; completed is a durable exact
// result; ambiguous requires an audited operator resolution.
type ExternalExecutionState string

const (
	ExternalExecutionPrepared  ExternalExecutionState = "prepared"
	ExternalExecutionPending   ExternalExecutionState = "pending"
	ExternalExecutionCompleted ExternalExecutionState = "completed"
	ExternalExecutionAmbiguous ExternalExecutionState = "ambiguous"
)

const (
	ExternalResolutionSuccess = "complete_success"
	ExternalResolutionFailure = "complete_failure"
)

type externalExecutionRequest struct {
	Request       ExternalStepRequest `json:"request"`
	ProductStepID string              `json:"product_step_id"`
	ProductKind   string              `json:"product_kind"`
	NodeID        string              `json:"node_id"`
}

// ExternalExecutionReceipt is the immutable request and durable outcome for
// one keyed external subprocess effect. RequestJSON is deliberately not
// exposed: callers receive the typed request after exact decoding.
type ExternalExecutionReceipt struct {
	ID               string
	RunID            string
	NodeID           string
	Iteration        string
	RequestDigest    string
	Request          ExternalStepRequest
	ProductStepID    string
	ProductKind      string
	State            ExternalExecutionState
	Result           *ExternalStepResult
	Ambiguity        string
	ResolutionKey    string
	Resolution       string
	ResolutionActor  string
	ResolutionReason string
	ResolvedAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ExternalExecutionResolutionRequest is an authenticated operator assertion.
// Authentication is performed by the API boundary; the principal, reason,
// action, and idempotency key are persisted before runtime reconciliation.
type ExternalExecutionResolutionRequest struct {
	ReceiptID              string
	Action                 string
	Output                 string
	AuthenticatedPrincipal string
	Reason                 string
	IdempotencyKey         string
	ResolvedAt             time.Time
}

const externalExecutionReceiptSelect = `
SELECT idempotency_key,run_id,node_id,iteration,request_digest,request_json,state,
       output,is_error,ambiguity_reason,resolution_key,resolution_action,
       resolution_actor,resolution_reason,resolved_at,created_at,updated_at
FROM workflow_external_execution_receipts`

func (s *WorkflowStateStore) prepareExternalExecution(
	ctx context.Context,
	runID, nodeID, iteration string,
	request externalExecutionRequest,
	at time.Time,
) (ExternalExecutionReceipt, error) {
	encoded, digest, normalized, err := encodeExternalExecutionRequest(request)
	if err != nil {
		return ExternalExecutionReceipt{}, err
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(nodeID) == "" || at.IsZero() {
		return ExternalExecutionReceipt{}, workflowInvalid(errors.New("external execution preparation requires run, node, and timestamp"))
	}
	key := normalized.Request.IdempotencyKey
	var receipt ExternalExecutionReceipt
	err = s.write(ctx, "prepare workflow external execution", func(query workflowSQL) error {
		current, loadErr := loadExternalExecutionReceipt(ctx, query, key)
		if loadErr == nil {
			if !externalExecutionBindingEqual(current, runID, nodeID, iteration, digest, normalized) {
				return workflowIdempotencyConflict("prepare workflow external execution", key)
			}
			receipt = current
			return nil
		}
		if !errors.Is(loadErr, workflowruntime.ErrNotFound) {
			return loadErr
		}
		_, insertErr := query.ExecContext(ctx, `
INSERT INTO workflow_external_execution_receipts(
    idempotency_key,run_id,node_id,iteration,request_digest,request_json,state,
    created_at,updated_at
) VALUES(?,?,?,?,?,?,?, ?,?)`, key, runID, nodeID, iteration, digest, encoded,
			ExternalExecutionPrepared, workflowTime(at), workflowTime(at))
		if insertErr != nil {
			return fmt.Errorf("insert workflow external execution receipt: %w", insertErr)
		}
		receipt = ExternalExecutionReceipt{
			ID: key, RunID: runID, NodeID: nodeID, Iteration: iteration,
			RequestDigest: digest, Request: normalized.Request,
			ProductStepID: normalized.ProductStepID, ProductKind: normalized.ProductKind,
			State: ExternalExecutionPrepared, CreatedAt: at.UTC(), UpdatedAt: at.UTC(),
		}
		return nil
	})
	return receipt, err
}

func (s *WorkflowStateStore) beginExternalExecution(ctx context.Context, id, expectedDigest string, at time.Time) (ExternalExecutionReceipt, bool, error) {
	var receipt ExternalExecutionReceipt
	call := false
	err := s.write(ctx, "begin workflow external execution", func(query workflowSQL) error {
		current, err := loadExternalExecutionReceipt(ctx, query, id)
		if err != nil {
			return err
		}
		if current.RequestDigest != expectedDigest {
			return workflowIdempotencyConflict("begin workflow external execution", id)
		}
		switch current.State {
		case ExternalExecutionPrepared:
			if _, err := query.ExecContext(ctx, `
UPDATE workflow_external_execution_receipts
SET state=?,updated_at=? WHERE idempotency_key=? AND state=?`,
				ExternalExecutionPending, workflowTime(at), id, ExternalExecutionPrepared); err != nil {
				return fmt.Errorf("begin workflow external execution: %w", err)
			}
			current.State, current.UpdatedAt, call = ExternalExecutionPending, at.UTC(), true
		case ExternalExecutionPending:
			current.State = ExternalExecutionAmbiguous
			current.Ambiguity = "a prior subprocess call has no durable completion receipt"
			current.UpdatedAt = at.UTC()
			if err := updateExternalExecutionAmbiguous(ctx, query, current); err != nil {
				return err
			}
		case ExternalExecutionCompleted, ExternalExecutionAmbiguous:
		default:
			return workflowInvalid(fmt.Errorf("unsupported external execution state %q", current.State))
		}
		receipt = current
		return nil
	})
	return receipt, call, err
}

func (s *WorkflowStateStore) completeExternalExecution(ctx context.Context, id string, result ExternalStepResult, at time.Time) (ExternalExecutionReceipt, error) {
	var receipt ExternalExecutionReceipt
	err := s.write(ctx, "complete workflow external execution", func(query workflowSQL) error {
		current, err := loadExternalExecutionReceipt(ctx, query, id)
		if err != nil {
			return err
		}
		if current.State == ExternalExecutionCompleted {
			if current.Result == nil || *current.Result != result {
				return workflowIdempotencyConflict("complete workflow external execution", id)
			}
			receipt = current
			return nil
		}
		if current.State != ExternalExecutionPending && current.State != ExternalExecutionAmbiguous {
			return workflowInvalid(fmt.Errorf("external execution %q cannot complete from %q", id, current.State))
		}
		isError := 0
		if result.IsError {
			isError = 1
		}
		updated, err := query.ExecContext(ctx, `
UPDATE workflow_external_execution_receipts
SET state=?,output=?,is_error=?,updated_at=?
WHERE idempotency_key=? AND state IN (?,?)`, ExternalExecutionCompleted, result.Output,
			isError, workflowTime(at), id, ExternalExecutionPending, ExternalExecutionAmbiguous)
		if err != nil {
			return fmt.Errorf("complete workflow external execution: %w", err)
		}
		if err := expectOneExternalReceiptRow(updated, "complete workflow external execution"); err != nil {
			return err
		}
		current.State, current.Result, current.UpdatedAt = ExternalExecutionCompleted, &result, at.UTC()
		receipt = current
		return nil
	})
	return receipt, err
}

func (s *WorkflowStateStore) markExternalExecutionAmbiguous(ctx context.Context, id, reason string, at time.Time) (ExternalExecutionReceipt, error) {
	var receipt ExternalExecutionReceipt
	err := s.write(ctx, "mark workflow external execution ambiguous", func(query workflowSQL) error {
		current, err := loadExternalExecutionReceipt(ctx, query, id)
		if err != nil {
			return err
		}
		if current.State == ExternalExecutionCompleted || current.State == ExternalExecutionAmbiguous {
			receipt = current
			return nil
		}
		if current.State != ExternalExecutionPending {
			return workflowInvalid(fmt.Errorf("external execution %q cannot become ambiguous from %q", id, current.State))
		}
		current.State, current.Ambiguity, current.UpdatedAt = ExternalExecutionAmbiguous, strings.TrimSpace(reason), at.UTC()
		if current.Ambiguity == "" {
			current.Ambiguity = "subprocess outcome was not durably observed"
		}
		if err := updateExternalExecutionAmbiguous(ctx, query, current); err != nil {
			return err
		}
		receipt = current
		return nil
	})
	return receipt, err
}

// LoadExternalExecutionReceipt returns the exact operator-facing receipt.
func (s *WorkflowStateStore) LoadExternalExecutionReceipt(ctx context.Context, id string) (ExternalExecutionReceipt, error) {
	if err := checkWorkflowContext(ctx); err != nil {
		return ExternalExecutionReceipt{}, err
	}
	if strings.TrimSpace(id) == "" {
		return ExternalExecutionReceipt{}, workflowInvalid(errors.New("external execution receipt id is required"))
	}
	return loadExternalExecutionReceipt(ctx, s.db, id)
}

func (s *WorkflowStateStore) resolveExternalExecution(ctx context.Context, request ExternalExecutionResolutionRequest) (ExternalExecutionReceipt, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.AuthenticatedPrincipal) == "" ||
		strings.TrimSpace(request.Reason) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || request.ResolvedAt.IsZero() {
		return ExternalExecutionReceipt{}, workflowInvalid(errors.New("external resolution requires receipt, principal, reason, idempotency key, and timestamp"))
	}
	if request.Action != ExternalResolutionSuccess && request.Action != ExternalResolutionFailure {
		return ExternalExecutionReceipt{}, workflowInvalid(fmt.Errorf("unsupported external resolution action %q", request.Action))
	}
	result := ExternalStepResult{Output: request.Output, IsError: request.Action == ExternalResolutionFailure}
	var receipt ExternalExecutionReceipt
	err := s.write(ctx, "resolve workflow external execution", func(query workflowSQL) error {
		current, err := loadExternalExecutionReceipt(ctx, query, request.ReceiptID)
		if err != nil {
			return err
		}
		if current.State == ExternalExecutionCompleted {
			if current.ResolutionKey != request.IdempotencyKey || current.Resolution != request.Action ||
				current.ResolutionActor != request.AuthenticatedPrincipal || current.ResolutionReason != request.Reason ||
				current.Result == nil || *current.Result != result {
				return workflowIdempotencyConflict("resolve workflow external execution", request.IdempotencyKey)
			}
			receipt = current
			return nil
		}
		if current.State != ExternalExecutionAmbiguous {
			return workflowInvalid(fmt.Errorf("external execution %q requires ambiguous state for operator resolution", request.ReceiptID))
		}
		isError := 0
		if result.IsError {
			isError = 1
		}
		updated, err := query.ExecContext(ctx, `
UPDATE workflow_external_execution_receipts
SET state=?,output=?,is_error=?,resolution_key=?,resolution_action=?,
    resolution_actor=?,resolution_reason=?,resolved_at=?,updated_at=?
WHERE idempotency_key=? AND state=?`, ExternalExecutionCompleted, result.Output, isError,
			request.IdempotencyKey, request.Action, request.AuthenticatedPrincipal, request.Reason,
			workflowTime(request.ResolvedAt), workflowTime(request.ResolvedAt), request.ReceiptID, ExternalExecutionAmbiguous)
		if err != nil {
			return fmt.Errorf("resolve workflow external execution: %w", err)
		}
		if err := expectOneExternalReceiptRow(updated, "resolve workflow external execution"); err != nil {
			return err
		}
		current.State, current.Result = ExternalExecutionCompleted, &result
		current.ResolutionKey, current.Resolution = request.IdempotencyKey, request.Action
		current.ResolutionActor, current.ResolutionReason = request.AuthenticatedPrincipal, request.Reason
		current.ResolvedAt, current.UpdatedAt = request.ResolvedAt.UTC(), request.ResolvedAt.UTC()
		receipt = current
		return nil
	})
	return receipt, err
}

func loadExternalExecutionReceipt(ctx context.Context, query workflowSQL, id string) (ExternalExecutionReceipt, error) {
	return scanExternalExecutionReceipt(query.QueryRowContext(ctx, externalExecutionReceiptSelect+` WHERE idempotency_key=?`, id))
}

func scanExternalExecutionReceipt(row workflowScanner) (ExternalExecutionReceipt, error) {
	var (
		receipt                                            ExternalExecutionReceipt
		requestJSON, state, createdAt, updatedAt           string
		output, ambiguity, resolutionKey, resolutionAction sql.NullString
		resolutionActor, resolutionReason, resolvedAt      sql.NullString
		isError                                            sql.NullInt64
	)
	err := row.Scan(&receipt.ID, &receipt.RunID, &receipt.NodeID, &receipt.Iteration,
		&receipt.RequestDigest, &requestJSON, &state, &output, &isError, &ambiguity,
		&resolutionKey, &resolutionAction, &resolutionActor, &resolutionReason,
		&resolvedAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalExecutionReceipt{}, fmt.Errorf("%w: external execution receipt %q", workflowruntime.ErrNotFound, receipt.ID)
	}
	if err != nil {
		return ExternalExecutionReceipt{}, fmt.Errorf("load workflow external execution receipt: %w", err)
	}
	var request externalExecutionRequest
	if decodeErr := decodeWorkflowJSON("external execution request", requestJSON, &request); decodeErr != nil {
		return ExternalExecutionReceipt{}, decodeErr
	}
	receipt.Request, receipt.ProductStepID, receipt.ProductKind = request.Request, request.ProductStepID, request.ProductKind
	receipt.State, receipt.Ambiguity = ExternalExecutionState(state), ambiguity.String
	receipt.ResolutionKey, receipt.Resolution = resolutionKey.String, resolutionAction.String
	receipt.ResolutionActor, receipt.ResolutionReason = resolutionActor.String, resolutionReason.String
	if output.Valid != isError.Valid {
		return ExternalExecutionReceipt{}, workflowInvalid(errors.New("external execution result columns must be present together"))
	}
	if output.Valid {
		receipt.Result = &ExternalStepResult{Output: output.String, IsError: isError.Int64 == 1}
	}
	if receipt.CreatedAt, err = parseWorkflowTime("external execution created_at", createdAt); err != nil {
		return ExternalExecutionReceipt{}, err
	}
	if receipt.UpdatedAt, err = parseWorkflowTime("external execution updated_at", updatedAt); err != nil {
		return ExternalExecutionReceipt{}, err
	}
	if resolvedAt.Valid {
		if receipt.ResolvedAt, err = parseWorkflowTime("external execution resolved_at", resolvedAt.String); err != nil {
			return ExternalExecutionReceipt{}, err
		}
	}
	if validationErr := validateExternalExecutionReceipt(receipt); validationErr != nil {
		return ExternalExecutionReceipt{}, workflowInvalid(validationErr)
	}
	return receipt, nil
}

func encodeExternalExecutionRequest(request externalExecutionRequest) (string, string, externalExecutionRequest, error) {
	if strings.TrimSpace(request.Request.IdempotencyKey) == "" || strings.TrimSpace(request.Request.Engine) == "" ||
		strings.TrimSpace(request.Request.WorkflowName) == "" || strings.TrimSpace(request.NodeID) == "" {
		return "", "", externalExecutionRequest{}, workflowInvalid(errors.New("external execution request requires key, engine, workflow, and node"))
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", "", externalExecutionRequest{}, workflowInvalid(fmt.Errorf("encode external execution request: %w", err))
	}
	var normalized externalExecutionRequest
	if normalizeErr := json.Unmarshal(encoded, &normalized); normalizeErr != nil {
		return "", "", externalExecutionRequest{}, workflowInvalid(fmt.Errorf("normalize external execution request: %w", normalizeErr))
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", "", externalExecutionRequest{}, workflowInvalid(fmt.Errorf("canonicalize external execution request: %w", err))
	}
	return string(canonical), values.SHA256Digest(canonical), normalized, nil
}

func externalExecutionBindingEqual(receipt ExternalExecutionReceipt, runID, nodeID, iteration, digest string, request externalExecutionRequest) bool {
	return receipt.RunID == runID && receipt.NodeID == nodeID && receipt.Iteration == iteration &&
		receipt.RequestDigest == digest && receipt.Request.IdempotencyKey == request.Request.IdempotencyKey &&
		receipt.ProductStepID == request.ProductStepID && receipt.ProductKind == request.ProductKind
}

func updateExternalExecutionAmbiguous(ctx context.Context, query workflowSQL, receipt ExternalExecutionReceipt) error {
	updated, err := query.ExecContext(ctx, `
UPDATE workflow_external_execution_receipts
SET state=?,ambiguity_reason=?,updated_at=?
WHERE idempotency_key=? AND state=?`, ExternalExecutionAmbiguous, receipt.Ambiguity,
		workflowTime(receipt.UpdatedAt), receipt.ID, ExternalExecutionPending)
	if err != nil {
		return fmt.Errorf("mark workflow external execution ambiguous: %w", err)
	}
	return expectOneExternalReceiptRow(updated, "mark workflow external execution ambiguous")
}

func expectOneExternalReceiptRow(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s affected rows: %w", operation, err)
	}
	if rows != 1 {
		return workflowInvalid(fmt.Errorf("%s changed %d rows, want 1", operation, rows))
	}
	return nil
}

func validateExternalExecutionReceipt(receipt ExternalExecutionReceipt) error {
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RunID) == "" || strings.TrimSpace(receipt.NodeID) == "" ||
		strings.TrimSpace(receipt.RequestDigest) == "" || receipt.Request.IdempotencyKey != receipt.ID {
		return errors.New("external execution receipt has incomplete immutable identity")
	}
	if receipt.UpdatedAt.Before(receipt.CreatedAt) {
		return errors.New("external execution receipt chronology regressed")
	}
	switch receipt.State {
	case ExternalExecutionPrepared, ExternalExecutionPending:
		if receipt.Result != nil || receipt.Ambiguity != "" {
			return errors.New("prepared or pending external execution cannot have an outcome")
		}
	case ExternalExecutionAmbiguous:
		if receipt.Result != nil || strings.TrimSpace(receipt.Ambiguity) == "" {
			return errors.New("ambiguous external execution requires only a reason")
		}
	case ExternalExecutionCompleted:
		if receipt.Result == nil {
			return errors.New("completed external execution requires a result")
		}
	default:
		return fmt.Errorf("unsupported external execution state %q", receipt.State)
	}
	return nil
}
