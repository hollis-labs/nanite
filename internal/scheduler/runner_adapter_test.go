package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/loop"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- fakes ------------------------------------------------------------

type fakeDurableAgentWaker struct {
	gotInstanceID string
	gotReq        service.DurableAgentWakeRequest
	result        *service.DurableAgentWakeResult
	err           error
	called        bool
}

func (f *fakeDurableAgentWaker) Wake(ctx context.Context, instanceID string, req service.DurableAgentWakeRequest) (*service.DurableAgentWakeResult, error) {
	f.called = true
	f.gotInstanceID = instanceID
	f.gotReq = req
	if f.result == nil {
		f.result = &service.DurableAgentWakeResult{InstanceID: instanceID}
	}
	return f.result, f.err
}

type fakeWorkflowLauncher struct {
	gotReq service.WorkflowLaunchRequest
	result *service.WorkflowLaunchResult
	err    error
	called bool
}

func (f *fakeWorkflowLauncher) Launch(ctx context.Context, req service.WorkflowLaunchRequest) (*service.WorkflowLaunchResult, error) {
	f.called = true
	f.gotReq = req
	if f.result == nil {
		f.result = &service.WorkflowLaunchResult{WorkflowName: req.WorkflowName}
	}
	return f.result, f.err
}

type fakeCommandExecutor struct {
	gotAgentID string
	gotTool    string
	gotInput   map[string]any
	result     *service.ToolResult
	err        error
	called     bool
}

func (f *fakeCommandExecutor) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*service.ToolResult, error) {
	f.called = true
	f.gotAgentID = agentID
	f.gotTool = toolName
	f.gotInput = input
	if f.result == nil {
		f.result = &service.ToolResult{Output: "ok"}
	}
	return f.result, f.err
}

type fakeLoopResumer struct {
	gotLoopRunID string
	result       loop.LoopResult
	err          error
	called       bool
}

func (f *fakeLoopResumer) Resume(ctx context.Context, loopRunID string) (loop.LoopResult, error) {
	f.called = true
	f.gotLoopRunID = loopRunID
	return f.result, f.err
}

func fakeLoopRunLookupFor(rows map[string]*store.LoopRun) LoopRunLookup {
	return func(ctx context.Context, id string) (*store.LoopRun, error) {
		if r, ok := rows[id]; ok {
			return r, nil
		}
		return nil, store.ErrLoopRunNotFound
	}
}

func fakeReflexLookupFor(rows map[string]*store.AgentReflex) ReflexLookup {
	return func(ctx context.Context, id string) (*store.AgentReflex, error) {
		if r, ok := rows[id]; ok {
			return r, nil
		}
		return nil, store.ErrAgentReflexNotFound
	}
}

func testExecutor() *reflexes.Executor {
	return &reflexes.Executor{Logger: slog.Default()}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// --- per-job-type happy-path dispatch tests -----------------------------

func TestEnqueue_DurableAgentWake_Dispatches(t *testing.T) {
	waker := &fakeDurableAgentWaker{}
	r := &RunnerAdapter{Wake: waker}

	payload := DurableAgentWakePayload{
		InstanceID: "inst-1",
		ProjectID:  "proj-1",
		Reason:     "scheduled_wake",
		Prompt:     "do the thing",
		Facts:      map[string]string{"k": "v"},
	}
	job := gosched.Job{
		ScheduleID: "sched-1",
		RunID:      "run-1",
		JobType:    JobTypeDurableAgentWake,
		Payload:    mustJSON(t, payload),
	}

	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
	if !waker.called {
		t.Fatalf("expected Wake to be called")
	}
	if waker.gotInstanceID != "inst-1" {
		t.Errorf("instanceID = %q, want inst-1", waker.gotInstanceID)
	}
	if waker.gotReq.ProjectID != "proj-1" {
		t.Errorf("ProjectID = %q, want proj-1", waker.gotReq.ProjectID)
	}
	if waker.gotReq.WakePayload.Reason != "scheduled_wake" {
		t.Errorf("Reason = %q, want scheduled_wake", waker.gotReq.WakePayload.Reason)
	}
	if waker.gotReq.WakePayload.Prompt != "do the thing" {
		t.Errorf("Prompt = %q, want %q", waker.gotReq.WakePayload.Prompt, "do the thing")
	}
	if waker.gotReq.WakePayload.Facts["k"] != "v" {
		t.Errorf("Facts[k] = %q, want v", waker.gotReq.WakePayload.Facts["k"])
	}
}

// TestEnqueue_DurableAgentWake_SkippedIsNotAnError proves the documented
// finding: Wake's own "already active"/skip verdict comes back as a
// nil-error, Skipped=true result — this Runner must not reinterpret that
// as gosched.ErrDuplicateJob (there is no real duplicate-run signal here,
// only a legitimate business skip Wake itself already resolved).
func TestEnqueue_DurableAgentWake_SkippedIsNotAnError(t *testing.T) {
	waker := &fakeDurableAgentWaker{
		result: &service.DurableAgentWakeResult{Skipped: true, SkipReason: "wake already active"},
	}
	r := &RunnerAdapter{Wake: waker}

	job := gosched.Job{
		RunID:   "run-2",
		JobType: JobTypeDurableAgentWake,
		Payload: mustJSON(t, DurableAgentWakePayload{InstanceID: "inst-1"}),
	}
	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: expected nil error on a Skipped result, got %v", err)
	}
}

func TestEnqueue_AgentWorkflowRun_Dispatches(t *testing.T) {
	launcher := &fakeWorkflowLauncher{}
	r := &RunnerAdapter{Workflows: launcher}

	payload := AgentWorkflowRunPayload{
		WorkflowName:   "daily-audit",
		Params:         map[string]any{"scope": "all"},
		ProjectID:      "proj-1",
		AgentProfileID: "profile-1",
		TimeoutSeconds: 60,
	}
	job := gosched.Job{
		RunID:   "run-3",
		JobType: JobTypeAgentWorkflowRun,
		Payload: mustJSON(t, payload),
	}

	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
	if !launcher.called {
		t.Fatalf("expected Launch to be called")
	}
	if launcher.gotReq.WorkflowName != "daily-audit" {
		t.Errorf("WorkflowName = %q, want daily-audit", launcher.gotReq.WorkflowName)
	}
	if launcher.gotReq.AgentProfileID != "profile-1" {
		t.Errorf("AgentProfileID = %q, want profile-1", launcher.gotReq.AgentProfileID)
	}
	if launcher.gotReq.Params["scope"] != "all" {
		t.Errorf("Params[scope] = %v, want all", launcher.gotReq.Params["scope"])
	}
	if launcher.gotReq.TimeoutSeconds != 60 {
		t.Errorf("TimeoutSeconds = %d, want 60", launcher.gotReq.TimeoutSeconds)
	}
}

func TestEnqueue_AgentWorkflowRun_MissingAgentProfileID(t *testing.T) {
	launcher := &fakeWorkflowLauncher{}
	r := &RunnerAdapter{Workflows: launcher}

	job := gosched.Job{
		RunID:   "run-3b",
		JobType: JobTypeAgentWorkflowRun,
		Payload: mustJSON(t, AgentWorkflowRunPayload{WorkflowName: "daily-audit"}),
	}
	if err := r.Enqueue(context.Background(), job); err == nil {
		t.Fatalf("Enqueue: expected error for missing agent_profile_id")
	}
	if launcher.called {
		t.Fatalf("Launch should not have been called")
	}
}

func TestEnqueue_CommandRun_Dispatches(t *testing.T) {
	exec := &fakeCommandExecutor{}
	r := &RunnerAdapter{Commands: exec}

	payload := CommandRunPayload{
		AgentID: "agent-1",
		Command: "audit_report_generate",
		Args:    map[string]any{"days": float64(7)},
	}
	job := gosched.Job{
		RunID:   "run-4",
		JobType: JobTypeCommandRun,
		Payload: mustJSON(t, payload),
	}

	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
	if !exec.called {
		t.Fatalf("expected Execute to be called")
	}
	if exec.gotAgentID != "agent-1" {
		t.Errorf("agentID = %q, want agent-1", exec.gotAgentID)
	}
	if exec.gotTool != "audit_report_generate" {
		t.Errorf("toolName = %q, want audit_report_generate", exec.gotTool)
	}
	if exec.gotInput["days"] != float64(7) {
		t.Errorf("Args[days] = %v, want 7", exec.gotInput["days"])
	}
}

func TestEnqueue_CommandRun_ToolErrorSurfacesAsEnqueueError(t *testing.T) {
	exec := &fakeCommandExecutor{result: &service.ToolResult{IsError: true, Output: "unknown tool"}}
	r := &RunnerAdapter{Commands: exec}

	job := gosched.Job{
		RunID:   "run-4b",
		JobType: JobTypeCommandRun,
		Payload: mustJSON(t, CommandRunPayload{Command: "nonexistent_tool"}),
	}
	err := r.Enqueue(context.Background(), job)
	if err == nil {
		t.Fatalf("Enqueue: expected error when ToolResult.IsError is true")
	}
}

func TestEnqueue_ReflexDispatch_Dispatches(t *testing.T) {
	reflex := &store.AgentReflex{
		ID:         "rfx-1",
		AgentID:    "agent-1",
		ClassTag:   "process",
		Name:       "nightly-reminder",
		ActionKind: store.ReflexActionInjectReminder,
		ActionSpec: `{"body":"remember to check in"}`,
		Status:     store.ReflexStatusActive,
	}
	r := &RunnerAdapter{
		ReflexLookup:   fakeReflexLookupFor(map[string]*store.AgentReflex{"rfx-1": reflex}),
		ReflexExecutor: testExecutor(),
	}

	job := gosched.Job{
		RunID:   "run-5",
		JobType: JobTypeReflexDispatch,
		Payload: mustJSON(t, ReflexDispatchPayload{ReflexID: "rfx-1", SessionID: "sess-1"}),
	}
	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
}

func TestEnqueue_ReflexDispatch_InactiveReflexIsNotAnError(t *testing.T) {
	reflex := &store.AgentReflex{
		ID:         "rfx-2",
		ActionKind: store.ReflexActionInjectReminder,
		Status:     store.ReflexStatusPaused,
	}
	r := &RunnerAdapter{
		ReflexLookup:   fakeReflexLookupFor(map[string]*store.AgentReflex{"rfx-2": reflex}),
		ReflexExecutor: testExecutor(),
	}

	job := gosched.Job{
		RunID:   "run-5b",
		JobType: JobTypeReflexDispatch,
		Payload: mustJSON(t, ReflexDispatchPayload{ReflexID: "rfx-2"}),
	}
	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: expected nil error for a paused reflex, got %v", err)
	}
}

func TestEnqueue_ReflexDispatch_UnknownReflexIsAnError(t *testing.T) {
	r := &RunnerAdapter{
		ReflexLookup:   fakeReflexLookupFor(nil),
		ReflexExecutor: testExecutor(),
	}
	job := gosched.Job{
		RunID:   "run-5c",
		JobType: JobTypeReflexDispatch,
		Payload: mustJSON(t, ReflexDispatchPayload{ReflexID: "does-not-exist"}),
	}
	if err := r.Enqueue(context.Background(), job); err == nil {
		t.Fatalf("Enqueue: expected error for unknown reflex_id")
	}
}

// --- loop_run_tick dispatch tests (TASKS/loops/12) ------------------------

func TestEnqueue_LoopRunTick_WaitingOnEscalation_ResumesLoop(t *testing.T) {
	resumer := &fakeLoopResumer{}
	r := &RunnerAdapter{
		Loops: resumer,
		LoopRunLookup: fakeLoopRunLookupFor(map[string]*store.LoopRun{
			"lr-1": {ID: "lr-1", Status: store.LoopRunStatusWaitingOnEscalation},
		}),
	}

	job := gosched.Job{
		RunID:   "run-7",
		JobType: JobTypeLoopRunTick,
		Payload: mustJSON(t, LoopRunTickPayload{LoopRunID: "lr-1"}),
	}
	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
	if !resumer.called {
		t.Fatalf("expected Resume to be called")
	}
	if resumer.gotLoopRunID != "lr-1" {
		t.Errorf("gotLoopRunID = %q, want lr-1", resumer.gotLoopRunID)
	}
}

func TestEnqueue_LoopRunTick_WaitingOnGate_ResumesLoop(t *testing.T) {
	resumer := &fakeLoopResumer{}
	r := &RunnerAdapter{
		Loops: resumer,
		LoopRunLookup: fakeLoopRunLookupFor(map[string]*store.LoopRun{
			"lr-2": {ID: "lr-2", Status: store.LoopRunStatusWaitingOnGate},
		}),
	}

	job := gosched.Job{
		RunID:   "run-7b",
		JobType: JobTypeLoopRunTick,
		Payload: mustJSON(t, LoopRunTickPayload{LoopRunID: "lr-2"}),
	}
	if err := r.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: unexpected error: %v", err)
	}
	if !resumer.called {
		t.Fatalf("expected Resume to be called")
	}
}

// TestEnqueue_LoopRunTick_NotResumableStatusIsNotAnError proves this task's
// own no-op guard: a tick against a LoopRun no longer in a resumable status
// (running, completed, failed, canceled) is a cheap no-op, not an error --
// and, critically, Resume is never called (calling it would itself return a
// hard error, per *loop.LoopEngine.Resume's own switch).
func TestEnqueue_LoopRunTick_NotResumableStatusIsNotAnError(t *testing.T) {
	for _, status := range []string{
		store.LoopRunStatusRunning,
		store.LoopRunStatusCompleted,
		store.LoopRunStatusFailed,
		store.LoopRunStatusCanceled,
	} {
		t.Run(status, func(t *testing.T) {
			resumer := &fakeLoopResumer{}
			r := &RunnerAdapter{
				Loops: resumer,
				LoopRunLookup: fakeLoopRunLookupFor(map[string]*store.LoopRun{
					"lr-3": {ID: "lr-3", Status: status},
				}),
			}
			job := gosched.Job{
				RunID:   "run-8-" + status,
				JobType: JobTypeLoopRunTick,
				Payload: mustJSON(t, LoopRunTickPayload{LoopRunID: "lr-3"}),
			}
			if err := r.Enqueue(context.Background(), job); err != nil {
				t.Fatalf("Enqueue: expected nil error for status %q, got %v", status, err)
			}
			if resumer.called {
				t.Fatalf("status %q: Resume should not have been called", status)
			}
		})
	}
}

func TestEnqueue_LoopRunTick_UnknownLoopRunIsAnError(t *testing.T) {
	resumer := &fakeLoopResumer{}
	r := &RunnerAdapter{
		Loops:         resumer,
		LoopRunLookup: fakeLoopRunLookupFor(nil),
	}
	job := gosched.Job{
		RunID:   "run-9",
		JobType: JobTypeLoopRunTick,
		Payload: mustJSON(t, LoopRunTickPayload{LoopRunID: "does-not-exist"}),
	}
	if err := r.Enqueue(context.Background(), job); err == nil {
		t.Fatalf("Enqueue: expected error for unknown loop_run_id")
	}
	if resumer.called {
		t.Fatalf("Resume should not have been called")
	}
}

func TestEnqueue_LoopRunTick_MissingLoopRunID(t *testing.T) {
	resumer := &fakeLoopResumer{}
	r := &RunnerAdapter{Loops: resumer, LoopRunLookup: fakeLoopRunLookupFor(nil)}
	job := gosched.Job{
		RunID:   "run-9b",
		JobType: JobTypeLoopRunTick,
		Payload: mustJSON(t, LoopRunTickPayload{}),
	}
	if err := r.Enqueue(context.Background(), job); err == nil {
		t.Fatalf("Enqueue: expected error for missing loop_run_id")
	}
	if resumer.called {
		t.Fatalf("Resume should not have been called")
	}
}

// --- not-configured guard -------------------------------------------------

func TestEnqueue_NotConfiguredReturnsClearError(t *testing.T) {
	r := &RunnerAdapter{}
	for _, jobType := range []string{JobTypeDurableAgentWake, JobTypeAgentWorkflowRun, JobTypeCommandRun, JobTypeReflexDispatch, JobTypeLoopRunTick} {
		job := gosched.Job{RunID: "run-x", JobType: jobType, Payload: []byte(`{}`)}
		if err := r.Enqueue(context.Background(), job); err == nil {
			t.Errorf("job type %q: expected a not-configured error on a zero-value RunnerAdapter", jobType)
		}
	}
}

// --- unknown job type -------------------------------------------------

func TestEnqueue_UnknownJobType(t *testing.T) {
	r := &RunnerAdapter{}
	job := gosched.Job{RunID: "run-6", JobType: "something_else", Payload: []byte(`{}`)}
	if err := r.Enqueue(context.Background(), job); err == nil {
		t.Fatalf("Enqueue: expected error for unknown job type")
	}
}

// --- malformed payload: bad JSON, not a panic -----------------------------

func TestEnqueue_MalformedPayload_ReturnsErrorNotPanic(t *testing.T) {
	cases := []struct {
		name    string
		r       *RunnerAdapter
		jobType string
		payload []byte
	}{
		{
			name:    "durable_agent_wake invalid JSON",
			r:       &RunnerAdapter{Wake: &fakeDurableAgentWaker{}},
			jobType: JobTypeDurableAgentWake,
			payload: []byte(`{not valid json`),
		},
		{
			name:    "agent_workflow_run wrong shape (JSON array)",
			r:       &RunnerAdapter{Workflows: &fakeWorkflowLauncher{}},
			jobType: JobTypeAgentWorkflowRun,
			payload: []byte(`[1,2,3]`),
		},
		{
			name:    "command_run invalid JSON",
			r:       &RunnerAdapter{Commands: &fakeCommandExecutor{}},
			jobType: JobTypeCommandRun,
			payload: []byte(`{"command": "x",`),
		},
		{
			name: "reflex_dispatch wrong shape (JSON array)",
			r: &RunnerAdapter{
				ReflexLookup:   fakeReflexLookupFor(nil),
				ReflexExecutor: testExecutor(),
			},
			jobType: JobTypeReflexDispatch,
			payload: []byte(`["not", "an", "object"]`),
		},
		{
			name: "loop_run_tick invalid JSON",
			r: &RunnerAdapter{
				Loops:         &fakeLoopResumer{},
				LoopRunLookup: fakeLoopRunLookupFor(nil),
			},
			jobType: JobTypeLoopRunTick,
			payload: []byte(`{"loop_run_id":`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("Enqueue panicked on malformed payload: %v", rec)
				}
			}()
			job := gosched.Job{RunID: "run-malformed", JobType: tc.jobType, Payload: tc.payload}
			err := tc.r.Enqueue(context.Background(), job)
			if err == nil {
				t.Fatalf("Enqueue: expected a decode error for malformed payload, got nil")
			}
		})
	}
}

// Compile-time assertion mirrored here too, so a future edit that breaks
// interface satisfaction fails this package's own tests, not just the
// build.
var _ gosched.Runner = (*RunnerAdapter)(nil)
