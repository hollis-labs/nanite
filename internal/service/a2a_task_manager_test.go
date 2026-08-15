package service

import (
	"context"
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
	t.Cleanup(func() { s.Close() })
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
			// "waiting_on_gate" is a real, schema-valid workflow_runs.status
			// (see the CHECK constraint) that deriveFromWorkflowRun's switch
			// doesn't explicitly map -- exercises the same default-fallback
			// branch a literal invalid string would, without violating the
			// CHECK constraint the way "unknown" does.
			name:      "unmapped-but-valid status defaults to working",
			runStatus: "waiting_on_gate",
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
			if err := st.CreateWorkflowRun(run); err != nil {
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
			if err := st.CreateAgent(profile); err != nil {
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
			if err := st.CreateDurableAgentInstance(inst); err != nil {
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
