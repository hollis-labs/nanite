package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newA2ATaskManagerTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func TestTaskManager_classifyTarget(t *testing.T) {
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"test-workflow": {
			Name: "test-workflow",
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
			// go-messaging v0.4.0 parses group as a recognized address kind;
			// TaskManager still intentionally accepts only agent targets.
			name:        "recognized but unsupported group address",
			target:      "msg://group/nanite/grp_xyz",
			wantErr:     true,
			errContains: "unsupported address kind: group",
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
			// "canceled" is a real, schema-valid workflow_runs.status (see
			// the CHECK constraint) that deriveFromWorkflowRun's switch
			// doesn't explicitly map -- exercises the same default-fallback
			// branch a literal invalid string would, without violating the
			// CHECK constraint the way "unknown" does.
			name:      "unmapped-but-valid status defaults to working",
			runStatus: "canceled",
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
			if err := persistTestActor(context.Background(), st, profile); err != nil {
				t.Fatalf("persist prior actor: %v", err)
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
			if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
				t.Fatalf("persist prior instance: %v", err)
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

// This host records effects but never supplies a fabric or actor issuer.
type a2aLaunchRecordingHost struct{ calls []string }

func (h *a2aLaunchRecordingHost) Run(context.Context, agentworkflow.WorkflowDefinition, agentworkflow.WorkflowInput, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	h.calls = append(h.calls, "run")
	return agentworkflow.WorkflowResult{}, nil
}
func (h *a2aLaunchRecordingHost) Resume(context.Context, string, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	h.calls = append(h.calls, "resume")
	return agentworkflow.WorkflowResult{}, nil
}
func (h *a2aLaunchRecordingHost) ResumeGate(context.Context, string, string, string, string, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	h.calls = append(h.calls, "gate")
	return agentworkflow.WorkflowResult{}, nil
}
func (h *a2aLaunchRecordingHost) Cancel(context.Context, string, string) (agentworkflow.WorkflowResult, error) {
	h.calls = append(h.calls, "cancel")
	return agentworkflow.WorkflowResult{}, nil
}

// Raw private task rows model retained protocol history. They cannot enroll an
// actor, launch a workflow, or bypass the held production fabric boundary.
func a2aRetainedTaskFixture(t *testing.T, st *store.Store, state a2a.TaskState, kind, executionID string) string {
	t.Helper()
	id := "retained-" + kind + "-" + string(state)
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO a2a_tasks(id,target_kind,target_ref,message,state,result,error,push_notification_config,created_at,updated_at) VALUES(?,?,?,'Private retained request',?,'Private retained result','Private retained error','{"url":"https://unused.invalid/callback"}','2026-09-01','2026-09-02')`, id, kind, "retained-target", state); err != nil {
		t.Fatal(err)
	}
	// Retained references remain linked to the original execution history.
	if kind == "instance" {
		if _, err := st.DB.ExecContext(t.Context(), `UPDATE a2a_tasks SET durable_agent_instance_id=? WHERE id=?`, executionID, id); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := st.DB.ExecContext(t.Context(), `UPDATE a2a_tasks SET workflow_run_id=? WHERE id=?`, executionID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO a2a_push_deliveries(id,task_id,target_state,attempt_count,last_error,next_retry,created_at,updated_at) VALUES(?,?,?,2,'Private retry history','2026-10-11','2026-09-01','2026-09-02')`, "push-"+id, id, state); err != nil {
		t.Fatal(err)
	}
	return id
}

func a2aBoundarySnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	rows := durableAuthoritySnapshot(t, st)
	for _, query := range []string{`SELECT * FROM a2a_tasks ORDER BY id`, `SELECT * FROM a2a_push_deliveries ORDER BY id`, `SELECT * FROM workflow_runs ORDER BY id`, `SELECT * FROM user_settings ORDER BY id`} {
		rows[query] = immutableConfigSnapshot(t, st, query)
	}
	return rows
}

func TestTaskManagerFabricReadCancelAndInputRefuseWithoutMutatingHistory(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)
	host := &a2aLaunchRecordingHost{}
	canceller := &fakeDurableAgentCanceller{}
	tm := &TaskManager{store: st, durableAgents: canceller, launcher: NewWorkflowLauncher(nil, host, nil, nil), pushNotifier: NewA2APushNotifier(st, nil), logger: testLogger(t)}
	durableHistoricalFixture(t, st, "retained-a2a-control", store.DurableAgentClassProcess, `["durable-agent"]`)
	const runID = "retained-a2a-control-run"
	if err := st.CreateWorkflowRun(t.Context(), &store.WorkflowRunRow{ID: runID, DefinitionName: "retained-a2a-control", Status: "waiting_on_gate"}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, kind := range []string{"instance", "workflow"} {
		executionID := runID
		if kind == "instance" {
			executionID = "historical-retained-a2a-control"
		}
		for _, state := range []a2a.TaskState{a2a.TaskStateSubmitted, a2a.TaskStateWorking, a2a.TaskStateInputRequired, a2a.TaskStateCanceled, a2a.TaskStateCompleted, a2a.TaskStateFailed, a2a.TaskStateRejected} {
			ids = append(ids, a2aRetainedTaskFixture(t, st, state, kind, executionID))
		}
	}
	ids = append(ids, "missing-task")
	before := a2aBoundarySnapshot(t, st)
	for _, id := range ids {
		task, err := tm.GetTask(t.Context(), id)
		if task != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("GetTask(%s)=%+v err=%v", id, task, err)
		}
		task, err = tm.CancelTask(t.Context(), id)
		if task != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("CancelTask(%s)=%+v err=%v", id, task, err)
		}
		if err = tm.ProvideTaskInput(t.Context(), id, "must not resume"); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("ProvideTaskInput(%s): %v", id, err)
		}
	}
	if len(canceller.calls) != 0 || len(host.calls) != 0 {
		t.Fatalf("held fabric invoked lifecycle/host: stop=%v host=%v", canceller.calls, host.calls)
	}
	if after := a2aBoundarySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("held fabric changed private history/authority: before=%v after=%v", before, after)
	}
}

func TestTaskManagerSubmitRefusesFabricIssuerBeforeWorkflowOrWake(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)
	historical := durableHistoricalFixture(t, st, "retained-a2a", store.DurableAgentClassProcess, `["durable-agent"]`)
	actor := &store.AgentProfile{Name: "Prior actor", Slug: "prior-a2a", SystemPrompt: "Private pin"}
	if err := persistTestActor(t.Context(), st, actor); err != nil {
		t.Fatal(err)
	}
	inst := &store.DurableAgentInstance{Name: "Prior instance", Slug: "prior-a2a", ProfileID: actor.ID}
	if err := persistTestDurableInstance(t.Context(), st, inst); err != nil {
		t.Fatal(err)
	}
	definition := agentworkflow.WorkflowDefinition{Name: "a2a-held-workflow", Steps: []agentworkflow.StepDefinition{{ID: "work", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop", "agent_id": actor.ID}}}}
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition})
	host := &a2aLaunchRecordingHost{}
	runtime := &fakeDurableRuntimeController{}
	durable := NewDurableAgentServiceWithRuntime(st, runtime)
	launcher := NewWorkflowLauncher(registry, host, &fakeStepExecutor{}, durable)
	tm := NewTaskManager(st, launcher, NewDurableAgentWakeService(st, durable), durable, registry, testLogger(t))
	before := a2aBoundarySnapshot(t, st)
	for _, target := range []string{definition.Name, actor.ID, "msg://agent/nanite/" + inst.ID, "msg://agent/nanite/" + historical.ID, "msg://agent/nanite/claimed"} {
		result, err := tm.SubmitTask(t.Context(), TaskSubmitRequest{Target: target, Message: "must not execute"})
		if result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("SubmitTask(%s)=%+v err=%v", target, result, err)
		}
	}
	if len(host.calls) != 0 || len(runtime.sent) != 0 || len(runtime.recovered) != 0 {
		t.Fatalf("held submit invoked execution: host=%v runtime=%+v", host.calls, runtime)
	}
	if after := a2aBoundarySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("held submit issued/replayed authority: before=%v after=%v", before, after)
	}
}

func TestTaskManagerWorkflowProfileResolutionRefusesHostSlugDefaultAndActorFallback(t *testing.T) {
	st := newA2ATaskManagerTestStore(t)
	historical := durableHistoricalFixture(t, st, "retained-resolution", store.DurableAgentClassProcess, `["durable-agent"]`)
	actor := &store.AgentProfile{Name: "Prior", Slug: "prior-resolution", SystemPrompt: "Private pin"}
	if err := persistTestActor(t.Context(), st, actor); err != nil {
		t.Fatal(err)
	}
	var hostID string
	if err := st.DB.QueryRowContext(t.Context(), `SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?`, actor.ID).Scan(&hostID); err != nil {
		t.Fatal(err)
	}
	// Private prior user preference row; settings cannot issue an actor.
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO user_settings(id,default_agent,updated_at) VALUES(1,'','2026-09-01')`); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"step", "default"} {
		for _, candidate := range []string{hostID, actor.Slug, historical.ID, historical.Slug, actor.ID, "missing", ""} {
			definition := agentworkflow.WorkflowDefinition{Name: "a2a-held-resolution", Steps: []agentworkflow.StepDefinition{{ID: "work", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}}}}
			settings, err := st.GetUserSettings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			settings.DefaultAgent = ""
			if source == "step" {
				definition.Steps[0].Config["agent_id"] = candidate
			} else {
				settings.DefaultAgent = candidate
			}
			if err = st.UpdateUserSettings(t.Context(), settings); err != nil {
				t.Fatal(err)
			}
			registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{definition.Name: definition})
			tm := NewTaskManager(st, nil, nil, nil, registry, testLogger(t))
			before := a2aBoundarySnapshot(t, st)
			profileID, err := tm.resolveA2AWorkflowProfile(t.Context(), definition.Name)
			if profileID != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("held %s candidate %q resolved %q err=%v", source, candidate, profileID, err)
			}
			if after := a2aBoundarySnapshot(t, st); !reflect.DeepEqual(before, after) {
				t.Fatal("held resolver changed history/authority")
			}
		}
	}
}
