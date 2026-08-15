package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// TaskManager routes A2A Task submissions to the appropriate execution path:
// either WorkflowLauncher.Launch (for workflow skill targets) or
// DurableWake.Wake (for existing instance targets).
//
// This is the core routing layer from CW-20260814-0015. It does NOT create a
// third parallel execution substrate — every Task is fulfilled by exactly one
// of Nanite's two existing execution paths.
type TaskManager struct {
	store    *store.Store
	launcher *WorkflowLauncher
	wake     DurableAgentWakeService
	registry *agentworkflow.Registry
	logger   *slog.Logger
}

// NewTaskManager constructs a TaskManager. All parameters are required.
func NewTaskManager(
	st *store.Store,
	launcher *WorkflowLauncher,
	wake DurableAgentWakeService,
	registry *agentworkflow.Registry,
	logger *slog.Logger,
) *TaskManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskManager{
		store:    st,
		launcher: launcher,
		wake:     wake,
		registry: registry,
		logger:   logger,
	}
}

// TaskSubmitRequest carries the caller's task submission. This mirrors
// a2a.TaskSubmitRequest but allows internal fields (workspace/project IDs)
// to be threaded through.
type TaskSubmitRequest struct {
	// Target is either a workflow skill ID or an existing durable-agent
	// instance's msg:// address.
	Target string
	// Message is the task content; becomes WakePayload.Prompt or workflow params.
	Message string
	// PushNotificationConfig is optional push delivery config.
	PushNotificationConfig *a2a.PushNotificationConfig
	// WorkspaceID / ProjectID are internal context, not in the wire protocol.
	WorkspaceID string
	ProjectID   string
}

// TaskSubmitResult is the outcome of a successful task submission.
type TaskSubmitResult struct {
	TaskID string
	State  a2a.TaskState
}

// SubmitTask accepts a task submission, routes it to the correct execution
// path, creates the a2a_tasks bookkeeping record, and returns the task ID.
//
// Routing logic:
//   - If target is a known workflow skill name → WorkflowLauncher.Launch
//   - If target is a msg://agent/<authority>/<id> instance address → DurableWake.Wake
//   - Otherwise → reject (TaskStateRejected)
func (tm *TaskManager) SubmitTask(ctx context.Context, req TaskSubmitRequest) (*TaskSubmitResult, error) {
	taskID := ulid.Make().String()

	tm.logger.Info("a2a: task submit",
		"task_id", taskID,
		"target", req.Target,
		"workspace_id", req.WorkspaceID,
		"project_id", req.ProjectID,
	)

	// Determine target kind and route accordingly.
	targetKind, targetRef, err := tm.classifyTarget(req.Target)
	if err != nil {
		// Unknown target → create a rejected task record.
		if createErr := tm.createRejectedTask(taskID, req, err.Error()); createErr != nil {
			tm.logger.Error("a2a: failed to create rejected task record", "task_id", taskID, "error", createErr)
		}
		return nil, err
	}

	// Create the initial a2a_tasks record in "submitted" state.
	task := &store.A2ATask{
		ID:         taskID,
		TargetKind: targetKind,
		TargetRef:  targetRef,
		Message:    req.Message,
		State:      a2a.TaskStateSubmitted,
	}

	if req.PushNotificationConfig != nil {
		configJSON, err := json.Marshal(req.PushNotificationConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal push config: %w", err)
		}
		task.PushNotificationConfig = sql.NullString{String: string(configJSON), Valid: true}
	}

	if err := tm.store.CreateA2ATask(task); err != nil {
		return nil, fmt.Errorf("failed to create a2a_tasks record: %w", err)
	}

	// Route to the appropriate execution path.
	switch targetKind {
	case "workflow":
		if err := tm.submitWorkflowTask(ctx, task, req); err != nil {
			tm.updateTaskStateFailed(task.ID, err.Error())
			return nil, err
		}
	case "instance":
		if err := tm.submitInstanceTask(ctx, task, req); err != nil {
			tm.updateTaskStateFailed(task.ID, err.Error())
			return nil, err
		}
	default:
		// Should not reach here if classifyTarget is correct, but defensively
		// handle it.
		err := fmt.Errorf("unknown target_kind: %s", targetKind)
		tm.updateTaskStateFailed(task.ID, err.Error())
		return nil, err
	}

	// Refresh the task state from the store (it may have been updated by the
	// execution path already).
	updatedTask, err := tm.store.GetA2ATask(taskID)
	if err != nil {
		tm.logger.Error("a2a: failed to refresh task after submission", "task_id", taskID, "error", err)
		// Return the original state as a fallback.
		return &TaskSubmitResult{TaskID: taskID, State: a2a.TaskStateWorking}, nil
	}

	return &TaskSubmitResult{TaskID: taskID, State: updatedTask.State}, nil
}

// classifyTarget determines whether the target is a workflow skill or an
// instance address, and returns (targetKind, targetRef, error).
func (tm *TaskManager) classifyTarget(target string) (string, string, error) {
	// Check if target is a msg:// address.
	if strings.HasPrefix(target, "msg://") {
		addr, err := a2a.ParseURN(target)
		if err != nil {
			return "", "", fmt.Errorf("invalid msg:// address: %w", err)
		}
		// We only support agent instances for now.
		if addr.Kind != a2a.KindAgent {
			return "", "", fmt.Errorf("unsupported address kind: %s (only 'agent' is supported)", addr.Kind)
		}
		// The instance ID is addr.ID. We'll validate it exists in submitInstanceTask.
		return "instance", addr.ID, nil
	}

	// Otherwise, treat it as a workflow skill name.
	// Validate that the workflow exists in the registry.
	if _, ok := tm.registry.Get(target); !ok {
		return "", "", fmt.Errorf("unknown workflow skill: %s", target)
	}

	return "workflow", target, nil
}

// submitWorkflowTask routes a workflow-target task to WorkflowLauncher.Launch.
func (tm *TaskManager) submitWorkflowTask(ctx context.Context, task *store.A2ATask, req TaskSubmitRequest) error {
	tm.logger.Info("a2a: routing to workflow", "task_id", task.ID, "workflow", task.TargetRef)

	launchReq := WorkflowLaunchRequest{
		WorkflowName: task.TargetRef,
		// Parse the message as workflow params. For now, treat it as a simple
		// { "prompt": "<message>" } structure. Future iterations can support
		// richer param extraction.
		Params: map[string]any{
			"prompt": req.Message,
		},
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		// TODO: Thread through AgentProfileID when we have a way to derive it
		// from the A2A request context. For now, leave it empty and rely on
		// WorkflowLauncher's defaults.
	}

	result, err := tm.launcher.Launch(ctx, launchReq)
	if err != nil {
		return fmt.Errorf("workflow launch failed: %w", err)
	}

	// Update the a2a_tasks record with the resulting instance ID and run ID.
	task.DurableAgentInstanceID = sql.NullString{String: result.InstanceID, Valid: true}
	task.WorkflowRunID = sql.NullString{String: result.RunID, Valid: true}
	task.State = a2a.TaskStateWorking

	if err := tm.store.UpdateA2ATask(task); err != nil {
		tm.logger.Error("a2a: failed to update task after workflow launch",
			"task_id", task.ID,
			"instance_id", result.InstanceID,
			"run_id", result.RunID,
			"error", err,
		)
	}

	return nil
}

// submitInstanceTask routes an instance-target task to DurableWake.Wake.
func (tm *TaskManager) submitInstanceTask(ctx context.Context, task *store.A2ATask, req TaskSubmitRequest) error {
	instanceID := task.TargetRef

	tm.logger.Info("a2a: routing to durable wake", "task_id", task.ID, "instance_id", instanceID)

	// Verify the instance exists.
	inst, err := tm.store.GetDurableAgentInstance(instanceID)
	if err != nil {
		return fmt.Errorf("failed to retrieve instance: %w", err)
	}
	if inst == nil {
		return fmt.Errorf("instance not found: %s", instanceID)
	}
	if inst.Status == store.DurableAgentStatusArchived {
		return errors.New("instance is archived")
	}

	// Wake the instance with the task message as the prompt.
	wakeReq := DurableAgentWakeRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: DurableAgentWakePayload{
			Reason: DurableAgentWakeExternalMessage,
			Prompt: req.Message,
		},
	}

	wakeResult, err := tm.wake.Wake(ctx, instanceID, wakeReq)
	if err != nil {
		return fmt.Errorf("durable wake failed: %w", err)
	}

	// Update the a2a_tasks record with the instance ID.
	task.DurableAgentInstanceID = sql.NullString{String: instanceID, Valid: true}
	task.State = a2a.TaskStateWorking

	// If the wake was skipped (e.g., instance already active), note it.
	if wakeResult.Skipped {
		tm.logger.Warn("a2a: wake skipped",
			"task_id", task.ID,
			"instance_id", instanceID,
			"reason", wakeResult.SkipReason,
		)
	}

	if err := tm.store.UpdateA2ATask(task); err != nil {
		tm.logger.Error("a2a: failed to update task after wake",
			"task_id", task.ID,
			"instance_id", instanceID,
			"error", err,
		)
	}

	return nil
}

// GetTask retrieves an A2A task by ID and derives its current state from the
// underlying execution (workflow run or durable instance status).
func (tm *TaskManager) GetTask(ctx context.Context, taskID string) (*a2a.Task, error) {
	storeTask, err := tm.store.GetA2ATask(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if storeTask == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Derive the current state from the underlying execution.
	derivedState := tm.deriveTaskState(ctx, storeTask)

	// If the state has changed, update the store.
	if derivedState != storeTask.State {
		tm.logger.Info("a2a: derived state differs from cached",
			"task_id", taskID,
			"cached", storeTask.State,
			"derived", derivedState,
		)
		storeTask.State = derivedState
		if err := tm.store.UpdateA2ATask(storeTask); err != nil {
			tm.logger.Error("a2a: failed to update derived state", "task_id", taskID, "error", err)
		}
	}

	return &a2a.Task{
		ID:        storeTask.ID,
		State:     storeTask.State,
		Message:   storeTask.Message,
		Error:     storeTask.Error.String,
		CreatedAt: storeTask.CreatedAt,
		UpdatedAt: storeTask.UpdatedAt,
	}, nil
}

// deriveTaskState derives the current TaskState from the underlying execution
// (workflow run or durable instance status). This implements the "TaskState is
// derived from real execution status, never independently maintained" principle.
func (tm *TaskManager) deriveTaskState(ctx context.Context, task *store.A2ATask) a2a.TaskState {
	// If the task was rejected, it stays rejected.
	if task.State == a2a.TaskStateRejected {
		return a2a.TaskStateRejected
	}

	// If the task was canceled, it stays canceled.
	if task.State == a2a.TaskStateCanceled {
		return a2a.TaskStateCanceled
	}

	// If the task has a workflow run, derive from workflow_runs status.
	if task.WorkflowRunID.Valid && task.WorkflowRunID.String != "" {
		return tm.deriveFromWorkflowRun(ctx, task.WorkflowRunID.String)
	}

	// If the task has a durable instance, derive from durable_agent_instances status.
	if task.DurableAgentInstanceID.Valid && task.DurableAgentInstanceID.String != "" {
		return tm.deriveFromDurableInstance(ctx, task.DurableAgentInstanceID.String)
	}

	// No execution attached yet → still submitted.
	return a2a.TaskStateSubmitted
}

// deriveFromWorkflowRun derives TaskState from a workflow_runs row.
func (tm *TaskManager) deriveFromWorkflowRun(ctx context.Context, runID string) a2a.TaskState {
	run, err := tm.store.GetWorkflowRun(runID)
	if err != nil {
		tm.logger.Error("a2a: failed to get workflow run for state derivation",
			"run_id", runID,
			"error", err,
		)
		// Fallback to working if we can't determine.
		return a2a.TaskStateWorking
	}
	if run == nil {
		tm.logger.Warn("a2a: workflow run not found for state derivation", "run_id", runID)
		return a2a.TaskStateWorking
	}

	// Map workflow_runs.status to TaskState.
	// Per design doc: "workflow run is running" → working,
	// completed → completed, failed → failed.
	// We also need to handle input-required for paused gates (future enhancement).
	switch run.Status {
	case "running":
		return a2a.TaskStateWorking
	case "completed":
		return a2a.TaskStateCompleted
	case "failed":
		return a2a.TaskStateFailed
	default:
		// Unknown status → assume working.
		tm.logger.Warn("a2a: unknown workflow run status",
			"run_id", runID,
			"status", run.Status,
		)
		return a2a.TaskStateWorking
	}
}

// deriveFromDurableInstance derives TaskState from a durable_agent_instances row.
// Per design doc: non-workflow tasks have coarser semantics — working until the
// woken session's turn finishes, then completed/failed.
func (tm *TaskManager) deriveFromDurableInstance(ctx context.Context, instanceID string) a2a.TaskState {
	inst, err := tm.store.GetDurableAgentInstance(instanceID)
	if err != nil {
		tm.logger.Error("a2a: failed to get durable instance for state derivation",
			"instance_id", instanceID,
			"error", err,
		)
		return a2a.TaskStateWorking
	}
	if inst == nil {
		tm.logger.Warn("a2a: durable instance not found for state derivation", "instance_id", instanceID)
		return a2a.TaskStateWorking
	}

	// Map durable_agent_instances.status to TaskState.
	// Per design doc: "working until woken session turn finishes, then completed/failed".
	// We interpret this as:
	//  - active, starting, start_requested → working
	//  - stopped, sleeping → completed (turn finished successfully)
	//  - failed → failed
	//  - archived → rejected (can't wake an archived instance)
	switch inst.Status {
	case store.DurableAgentStatusActive,
		store.DurableAgentStatusStarting,
		store.DurableAgentStatusStartRequested:
		return a2a.TaskStateWorking
	case store.DurableAgentStatusStopped,
		store.DurableAgentStatusSleeping:
		// Turn finished successfully.
		return a2a.TaskStateCompleted
	case store.DurableAgentStatusFailed:
		return a2a.TaskStateFailed
	case store.DurableAgentStatusArchived:
		return a2a.TaskStateRejected
	case store.DurableAgentStatusPaused,
		store.DurableAgentStatusResumeRequested,
		store.DurableAgentStatusStopRequested:
		// Still in progress.
		return a2a.TaskStateWorking
	default:
		tm.logger.Warn("a2a: unknown durable instance status",
			"instance_id", instanceID,
			"status", inst.Status,
		)
		return a2a.TaskStateWorking
	}
}

// createRejectedTask is a helper to create an a2a_tasks record in rejected state.
func (tm *TaskManager) createRejectedTask(taskID string, req TaskSubmitRequest, reason string) error {
	task := &store.A2ATask{
		ID:         taskID,
		TargetKind: "unknown",
		TargetRef:  req.Target,
		Message:    req.Message,
		State:      a2a.TaskStateRejected,
		Error:      sql.NullString{String: reason, Valid: true},
	}

	if req.PushNotificationConfig != nil {
		configJSON, _ := json.Marshal(req.PushNotificationConfig)
		task.PushNotificationConfig = sql.NullString{String: string(configJSON), Valid: true}
	}

	return tm.store.CreateA2ATask(task)
}

// updateTaskStateFailed is a helper to update a task to failed state.
func (tm *TaskManager) updateTaskStateFailed(taskID, errorMsg string) {
	task, err := tm.store.GetA2ATask(taskID)
	if err != nil {
		tm.logger.Error("a2a: failed to get task for error update", "task_id", taskID, "error", err)
		return
	}
	if task == nil {
		tm.logger.Error("a2a: task not found for error update", "task_id", taskID)
		return
	}

	task.State = a2a.TaskStateFailed
	task.Error = sql.NullString{String: errorMsg, Valid: true}

	if err := tm.store.UpdateA2ATask(task); err != nil {
		tm.logger.Error("a2a: failed to update task to failed state",
			"task_id", taskID,
			"error", err,
		)
	}
}
