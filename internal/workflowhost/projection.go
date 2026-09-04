package workflowhost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
)

const (
	EngineKindGoWorkflow  = "go_workflow_v0.1.0"
	EngineContractVersion = "v0.1.0"
	EngineKindPilotHadron = "hadron_v0.5.0-beta.2"
	PilotContractVersion  = "v0.5.0-beta.2"
	// EngineKindHadron is retained as a source-compatible name for the
	// frozen recovery-only pilot identity. New rows never use it.
	EngineKindHadron         = EngineKindPilotHadron
	legacyProjectionKindTool = "tool"
)

// PlanNodeProjection is immutable Nanite-owned metadata for projecting one
// shared plan node onto the existing workflow_run_steps product row.
type PlanNodeProjection struct {
	NodeID        string
	ProductStepID string
	ProductKind   string
	WaitClass     string
}

// RecordPlanNodeProjections records the product-facing identity of a compiled
// plan before a run is started. Re-recording the exact mapping is idempotent;
// changing it under the same plan digest fails closed.
func (s *WorkflowStateStore) RecordPlanNodeProjections(ctx context.Context, plan workflowruntime.PlanRef, projections []PlanNodeProjection) error {
	if err := plan.Validate(); err != nil {
		return workflowInvalid(err)
	}
	return s.write(ctx, "record workflow plan projections", func(query workflowSQL) error {
		if err := ensureWorkflowPlan(ctx, query, plan); err != nil {
			return err
		}
		for _, projection := range projections {
			if err := validatePlanNodeProjection(projection); err != nil {
				return workflowInvalid(err)
			}
			result, err := query.ExecContext(ctx, `
INSERT INTO workflow_plan_node_projections(
    plan_digest, node_id, product_step_id, product_kind, wait_class
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(plan_digest, node_id) DO NOTHING`,
				plan.Digest, projection.NodeID, projection.ProductStepID,
				projection.ProductKind, projection.WaitClass,
			)
			if err != nil {
				return fmt.Errorf("record workflow plan node projection: %w", err)
			}
			if _, rowsErr := result.RowsAffected(); rowsErr != nil {
				return fmt.Errorf("inspect workflow plan node projection: %w", rowsErr)
			}
			stored, err := loadPlanNodeProjection(ctx, query, plan.Digest, projection.NodeID)
			if err != nil {
				return err
			}
			if stored != projection {
				return fmt.Errorf("%w: plan node projection %q has different metadata", workflowruntime.ErrAlreadyExists, projection.NodeID)
			}
		}
		return nil
	})
}

func validatePlanNodeProjection(projection PlanNodeProjection) error {
	if projection.NodeID == "" || projection.ProductStepID == "" {
		return errors.New("plan node projection requires node and product step ids")
	}
	switch projection.ProductKind {
	case "llm", "tool", "gate", "flex", "loop":
	default:
		return fmt.Errorf("unsupported product step kind %q", projection.ProductKind)
	}
	switch projection.WaitClass {
	case "", "gate", "flex", "loop":
	default:
		return fmt.Errorf("unsupported product wait class %q", projection.WaitClass)
	}
	return nil
}

func loadPlanNodeProjection(ctx context.Context, query workflowSQL, digest, nodeID string) (PlanNodeProjection, error) {
	var projection PlanNodeProjection
	err := query.QueryRowContext(ctx, `
SELECT node_id, product_step_id, product_kind, wait_class
FROM workflow_plan_node_projections
WHERE plan_digest = ? AND node_id = ?`, digest, nodeID).Scan(
		&projection.NodeID, &projection.ProductStepID, &projection.ProductKind, &projection.WaitClass,
	)
	if err != nil {
		return PlanNodeProjection{}, err
	}
	return projection, nil
}

func projectionForInvocation(ctx context.Context, query workflowSQL, id workflowruntime.NodeInvocationID) (PlanNodeProjection, error) {
	var digest string
	if err := query.QueryRowContext(ctx, `SELECT plan_digest FROM workflow_runs WHERE id = ?`, id.RunID).Scan(&digest); err != nil {
		return PlanNodeProjection{}, fmt.Errorf("load workflow projection plan: %w", err)
	}
	projection, err := loadPlanNodeProjection(ctx, query, digest, id.NodeID)
	if errors.Is(err, sql.ErrNoRows) {
		// The runtime contract intentionally does not carry product StepKind
		// metadata. Retain a conservative row for low-level store callers while
		// production host starts record an exact mapping before materialization.
		return PlanNodeProjection{
			NodeID: id.NodeID, ProductStepID: id.NodeID, ProductKind: legacyProjectionKindTool,
		}, nil
	}
	if err != nil {
		return PlanNodeProjection{}, fmt.Errorf("load workflow plan node projection: %w", err)
	}
	return projection, nil
}

func (s *WorkflowStateStore) loadOpenProductWait(ctx context.Context, runID, productStepID string) (workflowruntime.WaitSnapshot, error) {
	return s.loadProductWait(ctx, runID, productStepID, true)
}

func (s *WorkflowStateStore) loadProductWait(ctx context.Context, runID, productStepID string, openOnly bool) (workflowruntime.WaitSnapshot, error) {
	var waitID string
	statement := `
SELECT w.wait_id
FROM workflow_waits w
JOIN workflow_runs r ON r.id = w.run_id
JOIN workflow_plan_node_projections p
  ON p.plan_digest = r.plan_digest AND p.node_id = w.node_id
	WHERE w.run_id = ? AND p.product_step_id = ?`
	if openOnly {
		statement += ` AND w.status = 'open'`
	}
	statement += ` ORDER BY w.created_at DESC, w.wait_id DESC LIMIT 1`
	err := s.db.QueryRowContext(ctx, statement, runID, productStepID).Scan(&waitID)
	if errors.Is(err, sql.ErrNoRows) {
		return workflowruntime.WaitSnapshot{}, fmt.Errorf("%w: open product wait %s/%s", workflowruntime.ErrNotFound, runID, productStepID)
	}
	if err != nil {
		return workflowruntime.WaitSnapshot{}, fmt.Errorf("load open product wait: %w", err)
	}
	return s.LoadWait(ctx, workflowruntime.WaitID(waitID))
}

func projectWorkflowRunStatus(ctx context.Context, query workflowSQL, snapshot workflowruntime.RunSnapshot) (string, error) {
	switch snapshot.Status {
	case workflowruntime.RunPending, workflowruntime.RunRunning:
		return "running", nil
	case workflowruntime.RunSucceeded:
		return "completed", nil
	case workflowruntime.RunFailed, workflowruntime.RunTimedOut, workflowruntime.RunCrashed:
		return "failed", nil
	case workflowruntime.RunCanceled:
		return "canceled", nil
	case workflowruntime.RunWaiting:
		return projectedWaitingStatus(ctx, query, snapshot.ID)
	default:
		return "", workflowInvalid(fmt.Errorf("unsupported run status projection %q", snapshot.Status))
	}
}

func projectedWaitingStatus(ctx context.Context, query workflowSQL, runID workflowruntime.RunID) (string, error) {
	rows, err := query.QueryContext(ctx, `
SELECT COALESCE(p.wait_class, '')
FROM workflow_waits w
LEFT JOIN workflow_runs r ON r.id = w.run_id
LEFT JOIN workflow_plan_node_projections p
  ON p.plan_digest = r.plan_digest AND p.node_id = w.node_id
WHERE w.run_id = ? AND w.status = 'open'
ORDER BY CASE COALESCE(p.wait_class, '')
    WHEN 'gate' THEN 0 WHEN 'flex' THEN 1 WHEN 'loop' THEN 2 ELSE 3 END,
    w.created_at, w.wait_id`, runID)
	if err != nil {
		return "", fmt.Errorf("load workflow waiting projection: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var class string
		if err := rows.Scan(&class); err != nil {
			return "", fmt.Errorf("scan workflow waiting projection: %w", err)
		}
		switch class {
		case "flex":
			return "waiting_on_flex", nil
		case "loop":
			return "waiting_on_loop", nil
		default:
			return "waiting_on_gate", nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate workflow waiting projection: %w", err)
	}
	var scheduledRetries int
	if err := query.QueryRowContext(ctx, `
SELECT COUNT(1)
FROM workflow_retry_activations
WHERE run_id = ? AND status = 'scheduled'`, runID).Scan(&scheduledRetries); err != nil {
		return "", fmt.Errorf("load workflow retry projection: %w", err)
	}
	if scheduledRetries > 0 {
		return "running", nil
	}
	// A run transition can race the first wait materialization only through a
	// broken caller. Keep the old API conservative and visibly input-blocked.
	return "waiting_on_gate", nil
}

func projectWorkflowNode(ctx context.Context, query workflowSQL, snapshot workflowruntime.NodeInvocationSnapshot) error {
	if snapshot.ID.Iteration != "" || snapshot.Phase != workflowruntime.InvocationForward {
		return nil
	}
	projection, err := projectionForInvocation(ctx, query, snapshot.ID)
	if err != nil {
		return err
	}
	var status string
	switch snapshot.Status {
	case workflowruntime.NodePending, workflowruntime.NodeReady, workflowruntime.NodeBlocked:
		status = "pending"
	case workflowruntime.NodeRunning:
		status = "running"
	case workflowruntime.NodeWaiting:
		switch projection.WaitClass {
		case "flex":
			status = "waiting_on_flex"
		case "loop":
			status = "waiting_on_loop"
		default:
			status = "waiting_on_gate"
		}
	case workflowruntime.NodeSucceeded:
		status = "completed"
	case workflowruntime.NodeSkipped, workflowruntime.NodeCanceled:
		// The current persistence CHECK has no canceled step literal. The
		// product run still projects canceled exactly; an unfinished canceled
		// node retains the established skipped compatibility representation.
		status = "skipped"
	case workflowruntime.NodeFailed, workflowruntime.NodeTimedOut, workflowruntime.NodeCrashed:
		status = "failed"
	default:
		return workflowInvalid(fmt.Errorf("unsupported node status projection %q", snapshot.Status))
	}
	output, isError, toolCallsJSON, verifyJSON, errorMessage, err := projectedNodeResult(ctx, query, snapshot)
	if err != nil {
		return err
	}
	if status == "failed" {
		isError = true
	}
	gateInput := ""
	if projection.ProductKind == "gate" && status == "completed" {
		gateInput = output
	}

	stepID := string(snapshot.ID.RunID) + ":" + projection.ProductStepID
	startedAt := ""
	if snapshot.Status == workflowruntime.NodeRunning || snapshot.Status == workflowruntime.NodeWaiting || snapshot.Status.Terminal() {
		startedAt = workflowTime(snapshot.UpdatedAt)
	}
	completedAt := ""
	if snapshot.Status.Terminal() {
		completedAt = workflowTime(snapshot.UpdatedAt)
	}
	var loopRunID any
	if projection.WaitClass == "loop" {
		loopRunID, err = projectedLoopRunID(ctx, query, snapshot)
		if err != nil {
			return err
		}
	}
	_, err = query.ExecContext(ctx, `
INSERT INTO workflow_run_steps(
    id, workflow_run_id, step_id, kind, status, output, is_error,
    tool_calls_json, verify_json, error, gate_input,
    started_at, completed_at, updated_at, loop_run_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    status = excluded.status,
    output = excluded.output,
    is_error = excluded.is_error,
    tool_calls_json = excluded.tool_calls_json,
    verify_json = excluded.verify_json,
    error = excluded.error,
	gate_input = excluded.gate_input,
    started_at = CASE
        WHEN workflow_run_steps.started_at = '' THEN excluded.started_at
        ELSE workflow_run_steps.started_at END,
    completed_at = excluded.completed_at,
	updated_at = excluded.updated_at,
	loop_run_id = COALESCE(workflow_run_steps.loop_run_id, excluded.loop_run_id)`,
		stepID, snapshot.ID.RunID, projection.ProductStepID, projection.ProductKind,
		status, output, isError, toolCallsJSON, verifyJSON, errorMessage,
		gateInput, startedAt, completedAt, workflowTime(snapshot.UpdatedAt), loopRunID,
	)
	if err != nil {
		return fmt.Errorf("project workflow node %s: %w", workflowNodeIdentity(snapshot.ID), err)
	}
	return nil
}

func projectedLoopRunID(ctx context.Context, query workflowSQL, snapshot workflowruntime.NodeInvocationSnapshot) (any, error) {
	if snapshot.Wait == nil {
		return nil, nil
	}
	wait, err := loadWorkflowWait(ctx, query, snapshot.Wait.ID)
	if err != nil {
		return nil, fmt.Errorf("load loop wait projection: %w", err)
	}
	if wait.Kind != "child_run" || wait.WakeSource != "child_run" || wait.Correlation == "" {
		return nil, workflowInvalid(errors.New("loop wait does not carry a LoopRun correlation"))
	}
	return wait.Correlation, nil
}

func projectedNodeResult(ctx context.Context, query workflowSQL, snapshot workflowruntime.NodeInvocationSnapshot) (string, bool, string, string, string, error) {
	output, toolCallsJSON, verifyJSON, errorMessage := "", "[]", "", ""
	isError := false
	if snapshot.Outputs != nil {
		set, err := loadWorkflowValues(ctx, query, *snapshot.Outputs)
		if err != nil {
			return "", false, "", "", "", err
		}
		if value, ok := set["result"]; ok {
			payload, _ := value.Inline.(map[string]any)
			output, _ = payload["output"].(string)
			isError, _ = payload["is_error"].(bool)
			if calls, exists := payload["tool_calls"]; exists {
				encoded, encodeErr := json.Marshal(calls)
				if encodeErr != nil {
					return "", false, "", "", "", workflowInvalid(encodeErr)
				}
				toolCallsJSON = string(encoded)
			}
			if verify, exists := payload["verify"]; exists {
				encoded, encodeErr := json.Marshal(verify)
				if encodeErr != nil {
					return "", false, "", "", "", workflowInvalid(encodeErr)
				}
				verifyJSON = string(encoded)
			}
		}
	}
	if snapshot.Status == workflowruntime.NodeFailed || snapshot.Status == workflowruntime.NodeTimedOut || snapshot.Status == workflowruntime.NodeCrashed {
		var failureJSON sql.NullString
		err := query.QueryRowContext(ctx, `
SELECT failure_json FROM workflow_attempts
WHERE run_id=? AND node_id=? AND iteration=? AND attempt_number=?`,
			snapshot.ID.RunID, snapshot.ID.NodeID, snapshot.ID.Iteration, snapshot.LatestAttempt,
		).Scan(&failureJSON)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", false, "", "", "", fmt.Errorf("load projected workflow failure: %w", err)
		}
		if failureJSON.Valid {
			var failure workflowruntime.Failure
			if decodeErr := decodeWorkflowJSON("projected workflow failure", failureJSON.String, &failure); decodeErr != nil {
				return "", false, "", "", "", decodeErr
			}
			errorMessage = failure.Message
			if output == "" {
				output = failure.Message
			}
		}
	}
	if isError && errorMessage == "" {
		errorMessage = output
	}
	return output, isError, toolCallsJSON, verifyJSON, errorMessage, nil
}

func projectedCompletionTime(status workflowruntime.RunStatus, at time.Time) string {
	if status.Terminal() {
		return workflowTime(at)
	}
	return ""
}
