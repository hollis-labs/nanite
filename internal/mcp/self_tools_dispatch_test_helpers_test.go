package mcp

import (
	"context"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// recordingSpawner records how many times Spawn was invoked and returns a
// canned result. Shared test double for dispatch.Spawner — used by
// self_tools_dispatch_audit_test.go and any other test that needs a
// task_execute call to succeed without a real subagent spawn.
type recordingSpawner struct {
	result *dispatch.SpawnResult
	calls  int
}

func (s *recordingSpawner) Spawn(context.Context, dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	s.calls++
	return s.result, nil
}

// stubWorkflowLauncher records the request it was launched with and
// returns a canned SpawnResult. Shared test double for
// dispatch.WorkflowLauncher — used by self_tools_workflow_run_test.go
// (the workflow_run self-tool's own direct-invocation tests, independent
// of dispatch_to_agent reflex routing).
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
