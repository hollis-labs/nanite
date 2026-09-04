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

// durableAgentCanceller is the minimal DurableAgentService surface
// TaskManager.CancelTask needs for an 'instance'-target task: the existing
// RequestStop stop primitive (internal/service/durable_agents.go), which
// itself transitions durable_agent_instances.status and, if there's a live
// session, calls into DurableAgentRuntimeController.StopSession — the same
// machinery driven by every other instance-stop path in this codebase.
// Narrowed to this one method (rather than embedding the full
// DurableAgentService) so this routing layer's dependency footprint states
// exactly what it uses, matching this file's existing narrow-interface
// pattern (durableWakeStore, DurableAgentStore).
type durableAgentCanceller interface {
	RequestStop(ctx context.Context, id string) (*store.DurableAgentInstance, error)
}

// TaskManager routes A2A Task submissions to the appropriate execution path:
// either WorkflowLauncher.Launch (for workflow skill targets) or
// DurableWake.Wake (for existing instance targets).
//
// This is the core routing layer from CW-20260814-0015. It does NOT create a
// third parallel execution substrate — every Task is fulfilled by exactly one
// of Nanite's two existing execution paths.
type TaskManager struct {
	store         *store.Store
	launcher      *WorkflowLauncher
	wake          DurableAgentWakeService
	durableAgents durableAgentCanceller
	registry      *agentworkflow.Registry
	pushNotifier  *A2APushNotifier
	logger        *slog.Logger
}

// NewTaskManager constructs a TaskManager. All parameters are required.
func NewTaskManager(
	st *store.Store,
	launcher *WorkflowLauncher,
	wake DurableAgentWakeService,
	durableAgents durableAgentCanceller,
	registry *agentworkflow.Registry,
	logger *slog.Logger,
) *TaskManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskManager{
		store:         st,
		launcher:      launcher,
		wake:          wake,
		durableAgents: durableAgents,
		registry:      registry,
		pushNotifier:  NewA2APushNotifier(st, logger),
		logger:        logger,
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
	// ProjectID is internal context, not in the wire protocol.
	ProjectID string
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

	if err := tm.store.CreateA2ATask(ctx, task); err != nil {
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
	updatedTask, err := tm.store.GetA2ATask(ctx, taskID)
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
			"prompt": req.Message, "_nanite_a2a_task_id": task.ID,
		},
		ProjectID: req.ProjectID,
	}
	profileID, err := tm.resolveA2AWorkflowProfile(ctx, task.TargetRef)
	if err != nil {
		return err
	}
	launchReq.AgentProfileID = profileID

	result, err := tm.launcher.Launch(ctx, launchReq)
	if err != nil {
		return fmt.Errorf("workflow launch failed: %w", err)
	}

	// Update the a2a_tasks record with the resulting instance ID and run ID.
	oldState := task.State
	task.DurableAgentInstanceID = sql.NullString{String: result.InstanceID, Valid: true}
	task.WorkflowRunID = sql.NullString{String: result.RunID, Valid: true}
	task.State = a2a.TaskStateWorking

	if err := tm.store.UpdateA2ATask(ctx, task); err != nil {
		tm.logger.Error("a2a: failed to update task after workflow launch",
			"task_id", task.ID,
			"instance_id", result.InstanceID,
			"run_id", result.RunID,
			"error", err,
		)
		return err
	}

	// Enqueue push notification if state changed and config is present.
	if oldState != task.State && task.PushNotificationConfig.Valid {
		tm.enqueuePushNotification(task.ID, task.State)
	}

	return nil
}

// submitInstanceTask routes an instance-target task to DurableWake.Wake.
func (tm *TaskManager) submitInstanceTask(ctx context.Context, task *store.A2ATask, req TaskSubmitRequest) error {
	instanceID := task.TargetRef

	tm.logger.Info("a2a: routing to durable wake", "task_id", task.ID, "instance_id", instanceID)

	// Verify the instance exists.
	inst, err := tm.store.GetDurableAgentInstance(ctx, instanceID)
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
		ProjectID: req.ProjectID,
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
	oldState := task.State
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

	if err := tm.store.UpdateA2ATask(ctx, task); err != nil {
		tm.logger.Error("a2a: failed to update task after wake",
			"task_id", task.ID,
			"instance_id", instanceID,
			"error", err,
		)
		return err
	}

	// Enqueue push notification if state changed and config is present.
	if oldState != task.State && task.PushNotificationConfig.Valid {
		tm.enqueuePushNotification(task.ID, task.State)
	}

	return nil
}

// GetTask retrieves an A2A task by ID and derives its current state from the
// underlying execution (workflow run or durable instance status).
func (tm *TaskManager) GetTask(ctx context.Context, taskID string) (*a2a.Task, error) {
	storeTask, err := tm.store.GetA2ATask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if storeTask == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Derive the current state from the underlying execution.
	derivedState := tm.deriveTaskState(ctx, storeTask)

	// If the state has changed, update the store and enqueue push notification.
	if derivedState != storeTask.State {
		tm.logger.Info("a2a: derived state differs from cached",
			"task_id", taskID,
			"cached", storeTask.State,
			"derived", derivedState,
		)
		storeTask.State = derivedState
		if err := tm.store.UpdateA2ATask(ctx, storeTask); err != nil {
			tm.logger.Error("a2a: failed to update derived state", "task_id", taskID, "error", err)
		} else if storeTask.PushNotificationConfig.Valid {
			// Only enqueue push if the update succeeded
			tm.enqueuePushNotification(taskID, derivedState)
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

// ErrWorkflowCancelUnsupported is returned by CancelTask for workflow engines
// that have no durable cancellation primitive. The built-in compatibility
// engine still runs synchronously with only a process-local Context cancel;
// Hadron runs route to its durable run-cancellation contract. See
// TASKS/phase-0/08-a2a-conformance.md's Work log and TASKS/ESCALATIONS.md
// for the original compatibility investigation.
var ErrWorkflowCancelUnsupported = errors.New("workflow task cancellation not supported: no interrupt primitive exists for in-flight workflow runs")

// CancelTask cancels a Task, deriving the correct cancellation primitive
// from the task's target_kind — the same two-substrate routing
// SubmitTask/classifyTarget already use:
//
//   - target_kind = 'instance': reuses the existing stop primitive,
//     DurableAgentService.RequestStop, rather than inventing new
//     session-control machinery. RequestStop already transitions
//     durable_agent_instances.status and calls into
//     DurableAgentRuntimeController.StopSession for any live session.
//   - target_kind = 'workflow': Hadron-backed runs use their durable engine
//     cancellation seam; older engines without that seam return the typed
//     ErrWorkflowCancelUnsupported rather than faking cancellation.
//
// A task already in a terminal state (completed/failed/canceled/rejected)
// is left alone: canceled is treated as an idempotent success (matches the
// A2A spec's documented idempotent-cancel behavior), and the other
// terminal states return an error rather than overwriting real completion
// or failure information with a fake cancellation.
func (tm *TaskManager) CancelTask(ctx context.Context, taskID string) (*a2a.Task, error) {
	task, err := tm.store.GetA2ATask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Re-derive first — CancelTask must act on real current state, not a
	// possibly-stale cached one (same principle GetTask already applies).
	current := tm.deriveTaskState(ctx, task)
	if current != task.State {
		task.State = current
		if err := tm.store.UpdateA2ATask(ctx, task); err != nil {
			tm.logger.Error("a2a: failed to persist derived state before cancel", "task_id", taskID, "error", err)
		}
	}

	switch current {
	case a2a.TaskStateCanceled:
		// Idempotent: already canceled, report success as-is.
		return &a2a.Task{
			ID:        task.ID,
			State:     task.State,
			Message:   task.Message,
			Error:     task.Error.String,
			CreatedAt: task.CreatedAt,
			UpdatedAt: task.UpdatedAt,
		}, nil
	case a2a.TaskStateCompleted, a2a.TaskStateFailed, a2a.TaskStateRejected:
		return nil, fmt.Errorf("task %s is already in terminal state %q, cannot cancel", taskID, current)
	}

	switch task.TargetKind {
	case "instance":
		if !task.DurableAgentInstanceID.Valid || task.DurableAgentInstanceID.String == "" {
			return nil, fmt.Errorf("task %s has no attached instance to cancel", taskID)
		}
		if tm.durableAgents == nil {
			return nil, fmt.Errorf("cancellation unavailable: no durable agent service configured")
		}
		tm.logger.Info("a2a: canceling instance-backed task",
			"task_id", taskID,
			"instance_id", task.DurableAgentInstanceID.String,
		)
		if _, err := tm.durableAgents.RequestStop(ctx, task.DurableAgentInstanceID.String); err != nil {
			return nil, fmt.Errorf("failed to stop durable agent instance: %w", err)
		}
	case "workflow":
		if !task.WorkflowRunID.Valid || task.WorkflowRunID.String == "" {
			return nil, fmt.Errorf("task %s has no attached workflow run to cancel", taskID)
		}
		if tm.launcher == nil {
			return nil, ErrWorkflowCancelUnsupported
		}
		if _, cancelErr := tm.launcher.Cancel(ctx, task.WorkflowRunID.String, "canceled through A2A task "+taskID); cancelErr != nil {
			return nil, fmt.Errorf("failed to cancel workflow run: %w", cancelErr)
		}
	default:
		return nil, fmt.Errorf("unknown target_kind: %s", task.TargetKind)
	}

	oldState := task.State
	task.State = a2a.TaskStateCanceled

	if err := tm.store.UpdateA2ATask(ctx, task); err != nil {
		return nil, fmt.Errorf("failed to update task state after cancel: %w", err)
	}

	if oldState != task.State && task.PushNotificationConfig.Valid {
		tm.enqueuePushNotification(task.ID, task.State)
	}

	tm.logger.Info("a2a: task canceled", "task_id", taskID, "target_kind", task.TargetKind)

	return &a2a.Task{
		ID:        task.ID,
		State:     task.State,
		Message:   task.Message,
		Error:     task.Error.String,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
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
	run, err := tm.store.GetWorkflowRun(ctx, runID)
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
	// CW-20260814-0017: waiting_on_gate → input-required.
	// TASKS/teams/06-stepkindflex-executor.md: waiting_on_flex → working,
	// deliberately NOT input-required — a flex-waiting TeamRun is agents
	// self-organizing against a live exit trigger, not blocked on a
	// human, and migration 133 exists specifically so this distinction is
	// real at the DB layer, not just documented here.
	// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md:
	// waiting_on_loop → working, by the same reasoning — a loop-waiting
	// run is a contained LoopRun making progress toward its goal, not
	// blocked on a human, and is "the least human-attention-demanding
	// kind" of the three waiting states per
	// docs/engineering/architecture/21-loops.md Decision 1. This
	// deliberately does NOT special-case a LoopRun that has itself
	// escalated (loop_runs.status = 'waiting_on_escalation'): that is a
	// real, separate signal surfaced via the LoopRun's own status (a
	// future task's job, once a LoopRun escalation surface exists), not
	// overloaded onto this outer WorkflowRun's A2A TaskState — the
	// documented resolution this task's Context section asked for.
	switch run.Status {
	case "running", "waiting_on_flex", "waiting_on_loop":
		return a2a.TaskStateWorking
	case "waiting_on_gate":
		return a2a.TaskStateInputRequired
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
	inst, err := tm.store.GetDurableAgentInstance(ctx, instanceID)
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

	return tm.store.CreateA2ATask(context.TODO(

	// updateTaskStateFailed is a helper to update a task to failed state.
	), task)
}

func (tm *TaskManager) updateTaskStateFailed(taskID, errorMsg string) {
	task, err := tm.store.GetA2ATask(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, taskID)
	if err != nil {
		tm.logger.Error("a2a: failed to get task for error update", "task_id", taskID, "error", err)
		return
	}
	if task == nil {
		tm.logger.Error("a2a: task not found for error update", "task_id", taskID)
		return
	}

	oldState := task.State
	task.State = a2a.TaskStateFailed
	task.Error = sql.NullString{String: errorMsg, Valid: true}

	if err := tm.store.UpdateA2ATask(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, task); err != nil {
		tm.logger.Error("a2a: failed to update task to failed state",
			"task_id", taskID,
			"error", err,
		)
		return
	}

	// Enqueue push notification if state changed and config is present.
	if oldState != task.State && task.PushNotificationConfig.Valid {
		tm.enqueuePushNotification(taskID, task.State)
	}
}

// enqueuePushNotification enqueues a push notification for the given task state
// transition. This is best-effort — if it fails, we log but don't fail the
// calling operation.
func (tm *TaskManager) enqueuePushNotification(taskID string, state a2a.TaskState) {
	if err := tm.pushNotifier.EnqueueDelivery(taskID, state); err != nil {
		tm.logger.Warn("a2a: failed to enqueue push notification",
			"task_id", taskID,
			"state", state,
			"error", err,
		)
	}
}

// PushNotifier returns the A2APushNotifier instance for background worker access.
func (tm *TaskManager) PushNotifier() *A2APushNotifier {
	return tm.pushNotifier
}

// ProvideTaskInput provides input to a task that is in input-required state.
// For workflow-backed tasks, this resolves the paused gate and resumes the workflow.
// For instance-backed tasks, this is a no-op (they don't have gates).
// CW-20260814-0017: A2A gate ↔ input-required mapping.
func (tm *TaskManager) ProvideTaskInput(ctx context.Context, taskID, input string) error {
	task, err := tm.store.GetA2ATask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	// Only workflow-backed tasks can be in input-required state.
	if !task.WorkflowRunID.Valid || task.WorkflowRunID.String == "" {
		return fmt.Errorf("task %s is not workflow-backed; input-required is only for workflow gates", taskID)
	}

	runID := task.WorkflowRunID.String

	// Get the waiting gate(s) for this workflow run.
	gates, err := tm.store.GetWaitingGates(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get waiting gates: %w", err)
	}
	if len(gates) == 0 {
		return fmt.Errorf("no waiting gates found for workflow run %s", runID)
	}

	// For simplicity, resolve the first waiting gate. In the future, we could
	// support targeting a specific gate by step_id or support multiple gates.
	gate := gates[0]
	tm.logger.Info("a2a: resolving gate",
		"task_id", taskID,
		"run_id", runID,
		"step_id", gate.StepID,
		"input", input,
	)

	if tm.launcher == nil {
		return fmt.Errorf("workflow launcher not configured")
	}
	if _, resumeErr := tm.launcher.ResumeGate(ctx, runID, gate.StepID, input, task.ID); resumeErr != nil {
		return fmt.Errorf("failed to resolve workflow gate: %w", resumeErr)
	}

	tm.logger.Info("a2a: gate resolved and workflow resumed",
		"task_id", taskID,
		"run_id", runID,
		"step_id", gate.StepID,
	)

	return nil
}

// resumeWorkflowRun resumes a paused workflow run after a gate has been resolved.
func (tm *TaskManager) resumeWorkflowRun(ctx context.Context, task *store.A2ATask) error {
	if !task.WorkflowRunID.Valid || task.WorkflowRunID.String == "" {
		return fmt.Errorf("task has no workflow_run_id")
	}

	runID := task.WorkflowRunID.String
	if tm.launcher == nil {
		return fmt.Errorf("workflow launcher not configured")
	}
	result, err := tm.launcher.Resume(ctx, runID)
	if err != nil {
		tm.logger.Error("a2a: workflow resume failed",
			"task_id", task.ID,
			"run_id", runID,
			"error", err,
		)
		tm.updateTaskStateFailed(task.ID, fmt.Sprintf("workflow resume failed: %v", err))
		return fmt.Errorf("workflow resume failed: %w", err)
	}

	tm.logger.Info("a2a: workflow resumed",
		"task_id", task.ID,
		"run_id", runID,
		"status", result.Status,
	)

	// The task state will be re-derived on next GetTask call based on the
	// workflow run's updated status.

	return nil
}

func (tm *TaskManager) resolveA2AWorkflowProfile(ctx context.Context, workflowName string) (string, error) {
	if tm == nil || tm.store == nil || tm.registry == nil {
		return "", fmt.Errorf("a2a workflow profile resolution is not configured")
	}
	definition, ok := tm.registry.Get(workflowName)
	if !ok {
		return "", fmt.Errorf("unknown workflow skill: %s", workflowName)
	}

	var candidates []string
	settings, err := tm.store.GetUserSettings(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve A2A workflow profile settings: %w", err)
	}
	if settings != nil && strings.TrimSpace(settings.DefaultAgent) != "" {
		candidates = append(candidates, strings.TrimSpace(settings.DefaultAgent))
	}
	for _, step := range definition.Steps {
		if configured, ok := step.Config["agent_id"].(string); ok && strings.TrimSpace(configured) != "" {
			candidates = append(candidates, strings.TrimSpace(configured))
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		if profile, loadErr := tm.store.GetAgent(ctx, candidate); loadErr == nil && profile.Status != "disabled" {
			return profile.ID, nil
		}
		if profile, loadErr := tm.store.GetAgentBySlug(ctx, candidate); loadErr == nil && profile.Status != "disabled" {
			return profile.ID, nil
		}
	}

	profiles, err := tm.store.ListAgents(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve A2A workflow profile fallback: %w", err)
	}
	for _, profile := range profiles {
		if profile.Status != "disabled" {
			return profile.ID, nil
		}
	}
	return "", fmt.Errorf("workflow %q has no persisted active agent profile for A2A launch", workflowName)
}
