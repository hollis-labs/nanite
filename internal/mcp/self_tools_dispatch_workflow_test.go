package mcp

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/promptrouter"
)

// stubWorkflowLauncher records the request it was launched with and
// returns a canned SpawnResult.
type stubWorkflowLauncher struct {
	saw    dispatch.WorkflowLaunchRequest
	calls  int
	result *dispatch.SpawnResult
	err    error
}

func (l *stubWorkflowLauncher) Launch(_ context.Context, req dispatch.WorkflowLaunchRequest) (*dispatch.SpawnResult, error) {
	l.saw = req
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	if l.result != nil {
		return l.result, nil
	}
	return &dispatch.SpawnResult{Summary: "workflow done"}, nil
}

// noCallSpawner fails the test if Spawn is invoked — proves the reflex
// match routed to the workflow launcher instead of the freeform
// Worker/Planner spawn path.
type noCallSpawner struct{ t *testing.T }

func (s *noCallSpawner) Spawn(context.Context, dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	s.t.Fatal("dispatch spawner invoked despite reflex routing to a workflow")
	return nil, nil
}

// recordingSpawner records how many times Spawn was invoked and returns a
// canned result.
type recordingSpawner struct {
	result *dispatch.SpawnResult
	calls  int
}

func (s *recordingSpawner) Spawn(context.Context, dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	s.calls++
	return s.result, nil
}

// TestCallExecuteTask_ReflexWorkflowName_RoutesToWorkflowLauncher is the
// CW-20260814-0002 regression: a matched reflex whose ResolvesTo.WorkflowName
// is set must reach dispatch.ReflexHints.WorkflowName and, from there,
// dispatch.ExecuteTask's RoleWorkflow branch — not the freeform
// Worker/Planner spawn path. Before this fix, self_tools_dispatch.go built
// ReflexHints without this field, so RoleWorkflow was unreachable in
// production even though internal/dispatch/execute.go fully implements and
// tests it (execute_test.go).
func TestCallExecuteTask_ReflexWorkflowName_RoutesToWorkflowLauncher(t *testing.T) {
	workflowReflex := promptrouter.Reflex{
		ID: "onboard-workflow",
		Triggers: promptrouter.Triggers{
			UserPhraseAnyOf: []string{"onboard the new hire"},
		},
		ResolvesTo: promptrouter.Resolution{WorkflowName: "onboard-user"},
		Priority:   50,
	}

	launcher := &stubWorkflowLauncher{}
	st := &SelfToolsTransport{
		Dispatch:         &noCallSpawner{t: t},
		WorkflowLauncher: launcher,
		ReflexSet:        []promptrouter.Reflex{workflowReflex},
	}

	res, err := st.callExecuteTask(context.Background(), map[string]any{
		"session_id": "sess-1",
		"message":    "please onboard the new hire",
	})
	if err != nil {
		t.Fatalf("callExecuteTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %+v", res)
	}
	if launcher.calls != 1 {
		t.Fatalf("expected WorkflowLauncher.Launch to be called once, got %d", launcher.calls)
	}
	if launcher.saw.WorkflowName != "onboard-user" {
		t.Errorf("launched workflow = %q, want onboard-user", launcher.saw.WorkflowName)
	}
}

// TestCallExecuteTask_NonWorkflowReflex_LeavesWorkflowNameEmpty guards the
// CW-20260814-0002 non-goal: an ordinary (non-workflow) reflex match must
// not spuriously populate ReflexHints.WorkflowName, which would force
// dispatch.ExecuteTask onto the RoleWorkflow branch instead of the normal
// Worker/Planner spawn.
func TestCallExecuteTask_NonWorkflowReflex_LeavesWorkflowNameEmpty(t *testing.T) {
	spawner := &recordingSpawner{result: &dispatch.SpawnResult{Summary: "worker done"}}
	st := &SelfToolsTransport{
		Dispatch:  spawner,
		ReflexSet: promptrouter.BuiltinReflexes(),
	}

	res, err := st.callExecuteTask(context.Background(), map[string]any{
		"session_id": "sess-2",
		"message":    "Implement the reflex matcher module",
	})
	if err != nil {
		t.Fatalf("callExecuteTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %+v", res)
	}
	if spawner.calls != 1 {
		t.Fatalf("expected Spawn to be called once, got %d", spawner.calls)
	}
}
