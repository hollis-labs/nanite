package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeFailingWorkflowEngine simulates an infra-level engine failure (e.g. a
// persistence error before any run row exists) — BuiltinWorkflowEngine
// returns a zero-value WorkflowResult in that case, which is what exercises
// the metadata omitempty path.
type fakeFailingWorkflowEngine struct{}

func (fakeFailingWorkflowEngine) Name() string { return "fake-failing" }

func (fakeFailingWorkflowEngine) Run(context.Context, agentworkflow.WorkflowDefinition, agentworkflow.WorkflowInput, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	return agentworkflow.WorkflowResult{}, fmt.Errorf("simulated infra failure")
}

var _ agentworkflow.WorkflowEngine = fakeFailingWorkflowEngine{}

// builtinEngineSet wraps a single engine as the "builtin"-keyed engine set
// most tests need — a workflow with an empty Engine field resolves to
// agentworkflow.EngineBuiltin (WorkflowLauncher.Launch's default), so
// registering the fixture engine under that key exercises the same
// resolution path production wiring uses.
func builtinEngineSet(engine agentworkflow.WorkflowEngine) map[string]agentworkflow.WorkflowEngine {
	return map[string]agentworkflow.WorkflowEngine{agentworkflow.EngineBuiltin: engine}
}

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
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	profile := &store.AgentProfile{Name: "Workflow Runner", Slug: "workflow-runner", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return st, profile
}

func TestWorkflowLauncher_Launch_Success(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)

	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	durable := NewDurableAgentService(st)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(engine), exec, durable)

	result, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "noop-workflow",
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
	inst, err := st.GetDurableAgentInstance(context.Background(), result.InstanceID)
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
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(engine), exec, NewDurableAgentService(st))

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "does-not-exist",
		AgentProfileID: profile.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown workflow") {
		t.Fatalf("err = %v, want unknown workflow", err)
	}
}

// TestWorkflowLauncher_Launch_RequiresWorkspaceID was removed by Phase 0
// item 20 (retire workspaces,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
// ErrDurableAgentWorkspaceRequired and the WorkspaceID field it gated are
// both gone — a workflow launch no longer requires a workspace to create a
// session.

func TestWorkflowLauncher_Launch_RequiresAgentProfileID(t *testing.T) {
	st, _ := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(engine), exec, NewDurableAgentService(st))

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName: "noop-workflow",
	})
	if err == nil || !strings.Contains(err.Error(), "agent_profile_id") {
		t.Fatalf("err = %v, want agent_profile_id required", err)
	}
}

// TestWorkflowLauncher_Launch_RespectsTimeout proves TimeoutSeconds is
// actually enforced against the engine run, not silently dropped — a step
// that ignores its own deadline and only exits when ctx is cancelled must
// still cause Launch to return promptly instead of blocking on the step
// forever, and the durable-agent instance must still be finalized to
// stopped rather than left dangling in "active".
func TestWorkflowLauncher_Launch_RespectsTimeout(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)

	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"slow-workflow": singleToolStepWorkflow("slow-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	tools := &fakeWorkflowToolService{
		executeFunc: func(ctx context.Context, _, _ string, _ map[string]any) (*ToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	exec := NewWorkflowStepExecutor(tools, &fakeProviderResolver{}, nil)
	durable := NewDurableAgentService(st)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(engine), exec, durable)

	start := time.Now()
	result, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "slow-workflow",
		AgentProfileID: profile.ID,
		TimeoutSeconds: 1,
	})
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("Launch took %s, want it bounded by the 1s TimeoutSeconds", elapsed)
	}
	// A timed-out step is a normal (non-infra) run outcome — the engine
	// reports it via WorkflowResult.Status, not a Go error.
	if err != nil {
		t.Fatalf("Launch: unexpected error %v", err)
	}
	if result == nil {
		t.Fatal("result is nil, want a WorkflowLaunchResult with Status=failed")
	}
	if result.Status != agentworkflow.RunStatusFailed {
		t.Errorf("Status = %q, want failed", result.Status)
	}

	// Even though the run failed, the instance must still be finalized —
	// not left dangling in "active" because the step timed out.
	insts, err := st.ListDurableAgentInstances(context.Background(), false)
	if err != nil {
		t.Fatalf("ListDurableAgentInstances: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("len(insts) = %d, want 1", len(insts))
	}
	if insts[0].Status != store.DurableAgentStatusStopped {
		t.Errorf("Status = %q, want stopped", insts[0].Status)
	}
}

func TestBuiltinWorkflowEngine_PersistWorkflowRunStepOutcome_SurvivesCancelledContext(t *testing.T) {
	st, _ := newWorkflowLaunchTestFixture(t)
	runID := "cancelled-outcome-run"
	if err := st.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{
		ID: runID, DefinitionName: "cancelled-outcome", Status: "running", InputJSON: "{}",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	exec := &fakeStepExecutor{
		toolFunc: func(agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
			cancel()
			return agentworkflow.ToolStepResult{}, context.Canceled
		},
	}
	engine := NewBuiltinWorkflowEngine(st)
	outcome := engine.runStep(ctx, runID, singleToolStepWorkflow("cancelled-outcome").Steps[0], nil, agentworkflow.WorkflowInput{}, exec)
	if outcome.Err != nil {
		t.Fatalf("runStep: %v", outcome.Err)
	}
	if !outcome.Result.IsError {
		t.Fatalf("runStep result = %+v, want cancellation error outcome", outcome.Result)
	}

	rows, err := st.ListWorkflowRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Status != "failed" || !rows[0].IsError || !strings.Contains(rows[0].Output, context.Canceled.Error()) {
		t.Fatalf("persisted row = %+v, want failed cancellation outcome", rows[0])
	}
}

// TestWorkflowLauncher_Launch_StampsParentSessionIDInName proves
// ParentSessionID is actually used, not just documented — a workflow-run
// instance's Name should surface which session launched it so an operator
// looking at concurrent runs can tell them apart.
func TestWorkflowLauncher_Launch_StampsParentSessionIDInName(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	engine := NewBuiltinWorkflowEngine(st)
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(engine), exec, NewDurableAgentService(st))

	result, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:    "noop-workflow",
		AgentProfileID:  profile.ID,
		ParentSessionID: "sess-abc",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	inst, err := st.GetDurableAgentInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstance: %v", err)
	}
	if !strings.Contains(inst.Name, "sess-abc") {
		t.Errorf("Name = %q, want it to contain ParentSessionID sess-abc", inst.Name)
	}
}

// TestWorkflowLauncher_Launch_EngineInfraError_OmitsEmptyRunID proves that
// when the engine returns an infra-level error (a zero-value
// WorkflowResult, no run was ever produced), the instance's persisted
// metadata omits workflow_run_id entirely instead of stamping a misleading
// empty string that would look like a valid-but-empty run link.
func TestWorkflowLauncher_Launch_EngineInfraError_OmitsEmptyRunID(t *testing.T) {
	st, profile := newWorkflowLaunchTestFixture(t)
	registry := agentworkflow.NewRegistry(map[string]agentworkflow.WorkflowDefinition{
		"noop-workflow": singleToolStepWorkflow("noop-workflow"),
	})
	exec := NewWorkflowStepExecutor(&fakeWorkflowToolService{}, &fakeProviderResolver{}, nil)
	durable := NewDurableAgentService(st)
	launcher := NewWorkflowLauncher(registry, builtinEngineSet(fakeFailingWorkflowEngine{}), exec, durable)

	_, err := launcher.Launch(context.Background(), WorkflowLaunchRequest{
		WorkflowName:   "noop-workflow",
		AgentProfileID: profile.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "simulated infra failure") {
		t.Fatalf("err = %v, want simulated infra failure", err)
	}

	insts, err := st.ListDurableAgentInstances(context.Background(), false)
	if err != nil {
		t.Fatalf("ListDurableAgentInstances: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("len(insts) = %d, want 1", len(insts))
	}
	if insts[0].Status != store.DurableAgentStatusStopped {
		t.Errorf("Status = %q, want stopped", insts[0].Status)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(insts[0].MetadataJSON), &meta); err != nil {
		t.Fatalf("unmarshal metadata_json %q: %v", insts[0].MetadataJSON, err)
	}
	if _, present := meta["workflow_run_id"]; present {
		t.Errorf("metadata = %v, want workflow_run_id omitted on infra failure", meta)
	}
	if meta["workflow_name"] != "noop-workflow" {
		t.Errorf("metadata workflow_name = %v, want noop-workflow", meta["workflow_name"])
	}
}
