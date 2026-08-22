package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

func newA2ATaskManagerTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func TestTaskManager_classifyTarget(t *testing.T) {
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"test-workflow": {
			Name:   "test-workflow",
			Engine: agentworkflow.EngineBuiltin,
		},
	})

	tm := &TaskManager{
		registry: registry,
		logger:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	tests := []struct {
		name        string
		target      string
		wantKind    string
		wantRef     string
		wantErr     bool
		errContains string
	}{
		{
			name:     "workflow skill name",
			target:   "test-workflow",
			wantKind: "workflow",
			wantRef:  "test-workflow",
			wantErr:  false,
		},
		{
			name:        "unknown workflow skill",
			target:      "unknown-workflow",
			wantErr:     true,
			errContains: "unknown workflow skill",
		},
		{
			name:     "valid agent msg:// address",
			target:   "msg://agent/nanite/agt_abc123",
			wantKind: "instance",
			wantRef:  "agt_abc123",
			wantErr:  false,
		},
		{
			name:        "invalid msg:// address",
			target:      "msg://invalid",
			wantErr:     true,
			errContains: "invalid msg:// address",
		},
		{
			// "group" is not a recognized AddressKind in the pinned
			// go-messaging v0.2.1 (only agent/user/service/session/workflow
			// exist there), so this fails at parse time, not at the
			// post-parse "only agent is supported" semantic check below.
			name:        "unrecognized address kind (group) fails to parse",
			target:      "msg://group/nanite/grp_xyz",
			wantErr:     true,
			errContains: "invalid msg:// address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKind, gotRef, err := tm.classifyTarget(tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("classifyTarget() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("classifyTarget() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}
			if gotKind != tt.wantKind {
				t.Errorf("classifyTarget() gotKind = %v, want %v", gotKind, tt.wantKind)
			}
			if gotRef != tt.wantRef {
				t.Errorf("classifyTarget() gotRef = %v, want %v", gotRef, tt.wantRef)
			}
		})
	}
}

func TestTaskManager_deriveFromWorkflowRun(t *testing.T) {
	tests := []struct {
		name      string
		runStatus string
		wantState a2a.TaskState
	}{
		{
			name:      "running workflow",
			runStatus: "running",
			wantState: a2a.TaskStateWorking,
		},
		{
			name:      "completed workflow",
			runStatus: "completed",
			wantState: a2a.TaskStateCompleted,
		},
		{
			name:      "failed workflow",
			runStatus: "failed",
			wantState: a2a.TaskStateFailed,
		},
		{
			// CW-20260814-0017: paused gate maps to input-required, not the
			// generic working fallback.
			name:      "waiting_on_gate workflow maps to input-required",
			runStatus: "waiting_on_gate",
			wantState: a2a.TaskStateInputRequired,
		},
		{
			// TASKS/teams/06-stepkindflex-executor.md: a flex-waiting
			// TeamRun is agents self-organizing against a live exit
			// trigger, not blocked on a human — deliberately NOT
			// input-required, unlike waiting_on_gate above.
			name:      "waiting_on_flex workflow maps to working, not input-required",
			runStatus: "waiting_on_flex",
			wantState: a2a.TaskStateWorking,
		},
		{
			// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md:
			// a loop-waiting WorkflowRun is a contained LoopRun making
			// progress toward its goal, not blocked on a human --
			// deliberately mapped the same way waiting_on_flex is (working,
			// not input-required), per this task's documented mapping
			// choice: a LoopRun that itself escalates is a separate signal
			// surfaced via the LoopRun's own status, not overloaded onto
			// this outer WorkflowRun's A2A TaskState.
			name:      "waiting_on_loop workflow maps to working, not input-required",
			runStatus: "waiting_on_loop",
			wantState: a2a.TaskStateWorking,
		},
		{
			// "cancelled" is a real, schema-valid workflow_runs.status (see
			// the CHECK constraint) that deriveFromWorkflowRun's switch
			// doesn't explicitly map -- exercises the same default-fallback
			// branch a literal invalid string would, without violating the
			// CHECK constraint the way "unknown" does.
			name:      "unmapped-but-valid status defaults to working",
			runStatus: "cancelled",
			wantState: a2a.TaskStateWorking,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create an in-memory store for testing.
			st := newA2ATaskManagerTestStore(t)

			// Create a workflow run with the test status.
			run := &store.WorkflowRunRow{
				ID:             "run_test",
				DefinitionName: "test-workflow",
				Status:         tt.runStatus,
			}
			if err := st.CreateWorkflowRun(context.Background(), run); err != nil {
				t.Fatalf("failed to create workflow run: %v", err)
			}

			tm := &TaskManager{
				store:  st,
				logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			}

			gotState := tm.deriveFromWorkflowRun(context.Background(), run.ID)
			if gotState != tt.wantState {
				t.Errorf("deriveFromWorkflowRun() = %v, want %v", gotState, tt.wantState)
			}
		})
	}
}

func TestTaskManager_deriveFromDurableInstance(t *testing.T) {
	tests := []struct {
		name           string
		instanceStatus string
		wantState      a2a.TaskState
	}{
		{
			name:           "active instance",
			instanceStatus: store.DurableAgentStatusActive,
			wantState:      a2a.TaskStateWorking,
		},
		{
			name:           "starting instance",
			instanceStatus: store.DurableAgentStatusStarting,
			wantState:      a2a.TaskStateWorking,
		},
		{
			name:           "stopped instance (turn finished)",
			instanceStatus: store.DurableAgentStatusStopped,
			wantState:      a2a.TaskStateCompleted,
		},
		{
			name:           "sleeping instance (turn finished)",
			instanceStatus: store.DurableAgentStatusSleeping,
			wantState:      a2a.TaskStateCompleted,
		},
		{
			name:           "failed instance",
			instanceStatus: store.DurableAgentStatusFailed,
			wantState:      a2a.TaskStateFailed,
		},
		{
			name:           "archived instance",
			instanceStatus: store.DurableAgentStatusArchived,
			wantState:      a2a.TaskStateRejected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newA2ATaskManagerTestStore(t)

			profile := &store.AgentProfile{Name: "A2A Test Agent", Slug: "a2a-test-agent", SystemPrompt: "x"}
			if err := st.CreateAgent(context.Background(), profile); err != nil {
				t.Fatalf("CreateAgent: %v", err)
			}

			// Create a durable agent instance with the test status.
			inst := &store.DurableAgentInstance{
				ID:             "inst_test",
				Name:           "test-instance",
				Slug:           "test-instance",
				LifecycleClass: store.DurableAgentClassProcess,
				ProfileID:      profile.ID,
				Status:         tt.instanceStatus,
			}
			if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
				t.Fatalf("failed to create durable instance: %v", err)
			}

			tm := &TaskManager{
				store:  st,
				logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			}

			gotState := tm.deriveFromDurableInstance(context.Background(), inst.ID)
			if gotState != tt.wantState {
				t.Errorf("deriveFromDurableInstance() = %v, want %v", gotState, tt.wantState)
			}
		})
	}
}

func TestTaskManager_deriveTaskState_rejected_stays_rejected(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	tm := &TaskManager{
		store:  st,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	task := &store.A2ATask{
		ID:         "task_test",
		TargetKind: "unknown",
		TargetRef:  "unknown-target",
		Message:    "test message",
		State:      a2a.TaskStateRejected,
	}

	gotState := tm.deriveTaskState(context.Background(), task)
	if gotState != a2a.TaskStateRejected {
		t.Errorf("deriveTaskState() for rejected task = %v, want %v", gotState, a2a.TaskStateRejected)
	}
}

func TestTaskManager_deriveTaskState_no_execution_is_submitted(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	tm := &TaskManager{
		store:  st,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	task := &store.A2ATask{
		ID:         "task_test",
		TargetKind: "workflow",
		TargetRef:  "test-workflow",
		Message:    "test message",
		State:      a2a.TaskStateSubmitted,
		// No DurableAgentInstanceID or WorkflowRunID set.
	}

	gotState := tm.deriveTaskState(context.Background(), task)
	if gotState != a2a.TaskStateSubmitted {
		t.Errorf("deriveTaskState() for task with no execution = %v, want %v", gotState, a2a.TaskStateSubmitted)
	}
}

// fakeDurableAgentCanceller is a minimal durableAgentCanceller test double
// that records every RequestStop call it receives, so tests can assert
// CancelTask actually invoked the reused stop primitive with the right
// instance ID (not just that it returned success).
type fakeDurableAgentCanceller struct {
	calls []string
	err   error
}

func (f *fakeDurableAgentCanceller) RequestStop(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	f.calls = append(f.calls, id)
	if f.err != nil {
		return nil, f.err
	}
	return &store.DurableAgentInstance{ID: id, Status: store.DurableAgentStatusStopped}, nil
}

// TestTaskManager_CancelTask_InstanceTarget_Success verifies the real
// 'instance' target_kind execution path: CancelTask must call
// durableAgentCanceller.RequestStop with the task's attached instance ID,
// transition the task to 'canceled', and persist it.
func TestTaskManager_CancelTask_InstanceTarget_Success(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	profile := &store.AgentProfile{Name: "A2A Cancel Test Agent", Slug: "a2a-cancel-test-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		ID:             "inst_cancel_test",
		Name:           "cancel-test-instance",
		Slug:           "cancel-test-instance",
		LifecycleClass: store.DurableAgentClassProcess,
		ProfileID:      profile.ID,
		Status:         store.DurableAgentStatusActive,
	}
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	task := &store.A2ATask{
		ID:                     "task_cancel_instance",
		TargetKind:             "instance",
		TargetRef:              "msg://agent/nanite/" + inst.ID,
		Message:                "test message",
		State:                  a2a.TaskStateWorking,
		DurableAgentInstanceID: sql.NullString{String: inst.ID, Valid: true},
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	canceller := &fakeDurableAgentCanceller{}
	tm := &TaskManager{
		store:         st,
		durableAgents: canceller,
		pushNotifier:  NewA2APushNotifier(st, nil),
		logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	got, err := tm.CancelTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if got.State != a2a.TaskStateCanceled {
		t.Errorf("CancelTask() state = %v, want %v", got.State, a2a.TaskStateCanceled)
	}
	if len(canceller.calls) != 1 || canceller.calls[0] != inst.ID {
		t.Errorf("RequestStop calls = %v, want exactly one call with %q", canceller.calls, inst.ID)
	}

	// Verify persisted.
	persisted, err := st.GetA2ATask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetA2ATask: %v", err)
	}
	if persisted.State != a2a.TaskStateCanceled {
		t.Errorf("persisted task.State = %v, want %v", persisted.State, a2a.TaskStateCanceled)
	}
}

// TestTaskManager_CancelTask_WorkflowTarget_Unsupported verifies the
// escalated finding: workflow-backed tasks have no real interrupt
// primitive today, so CancelTask must return ErrWorkflowCancelUnsupported
// rather than silently no-op'ing or faking a 'canceled' state.
func TestTaskManager_CancelTask_WorkflowTarget_Unsupported(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	runID := "run_cancel_test"
	if err := st.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{
		ID:             runID,
		DefinitionName: "test-workflow",
		Status:         "running",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	task := &store.A2ATask{
		ID:            "task_cancel_workflow",
		TargetKind:    "workflow",
		TargetRef:     "test-workflow",
		Message:       "test message",
		State:         a2a.TaskStateWorking,
		WorkflowRunID: sql.NullString{String: runID, Valid: true},
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	tm := &TaskManager{
		store:  st,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	_, err := tm.CancelTask(context.Background(), task.ID)
	if !errors.Is(err, ErrWorkflowCancelUnsupported) {
		t.Fatalf("CancelTask() error = %v, want errors.Is match for ErrWorkflowCancelUnsupported", err)
	}

	// The task must not have been mutated into a fake 'canceled' state.
	persisted, err := st.GetA2ATask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetA2ATask: %v", err)
	}
	if persisted.State == a2a.TaskStateCanceled {
		t.Errorf("persisted task.State = %v, must not be canceled when cancellation is unsupported", persisted.State)
	}
}

// TestTaskManager_CancelTask_TaskNotFound verifies a clear error for an
// unknown task ID.
func TestTaskManager_CancelTask_TaskNotFound(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	tm := &TaskManager{
		store:  st,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	_, err := tm.CancelTask(context.Background(), "does-not-exist")
	if err == nil || !contains(err.Error(), "not found") {
		t.Fatalf("CancelTask() error = %v, want an error containing %q", err, "not found")
	}
}

// TestTaskManager_CancelTask_AlreadyCanceled_IsIdempotent verifies the
// spec-documented idempotent-cancel behavior: canceling an
// already-canceled task succeeds and reports 'canceled' again, without
// calling the stop primitive a second time.
func TestTaskManager_CancelTask_AlreadyCanceled_IsIdempotent(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	task := &store.A2ATask{
		ID:         "task_already_canceled",
		TargetKind: "instance",
		TargetRef:  "n/a",
		Message:    "test message",
		State:      a2a.TaskStateCanceled,
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	canceller := &fakeDurableAgentCanceller{}
	tm := &TaskManager{
		store:         st,
		durableAgents: canceller,
		logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	got, err := tm.CancelTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("CancelTask() error = %v, want nil (idempotent success)", err)
	}
	if got.State != a2a.TaskStateCanceled {
		t.Errorf("CancelTask() state = %v, want %v", got.State, a2a.TaskStateCanceled)
	}
	if len(canceller.calls) != 0 {
		t.Errorf("RequestStop calls = %v, want none for an already-canceled task", canceller.calls)
	}
}

// TestTaskManager_CancelTask_AlreadyCompleted_ReturnsError verifies a task
// in a genuine terminal state (not 'canceled') is not silently
// re-canceled — real completion/failure information must not be
// overwritten by a fake cancellation.
func TestTaskManager_CancelTask_AlreadyCompleted_ReturnsError(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)

	profile := &store.AgentProfile{Name: "A2A Cancel Completed Agent", Slug: "a2a-cancel-completed-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		ID:             "inst_already_completed",
		Name:           "already-completed-instance",
		Slug:           "already-completed-instance",
		LifecycleClass: store.DurableAgentClassProcess,
		ProfileID:      profile.ID,
		Status:         store.DurableAgentStatusStopped, // "turn finished" -> derives to Completed
	}
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	task := &store.A2ATask{
		ID:                     "task_already_completed",
		TargetKind:             "instance",
		TargetRef:              "msg://agent/nanite/" + inst.ID,
		Message:                "test message",
		State:                  a2a.TaskStateWorking, // stale cached state
		DurableAgentInstanceID: sql.NullString{String: inst.ID, Valid: true},
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	canceller := &fakeDurableAgentCanceller{}
	tm := &TaskManager{
		store:         st,
		durableAgents: canceller,
		logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	_, err := tm.CancelTask(context.Background(), task.ID)
	if err == nil || !contains(err.Error(), "terminal state") {
		t.Fatalf("CancelTask() error = %v, want an error containing %q", err, "terminal state")
	}
	if len(canceller.calls) != 0 {
		t.Errorf("RequestStop calls = %v, want none for an already-terminal task", canceller.calls)
	}
}
