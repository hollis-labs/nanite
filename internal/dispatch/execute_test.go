package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeSpawner records what ExecuteTask asked it to spawn so tests can
// assert on the role/surface delegation, and returns a configurable
// SpawnResult.
type fakeSpawner struct {
	got    SpawnRequest
	called int
	result *SpawnResult
	err    error
}

func (f *fakeSpawner) Spawn(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
	f.called++
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// recordingWrapper captures what the wrapper was asked to wrap so tests
// can assert that raw Worker output never escapes around the envelope
// path.
type recordingWrapper struct {
	gotRole   Role
	gotResult *SpawnResult
	out       Envelope
	err       error
}

func (r *recordingWrapper) Wrap(role Role, result *SpawnResult) (Envelope, error) {
	r.gotRole = role
	r.gotResult = result
	if r.err != nil {
		return Envelope{}, r.err
	}
	return r.out, nil
}

// TestExecuteTask_E2E_DispatchToWorker is the end-to-end gate for B3:
// a user message arrives → ExecuteTask classifies → spawns a Worker with
// the Worker slug (NOT the Chat surface) → wraps the result in an
// envelope → returns the envelope. Raw Worker output is reachable ONLY
// via the envelope, never as a side-channel string the caller could leak
// into Chat's context.
func TestExecuteTask_E2E_DispatchToWorker(t *testing.T) {
	spawner := &fakeSpawner{
		result: &SpawnResult{
			Summary:      "worker did the thing",
			EnvelopeJSON: `{"kind":"envelope","version":1,"type":"report-card","title":"Done","data":{"detail":"complete"}}`,
		},
	}
	wrapper := &recordingWrapper{
		out: Envelope{
			Kind:    "envelope",
			Version: 1,
			Type:    "report-card",
			Title:   "Done",
			Data:    map[string]any{"detail": "complete"},
		},
	}

	args := ExecuteTaskArgs{
		SessionID:     "session-abc",
		ParentAgentID: "file-default",
		Message:       "fix the typo in README",
	}

	env, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args)
	if err != nil {
		t.Fatalf("ExecuteTask returned error: %v", err)
	}

	// Envelope round-trip.
	if env.Type != "report-card" {
		t.Errorf("envelope type = %q, want report-card", env.Type)
	}

	// Spawner called with Worker (not Chat) slug — the static surface
	// for Chat must NOT have been forwarded to the spawned role. The
	// spawned role's surface is owned by its own profile.
	if spawner.called != 1 {
		t.Fatalf("spawner called %d times, want 1", spawner.called)
	}
	if spawner.got.Role != WorkerRoleSlug {
		t.Errorf("spawned role slug = %q, want %q (the Worker slug; never the Chat surface)", spawner.got.Role, WorkerRoleSlug)
	}
	if spawner.got.ParentSessionID != "session-abc" {
		t.Errorf("ParentSessionID = %q, want session-abc", spawner.got.ParentSessionID)
	}
	if spawner.got.Prompt != args.Message {
		t.Errorf("spawn Prompt = %q, want %q", spawner.got.Prompt, args.Message)
	}

	// Wrapper saw the SpawnResult — and the wrapper's output is what we
	// returned. The caller has no other path to the raw worker output.
	if wrapper.gotRole != RoleWorker {
		t.Errorf("wrapper role = %v, want RoleWorker", wrapper.gotRole)
	}
	if wrapper.gotResult == nil {
		t.Fatal("wrapper did not receive SpawnResult")
	}
	if wrapper.gotResult.Summary != "worker did the thing" {
		t.Errorf("wrapper got summary %q, want %q", wrapper.gotResult.Summary, "worker did the thing")
	}
}

// TestExecuteTask_E2E_OpenScopeRoutesToPlanner asserts that an open-scope
// task (TierOpen × PatternSubagent) maps to the Planner role.
func TestExecuteTask_E2E_OpenScopeRoutesToPlanner(t *testing.T) {
	spawner := &fakeSpawner{result: &SpawnResult{Summary: "planned"}}
	wrapper := &recordingWrapper{out: Envelope{Kind: "envelope", Version: 1, Type: "report-card"}}

	// "build a complete X" hits ScopeTierOpenKeywords → TierOpen, and
	// the size-driven rule promotes PatternSubagent → maps to Planner.
	args := ExecuteTaskArgs{
		SessionID: "s1",
		Message:   "build a complete authentication system with full implementation across the codebase",
	}

	if _, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args); err != nil {
		t.Fatalf("ExecuteTask returned error: %v", err)
	}

	if spawner.got.Role != PlannerRoleSlug {
		t.Errorf("spawn role slug = %q, want %q (open-scope subagent must route to Planner)", spawner.got.Role, PlannerRoleSlug)
	}
	if wrapper.gotRole != RolePlanner {
		t.Errorf("wrapper role = %v, want RolePlanner", wrapper.gotRole)
	}
}

// TestExecuteTask_BackgroundIsCoercedToSyncForCapture documents the
// capture contract: ExecuteTask must return the worker output to its
// caller, so async/background dispatch is forced to sync at the seam.
func TestExecuteTask_BackgroundIsCoercedToSyncForCapture(t *testing.T) {
	spawner := &fakeSpawner{result: &SpawnResult{Summary: "ok"}}
	wrapper := &recordingWrapper{out: Envelope{Kind: "envelope", Version: 1, Type: "report-card"}}

	args := ExecuteTaskArgs{
		SessionID: "s1",
		// "in the background" matches ExecutionPatternBackgroundKeywords.
		Message: "run the migration in the background",
	}

	if _, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args); err != nil {
		t.Fatalf("ExecuteTask returned error: %v", err)
	}

	if spawner.got.Mode != "sync" {
		t.Errorf("spawn mode = %q, want sync (ExecuteTask must capture, so async is coerced)", spawner.got.Mode)
	}
}

// TestExecuteTask_ValidatesArgs covers the input-validation paths.
func TestExecuteTask_ValidatesArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    ExecuteTaskArgs
		wantErr string
	}{
		{
			name:    "empty message",
			args:    ExecuteTaskArgs{SessionID: "s1", Message: "   "},
			wantErr: "message is required",
		},
		{
			name:    "empty session id",
			args:    ExecuteTaskArgs{Message: "do the thing"},
			wantErr: "session_id is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExecuteTask(context.Background(), &fakeSpawner{}, &recordingWrapper{}, nil, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestExecuteTask_SpawnerErrorPropagates ensures spawn failures surface
// to the caller — the seam does not silently swallow worker errors.
func TestExecuteTask_SpawnerErrorPropagates(t *testing.T) {
	spawner := &fakeSpawner{err: errors.New("worker unavailable")}
	wrapper := &recordingWrapper{}
	args := ExecuteTaskArgs{SessionID: "s1", Message: "do the thing"}

	_, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args)
	if err == nil || !strings.Contains(err.Error(), "worker unavailable") {
		t.Errorf("err = %v, want spawn-error wrapped", err)
	}
}

// TestExecuteTask_RequiresSpawnerAndWrapper guards the wiring contract.
func TestExecuteTask_RequiresSpawnerAndWrapper(t *testing.T) {
	args := ExecuteTaskArgs{SessionID: "s1", Message: "do the thing"}

	if _, err := ExecuteTask(context.Background(), nil, &recordingWrapper{}, nil, args); !errors.Is(err, ErrNoSpawner) {
		t.Errorf("nil spawner err = %v, want ErrNoSpawner", err)
	}
	if _, err := ExecuteTask(context.Background(), &fakeSpawner{}, nil, nil, args); !errors.Is(err, ErrNoWrapper) {
		t.Errorf("nil wrapper err = %v, want ErrNoWrapper", err)
	}
}

// TestDefaultEnvelopeWrapper_PrefersWorkerEnvelope asserts that a
// well-formed envelope JSON from the worker round-trips verbatim — we
// don't synthesize a default when the worker already emitted one.
func TestDefaultEnvelopeWrapper_PrefersWorkerEnvelope(t *testing.T) {
	w := DefaultEnvelopeWrapper{}
	res := &SpawnResult{
		Summary:      "ignored",
		EnvelopeJSON: `{"kind":"envelope","version":1,"type":"report-card","title":"From Worker","data":{"k":"v"}}`,
	}
	env, err := w.Wrap(RoleWorker, res)
	if err != nil {
		t.Fatalf("Wrap err = %v", err)
	}
	if env.Title != "From Worker" {
		t.Errorf("title = %q, want From Worker (worker envelope must round-trip)", env.Title)
	}
	if env.Type != "report-card" {
		t.Errorf("type = %q, want report-card", env.Type)
	}
}

// TestDefaultEnvelopeWrapper_SynthesizesWhenWorkerEmittedProse covers
// the fallback when the worker only returned a summary string.
func TestDefaultEnvelopeWrapper_SynthesizesWhenWorkerEmittedProse(t *testing.T) {
	w := DefaultEnvelopeWrapper{}
	res := &SpawnResult{Summary: "did the thing", EnvelopeJSON: ""}
	env, err := w.Wrap(RoleWorker, res)
	if err != nil {
		t.Fatalf("Wrap err = %v", err)
	}
	if env.Type != "report-card" {
		t.Errorf("type = %q, want report-card", env.Type)
	}
	if got, _ := env.Data["summary"].(string); got != "did the thing" {
		t.Errorf("summary in envelope = %q, want %q", got, "did the thing")
	}
	if got, _ := env.Data["role"].(string); got != "worker" {
		t.Errorf("role in envelope = %q, want worker", got)
	}
}

// TestDefaultEnvelopeWrapper_TreatsEmptyAsProse — `{}` is what
// ChatRunner.drainCapture returns when the worker emitted no envelope
// event. The wrapper must NOT round-trip that as a real envelope; it
// must synthesize one.
func TestDefaultEnvelopeWrapper_TreatsEmptyAsProse(t *testing.T) {
	w := DefaultEnvelopeWrapper{}
	res := &SpawnResult{Summary: "x", EnvelopeJSON: "{}"}
	env, err := w.Wrap(RoleWorker, res)
	if err != nil {
		t.Fatalf("Wrap err = %v", err)
	}
	if env.Type != "report-card" {
		t.Errorf("type = %q, want synthesized report-card", env.Type)
	}
	if got, _ := env.Data["summary"].(string); got != "x" {
		t.Errorf("summary = %q, want x", got)
	}
}

// TestExecuteTask_WorkspaceAndProfileThreadedToSpawn verifies that
// ExecuteTaskArgs.WorkspaceID and .AgentProfileID are forwarded verbatim
// to the SpawnRequest so the subagent trust gate can fire. H1 CW-20260421-0014.
func TestExecuteTask_WorkspaceAndProfileThreadedToSpawn(t *testing.T) {
	spawner := &fakeSpawner{result: &SpawnResult{Summary: "ok"}}
	wrapper := &recordingWrapper{out: Envelope{Kind: "envelope", Version: 1, Type: "report-card"}}

	args := ExecuteTaskArgs{
		SessionID:      "sess-trust",
		Message:        "do the thing",
		WorkspaceID:    "ws-dogfood",
		AgentProfileID: "ap-worker-id",
	}

	if _, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args); err != nil {
		t.Fatalf("ExecuteTask error: %v", err)
	}

	if spawner.got.WorkspaceID != "ws-dogfood" {
		t.Errorf("WorkspaceID = %q, want ws-dogfood", spawner.got.WorkspaceID)
	}
	if spawner.got.AgentProfileID != "ap-worker-id" {
		t.Errorf("AgentProfileID = %q, want ap-worker-id", spawner.got.AgentProfileID)
	}
}

// fakeWorkflowLauncher records what ExecuteTask asked it to launch and
// returns a configurable SpawnResult, mirroring fakeSpawner.
type fakeWorkflowLauncher struct {
	got    WorkflowLaunchRequest
	called int
	result *SpawnResult
	err    error
}

func (f *fakeWorkflowLauncher) Launch(_ context.Context, req WorkflowLaunchRequest) (*SpawnResult, error) {
	f.called++
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// TestExecuteTask_ReflexWorkflowHint_RoutesToWorkflowLauncher asserts that
// a ReflexHints.WorkflowName override bypasses AssignRole's Worker/Planner
// mapping entirely and calls the WorkflowLauncher instead of Spawner —
// CW-20260813-0014's "new outcome alongside spawning a bare Worker/Planner".
func TestExecuteTask_ReflexWorkflowHint_RoutesToWorkflowLauncher(t *testing.T) {
	spawner := &fakeSpawner{result: &SpawnResult{Summary: "should not be called"}}
	launcher := &fakeWorkflowLauncher{
		result: &SpawnResult{Summary: "workflow \"onboard-user\" run r1: completed."},
	}
	wrapper := &recordingWrapper{out: Envelope{Kind: "envelope", Version: 1, Type: "report-card"}}

	args := ExecuteTaskArgs{
		SessionID:      "sess-wf",
		Message:        "onboard the new user",
		WorkspaceID:    "ws-1",
		AgentProfileID: "ap-1",
		ReflexHints:    &ReflexHints{WorkflowName: "onboard-user"},
	}

	env, err := ExecuteTask(context.Background(), spawner, wrapper, launcher, args)
	if err != nil {
		t.Fatalf("ExecuteTask returned error: %v", err)
	}
	if env.Type != "report-card" {
		t.Errorf("envelope type = %q, want report-card", env.Type)
	}

	if spawner.called != 0 {
		t.Errorf("spawner called %d times, want 0 (workflow route must not spawn)", spawner.called)
	}
	if launcher.called != 1 {
		t.Fatalf("launcher called %d times, want 1", launcher.called)
	}
	if launcher.got.WorkflowName != "onboard-user" {
		t.Errorf("launched workflow = %q, want onboard-user", launcher.got.WorkflowName)
	}
	if launcher.got.WorkspaceID != "ws-1" || launcher.got.AgentProfileID != "ap-1" {
		t.Errorf("launch req = %+v, want workspace/profile threaded through", launcher.got)
	}
	if launcher.got.ParentSessionID != "sess-wf" {
		t.Errorf("launch req ParentSessionID = %q, want sess-wf", launcher.got.ParentSessionID)
	}

	if wrapper.gotRole != RoleWorkflow {
		t.Errorf("wrapper role = %v, want RoleWorkflow", wrapper.gotRole)
	}
}

// TestExecuteTask_WorkflowRoute_RequiresLauncher guards the wiring
// contract for the workflow route, mirroring
// TestExecuteTask_RequiresSpawnerAndWrapper.
func TestExecuteTask_WorkflowRoute_RequiresLauncher(t *testing.T) {
	spawner := &fakeSpawner{}
	wrapper := &recordingWrapper{}
	args := ExecuteTaskArgs{
		SessionID:   "s1",
		Message:     "do the thing",
		ReflexHints: &ReflexHints{WorkflowName: "some-workflow"},
	}

	if _, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args); !errors.Is(err, ErrNoWorkflowLauncher) {
		t.Errorf("nil launcher err = %v, want ErrNoWorkflowLauncher", err)
	}
}

// TestExecuteTask_EmptyWorkspaceProfile_FallsThrough verifies that when no
// WorkspaceID / AgentProfileID are set, the SpawnRequest carries empty
// strings (which causes the subagent gate to fall back to TrustNormal).
func TestExecuteTask_EmptyWorkspaceProfile_FallsThrough(t *testing.T) {
	spawner := &fakeSpawner{result: &SpawnResult{Summary: "ok"}}
	wrapper := &recordingWrapper{out: Envelope{Kind: "envelope", Version: 1, Type: "report-card"}}

	args := ExecuteTaskArgs{
		SessionID: "sess-no-trust",
		Message:   "do the thing",
		// WorkspaceID and AgentProfileID intentionally omitted.
	}

	if _, err := ExecuteTask(context.Background(), spawner, wrapper, nil, args); err != nil {
		t.Fatalf("ExecuteTask error: %v", err)
	}

	if spawner.got.WorkspaceID != "" {
		t.Errorf("expected empty WorkspaceID fallback, got %q", spawner.got.WorkspaceID)
	}
	if spawner.got.AgentProfileID != "" {
		t.Errorf("expected empty AgentProfileID fallback, got %q", spawner.got.AgentProfileID)
	}
}
