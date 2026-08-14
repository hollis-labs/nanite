package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// singleToolStepWorkflow is a minimal, valid workflow definition — one
// engine-owned tool step, no LLM involved — enough to exercise the full
// launch lifecycle without needing a fake provider stream.
func singleToolStepWorkflow(name string) agentworkflow.WorkflowDefinition {
	return agentworkflow.WorkflowDefinition{
		Name: name,
		Steps: []agentworkflow.StepDefinition{
			{
				ID:   "only",
				Kind: agentworkflow.StepKindTool,
				Config: map[string]any{
					"tool": "noop",
				},
			},
		},
	}
}

func newWorkflowLaunchTestFixture(t *testing.T) (*store.Store, *store.AgentProfile) {
	t.Helper()
	st, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile := &store.AgentProfile{Name: "Workflow Runner", Slug: "workflow-runner", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "ws-launch", Name: "Launch Workspace"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	return st, profile
}

func TestWorkflowLauncher_Launch_Success(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)

	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})
	durable := NewDurableAgentService(st)
	launcher := NewWorkflowLauncher(registry, engine, exec, durable)

	result, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "noop-workflow",
		WorkspaceID:    "ws-launch",
		AgentProfileID: profile.ID,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	if result.RunID == "" {
		t.Fatal("RunID is empty")
	}
	sr, ok := result.StepResults["only"]
	if !ok || sr.IsError {
		t.Fatalf("StepResults[only] = %+v, ok=%v", sr, ok)
	}

	// The instance was created as a template-class durable agent, sourced
	// from the workflow name, and finalized to stopped after the run.
	inst, err := st.GetDurableAgentInstance(result.InstanceID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstance: %v", err)
	}
	if inst.LifecycleClass != store.DurableAgentClassTemplate {
		t.Errorf("LifecycleClass = %q, want template", inst.LifecycleClass)
	}
	if inst.LaunchSourceType != store.DurableAgentLaunchTaskTemplateRun {
		t.Errorf("LaunchSourceType = %q, want task_template_run", inst.LaunchSourceType)
	}
	if inst.LaunchSourceID != "noop-workflow" {
		t.Errorf("LaunchSourceID = %q, want noop-workflow", inst.LaunchSourceID)
	}
	if inst.Status != store.DurableAgentStatusStopped {
		t.Errorf("Status = %q, want stopped", inst.Status)
	}

	var meta struct {
		WorkflowName  string `json:"workflow_name"`
		WorkflowRunID string `json:"workflow_run_id"`
	}
	if err := json.Unmarshal([]byte(inst.MetadataJSON), &meta); err != nil {
		t.Fatalf("unmarshal metadata_json %q: %v", inst.MetadataJSON, err)
	}
	if meta.WorkflowName != "noop-workflow" || meta.WorkflowRunID != result.RunID {
		t.Errorf("metadata = %+v, want workflow_name=noop-workflow workflow_run_id=%s", meta, result.RunID)
	}
}

func TestWorkflowLauncher_Launch_UnknownWorkflow(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(nil)
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})
	launcher := NewWorkflowLauncher(registry, engine, exec, NewDurableAgentService(st))

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "does-not-exist",
		WorkspaceID:    "ws-launch",
		AgentProfileID: profile.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown workflow") {
		t.Fatalf("err = %v, want unknown workflow", err)
	}
}

func TestWorkflowLauncher_Launch_RequiresWorkspaceID(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})
	launcher := NewWorkflowLauncher(registry, engine, exec, NewDurableAgentService(st))

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "noop-workflow",
		AgentProfileID: profile.ID,
	})
	if !errors.Is(err, ErrDurableAgentWorkspaceRequired) {
		t.Fatalf("err = %v, want ErrDurableAgentWorkspaceRequired", err)
	}
}

func TestWorkflowLauncher_Launch_RequiresAgentProfileID(t *testing.T) {
	st, _ := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{})
	launcher := NewWorkflowLauncher(registry, engine, exec, NewDurableAgentService(st))

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName: "noop-workflow",
		WorkspaceID:  "ws-launch",
	})
	if err == nil || !strings.Contains(err.Error(), "agent_profile_id") {
		t.Fatalf("err = %v, want agent_profile_id required", err)
	}
}
