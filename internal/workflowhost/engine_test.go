package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	workflowcompile "github.com/hollis-labs/go-workflow/compile"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"
	"github.com/hollis-labs/go-workflow/verification"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

type recordingStepExecutor struct {
	mu        sync.Mutex
	llmCalls  []agentworkflow.LLMStepRequest
	toolCalls []agentworkflow.ToolStepRequest
}

type retryingStepExecutor struct {
	mu    sync.Mutex
	calls []agentworkflow.LLMStepRequest
}

func (f *retryingStepExecutor) ExecuteLLMStep(_ context.Context, request agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, request)
	if len(f.calls) == 1 {
		return agentworkflow.LLMStepResult{}, &stepkind.ExecutionError{
			Code: "temporary", Message: "provider asked Nanite to retry",
			Classification: stepkind.Retryable,
		}
	}
	return agentworkflow.LLMStepResult{Text: "retry-succeeded"}, nil
}

func (*retryingStepExecutor) ExecuteToolStep(context.Context, agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	return agentworkflow.ToolStepResult{}, errors.New("unexpected tool execution")
}

func (*retryingStepExecutor) Verify(context.Context, agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{Passed: true}, nil
}

func (f *recordingStepExecutor) ExecuteLLMStep(_ context.Context, request agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.llmCalls = append(f.llmCalls, request)
	return agentworkflow.LLMStepResult{Text: request.StepID + "-output"}, nil
}

func (f *recordingStepExecutor) ExecuteToolStep(_ context.Context, request agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolCalls = append(f.toolCalls, request)
	return agentworkflow.ToolStepResult{Output: "tool-output"}, nil
}

func (*recordingStepExecutor) Verify(_ context.Context, _ agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{Passed: true}, nil
}

func TestSharedCompilerOwnsGraphStructuralValidation(t *testing.T) {
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		definition agentworkflow.WorkflowDefinition
		contains   string
	}{
		{
			name: "cycle",
			definition: agentworkflow.WorkflowDefinition{Name: "cycle", Steps: []agentworkflow.StepDefinition{
				{ID: "a", Kind: agentworkflow.StepKindTool, DependsOn: []string{"b"}, Config: map[string]any{"tool": "noop"}},
				{ID: "b", Kind: agentworkflow.StepKindTool, DependsOn: []string{"a"}, Config: map[string]any{"tool": "noop"}},
			}},
		},
		{
			name: "unknown dependency",
			definition: agentworkflow.WorkflowDefinition{Name: "unknown dependency", Steps: []agentworkflow.StepDefinition{
				{ID: "a", Kind: agentworkflow.StepKindTool, DependsOn: []string{"missing"}, Config: map[string]any{"tool": "noop"}},
			}},
		},
		{
			name: "normalized id collision",
			definition: agentworkflow.WorkflowDefinition{Name: "normalized collision", Steps: []agentworkflow.StepDefinition{
				{ID: "A B", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}},
				{ID: "a-b", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}},
			}},
			contains: "normalization",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, compileErr := compileWorkflowDefinition(t.Context(), test.definition, registry)
			if compileErr == nil {
				t.Fatal("shared compiler accepted structurally invalid graph")
			}
			if test.contains != "" && !strings.Contains(compileErr.Error(), test.contains) {
				t.Fatalf("error = %v, want %q", compileErr, test.contains)
			}
		})
	}
}

func TestHadronEngineWorkerReviewerGateRestartResumeAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hadron-host.db")
	productStore, state := openWorkflowStateTest(t, path)
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingStepExecutor{}
	definition := agentworkflow.WorkflowDefinition{
		Name: "worker reviewer approval", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{
			{ID: "worker", Kind: agentworkflow.StepKindLLM, Config: map[string]any{
				"role": "worker", "agent_id": "worker-agent", "provider": "test", "prompt": "Build {{input.topic}}",
			}},
			{ID: "reviewer", Kind: agentworkflow.StepKindLLM, DependsOn: []string{"worker"}, Config: map[string]any{
				"role": "reviewer", "agent_id": "reviewer-agent", "provider": "test", "prompt": "Review {{steps.worker.output}}",
			}},
			{ID: "approval", Kind: agentworkflow.StepKindGate, DependsOn: []string{"reviewer"}, Config: map[string]any{
				"correlation": "release-approval", "authority_ref": "release-manager",
			}},
			{ID: "publish", Kind: agentworkflow.StepKindTool, DependsOn: []string{"approval"}, Config: map[string]any{
				"agent_id": "publisher-agent", "tool": "release_publish", "args": map[string]any{"decision": "{{steps.approval.output}}"},
			}},
		},
	}
	first, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{
		Params: map[string]any{"topic": "artifact"}, SessionID: "workflow-session",
	}, executor)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Status != agentworkflow.RunStatusWaiting || first.RunID == "" {
		t.Fatalf("first result = %+v", first)
	}
	if len(executor.llmCalls) != 2 || len(executor.toolCalls) != 0 {
		t.Fatalf("execution calls before gate = llm:%+v tool:%+v", executor.llmCalls, executor.toolCalls)
	}
	if worker := executor.llmCalls[0]; worker.StepID != "worker" || worker.AgentID != "worker-agent" || worker.SessionID != "workflow-session" || worker.WorkflowRunID != first.RunID || worker.Messages[0].Content != "Build artifact" {
		t.Fatalf("worker identity/request = %+v", worker)
	}
	if reviewer := executor.llmCalls[1]; reviewer.StepID != "reviewer" || reviewer.AgentID != "reviewer-agent" || reviewer.SessionID != "workflow-session" || reviewer.WorkflowRunID != first.RunID || reviewer.Messages[0].Content != "Review worker-output" {
		t.Fatalf("reviewer identity/request = %+v", reviewer)
	}
	var engineKind, contractVersion, productStatus, definitionName string
	if queryErr := productStore.DB.QueryRow(`SELECT engine_kind, engine_contract_version, status, definition_name FROM workflow_runs WHERE id=?`, first.RunID).Scan(&engineKind, &contractVersion, &productStatus, &definitionName); queryErr != nil {
		t.Fatal(queryErr)
	}
	if engineKind != EngineKindGoWorkflow || contractVersion != EngineContractVersion || productStatus != "waiting_on_gate" || definitionName != definition.Name {
		t.Fatalf("run projection = kind:%q contract:%q status:%q definition:%q", engineKind, contractVersion, productStatus, definitionName)
	}
	waits, err := state.RecoverOpenWaits(t.Context(), workflowruntime.OpenWaitQuery{RunID: workflowruntime.RunID(first.RunID)})
	if err != nil || len(waits) != 1 || waits[0].Correlation != "release-approval" {
		t.Fatalf("open waits = %+v, %v", waits, err)
	}
	material, err := state.LoadPlanMaterial(t.Context(), string(waits[0].Invocation.RunID))
	_ = material
	if err == nil {
		t.Fatal("LoadPlanMaterial unexpectedly accepted a run id in place of a plan digest")
	}
	run, err := state.LoadRun(t.Context(), workflowruntime.RunID(first.RunID))
	if err != nil {
		t.Fatal(err)
	}
	material, err = state.LoadPlanMaterial(t.Context(), run.Plan.Digest)
	if err != nil || material.SourceFormat != "agent" || material.ProductDefinitionName != definition.Name || material.StepKindCatalogDigest == "" || material.VerifierCatalogDigest == "" || material.HostContractDigest == "" || len(material.SourceContent) == 0 {
		t.Fatalf("exact plan material = %+v, %v", material, err)
	}
	head, err := productStore.GetWorkflowDefinitionHead(t.Context(), definition.Name)
	if err != nil || head.RevisionID != workflowDefinitionRevisionID(material) || head.Generation != 1 {
		t.Fatalf("immutable definition head = %+v, %v", head, err)
	}
	revision, err := productStore.GetWorkflowDefinitionRevision(t.Context(), head.RevisionID)
	if err != nil {
		t.Fatalf("immutable definition revision: %v", err)
	}
	graphDigest, err := workflowcompile.GraphDigest(material.Plan.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if revision.DefinitionName != definition.Name || revision.SourceLocator != material.SourceLocator ||
		revision.SourceFormat != string(material.SourceFormat) || revision.SourceDigest != material.SourceDigest ||
		string(revision.SourceContent) != string(material.SourceContent) || revision.CompiledGraphDigest != graphDigest ||
		revision.CompiledPlanDigest != material.Plan.Digest || revision.Engine != nanitestore.WorkflowEngineIdentityShared ||
		revision.RegisteredBy != workflowDefinitionPublisher {
		t.Fatalf("immutable definition revision = %+v", revision)
	}
	runRevision, err := productStore.GetWorkflowRunDefinitionRevision(t.Context(), first.RunID)
	if err != nil || runRevision.RevisionID != revision.RevisionID || runRevision.CompiledPlanDigest != run.Plan.Digest {
		t.Fatalf("run immutable definition association = %+v, %v", runRevision, err)
	}
	productRun, err := productStore.GetWorkflowRun(t.Context(), first.RunID)
	if err != nil || productRun.DefinitionRevisionID == nil || *productRun.DefinitionRevisionID != revision.RevisionID {
		t.Fatalf("product run revision association = %+v, %v", productRun, err)
	}

	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	restartedState, err := NewWorkflowStateStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEngine(restartedState)
	if err != nil {
		t.Fatal(err)
	}
	resumeRequest := ResumeWaitRequest{
		WaitID: string(waits[0].Ref.ID), Payload: "approved",
		ResponderKind: "agent", ResponderReference: "release-manager",
		IdempotencyKey: "approval-response-1", ReceivedAt: time.Now().UTC(),
	}
	completed, err := restarted.ResumeWait(t.Context(), resumeRequest, executor)
	if err != nil {
		t.Fatalf("ResumeWait after restart: %v", err)
	}
	if completed.Status != agentworkflow.RunStatusCompleted || completed.StepResults["publish"].Output != "tool-output" {
		t.Fatalf("completed result = %+v", completed)
	}
	if len(executor.toolCalls) != 1 {
		t.Fatalf("tool calls = %+v", executor.toolCalls)
	}
	tool := executor.toolCalls[0]
	if tool.WorkflowRunID != first.RunID || tool.StepID != "publish" || tool.AgentID != "publisher-agent" || tool.SessionID != "workflow-session" || tool.Args["decision"] != "approved" {
		t.Fatalf("publish identity/request = %+v", tool)
	}
	var projectedOutput string
	if queryErr := reopened.DB.QueryRow(`SELECT output FROM workflow_run_steps WHERE workflow_run_id=? AND step_id='publish'`, first.RunID).Scan(&projectedOutput); queryErr != nil || projectedOutput != "tool-output" {
		t.Fatalf("atomic product output projection = %q, %v", projectedOutput, queryErr)
	}
	replayed, replayErr := restarted.ResumeWait(t.Context(), resumeRequest, executor)
	if replayErr != nil || replayed.Status != agentworkflow.RunStatusCompleted || len(executor.toolCalls) != 1 {
		t.Fatalf("idempotent resume = %+v, %v; tool calls=%+v", replayed, replayErr, executor.toolCalls)
	}
}

func TestHadronEngineCancelWaitingRunSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hadron-cancel.db")
	productStore, state := openWorkflowStateTest(t, path)
	engine, _ := NewEngine(state)
	executor := &recordingStepExecutor{}
	waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
		Name: "cancel approval", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{{ID: "approval", Kind: agentworkflow.StepKindGate}},
	}, agentworkflow.WorkflowInput{}, executor)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Run = %+v, %v", waiting, err)
	}
	canceled, err := engine.Cancel(t.Context(), waiting.RunID, "operator stopped release")
	if err != nil || canceled.Status != agentworkflow.RunStatusCanceled {
		t.Fatalf("Cancel = %+v, %v", canceled, err)
	}
	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	restartedState, _ := NewWorkflowStateStore(reopened)
	restarted, _ := NewEngine(restartedState)
	afterRestart, err := restarted.Resume(t.Context(), waiting.RunID, executor)
	if err != nil || afterRestart.Status != agentworkflow.RunStatusCanceled {
		t.Fatalf("Resume canceled after restart = %+v, %v", afterRestart, err)
	}
}

func TestHadronEngineGraphNativeSourceUsesPublicCompilerPipeline(t *testing.T) {
	productStore, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "source.db"))
	engine, _ := NewEngine(state)
	executor := &recordingStepExecutor{}
	source := `workflow:
  name: nanite source pilot
  version: 1.0.0
steps:
  - id: direct-tool
    kind: nanite-tool
    kind_version: v1
    config:
      product_step_id: direct-tool
      product_kind: tool
      agent_id: source-agent
      tool: source_echo
      args:
        message: hello
    outputs:
      result:
        type: object
`
	result, err := engine.RunSource(t.Context(), "nanite-source.workflow.yaml", []byte(source), agentworkflow.WorkflowInput{}, executor)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted || len(executor.toolCalls) != 1 || executor.toolCalls[0].Tool != "source_echo" {
		t.Fatalf("source result=%+v calls=%+v", result, executor.toolCalls)
	}
	if !strings.HasPrefix(result.RunID, "01") {
		t.Fatalf("run id %q is not host-issued ULID", result.RunID)
	}
	var definitionName string
	if queryErr := productStore.DB.QueryRow(`SELECT definition_name FROM workflow_runs WHERE id=?`, result.RunID).Scan(&definitionName); queryErr != nil || definitionName != "nanite source pilot" {
		t.Fatalf("graph-native product definition=%q err=%v", definitionName, queryErr)
	}
	revision, err := productStore.GetWorkflowRunDefinitionRevision(t.Context(), result.RunID)
	if err != nil || revision.DefinitionName != "nanite source pilot" || revision.SourceLocator != "nanite-source.workflow.yaml" ||
		string(revision.SourceContent) != source || revision.SourceDigest != values.SHA256Digest([]byte(source)) ||
		revision.Engine != nanitestore.WorkflowEngineIdentityShared {
		t.Fatalf("graph-native immutable run definition = %+v, %v", revision, err)
	}
}

func TestHadronEngineRetryActivationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hadron-retry.db")
	productStore, state := openWorkflowStateTest(t, path)
	waits := &workflowruntime.WaitCoordinator{
		Store: state, Scheduler: NewActivationScheduler(productStore),
	}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithWaitCoordinator(waits)
	executor := &retryingStepExecutor{}
	source := `workflow:
  name: Nanite Retry Pilot
  version: 1.0.0
inputs:
  - name: retry-key
    type: string
    required: true
steps:
  - id: flaky-provider
    kind: nanite-llm
    kind_version: v1
    config:
      product_step_id: flaky-provider
      product_kind: llm
      provider: test
      prompt: retry me
    outputs:
      result:
        type: object
    retry:
      attempts: 2
      backoff:
        strategy: fixed
        initial_delay: 1h
      idempotency_key: inputs["retry-key"]
`
	waiting, err := engine.RunSource(t.Context(), "retry.workflow.yaml", []byte(source), agentworkflow.WorkflowInput{
		Params: map[string]any{"retry-key": "stable-provider-request"},
	}, executor)
	if err != nil {
		t.Fatalf("RunSource(retry): %v", err)
	}
	if waiting.Status != agentworkflow.RunStatusRunning || len(executor.calls) != 1 {
		t.Fatalf("scheduled retry result=%+v calls=%d", waiting, len(executor.calls))
	}
	var productStatus string
	if queryErr := productStore.DB.QueryRow(`SELECT status FROM workflow_runs WHERE id=?`, waiting.RunID).Scan(&productStatus); queryErr != nil || productStatus != "running" {
		t.Fatalf("scheduled retry product status=%q err=%v", productStatus, queryErr)
	}
	due, err := productStore.ListDueWorkflowActivationSchedules(t.Context(), time.Now().UTC().Add(2*time.Hour), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("due retry activations=%+v err=%v", due, err)
	}
	var activation workflowwait.Activation
	if decodeErr := json.Unmarshal([]byte(due[0].ActivationJSON), &activation); decodeErr != nil {
		t.Fatalf("decode retry activation: %v", decodeErr)
	}
	if activation.Kind != "node_retry" || activation.RunID != waiting.RunID {
		t.Fatalf("retry activation=%+v", activation)
	}
	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}

	reopened, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	restartedState, err := NewWorkflowStateStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restartedWaits := &workflowruntime.WaitCoordinator{
		Store: restartedState, Scheduler: NewActivationScheduler(reopened),
	}
	restarted, err := NewEngine(restartedState)
	if err != nil {
		t.Fatal(err)
	}
	restarted.WithWaitCoordinator(restartedWaits)
	dispatcher := &ActivationDispatcher{
		State: restartedState, Waits: restartedWaits, Engine: restarted, Executor: executor,
	}
	applied, err := dispatcher.DispatchWorkflowActivation(t.Context(), activation, activation.FireAt.Add(time.Second))
	if err != nil || !applied {
		t.Fatalf("DispatchWorkflowActivation(retry) applied=%t err=%v", applied, err)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("LLM calls after durable retry=%d, want 2", len(executor.calls))
	}
	var status string
	if queryErr := reopened.DB.QueryRow(`SELECT status FROM workflow_runs WHERE id=?`, waiting.RunID).Scan(&status); queryErr != nil || status != "completed" {
		t.Fatalf("completed retry product status=%q err=%v", status, queryErr)
	}
	var attempts int
	if queryErr := reopened.DB.QueryRow(`SELECT COUNT(*) FROM workflow_attempts WHERE run_id=? AND node_id='flaky-provider'`, waiting.RunID).Scan(&attempts); queryErr != nil || attempts != 2 {
		t.Fatalf("durable retry attempts=%d err=%v", attempts, queryErr)
	}
	retrySnapshot, err := restartedState.LoadRetryActivation(t.Context(), string(activation.ID))
	if err != nil || retrySnapshot.Status != workflowruntime.RetryActivated {
		t.Fatalf("retry snapshot=%+v err=%v", retrySnapshot, err)
	}
}

func TestCompileSourceRejectsUnpinnedStepKindVersion(t *testing.T) {
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	source := `workflow:
  name: Unpinned Nanite Step
  version: 1.0.0
steps:
  - id: unpinned
    kind: nanite-tool
    config:
      product_step_id: unpinned
      product_kind: tool
      tool: echo
    outputs:
      result:
        type: object
`
	_, err = CompileSource(t.Context(), "unpinned.workflow.yaml", []byte(source), registry)
	if err == nil || !strings.Contains(err.Error(), "does not pin an exact StepKind version") {
		t.Fatalf("CompileSource(unpinned) error=%v", err)
	}
}

func TestHadronEngineRecoveryRejectsExecutionIdentityDrift(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
		drift     func(PlanMaterial) []byte
		wantError string
	}{
		{
			name:      "host policy contract",
			statement: "UPDATE workflow_plan_materials SET host_contract_json=?, host_contract_digest=? WHERE plan_digest=?",
			drift: func(material PlanMaterial) []byte {
				changed := append([]HostComponentIdentity(nil), material.HostContract...)
				changed[0].Version = "v2"
				encoded, _ := json.Marshal(changed)
				return encoded
			},
			wantError: "installed workflow host contract differs",
		},
		{
			name:      "verifier catalog",
			statement: "UPDATE workflow_plan_materials SET verifier_catalog_json=?, verifier_catalog_digest=? WHERE plan_digest=?",
			drift: func(material PlanMaterial) []byte {
				changed := append([]verification.VerifierSpec(nil), material.VerifierCatalog...)
				changed[0].Version = "drifted-v2"
				encoded, _ := json.Marshal(changed)
				return encoded
			},
			wantError: "installed go-workflow verifier catalog differs",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hadron-identity-drift.db")
			productStore, state := openWorkflowStateTest(t, path)
			engine, err := NewEngine(state)
			if err != nil {
				t.Fatal(err)
			}
			executor := &recordingStepExecutor{}
			waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
				Name: "identity drift gate", Engine: agentworkflow.EngineHadron,
				Steps: []agentworkflow.StepDefinition{{ID: "approval", Kind: agentworkflow.StepKindGate}},
			}, agentworkflow.WorkflowInput{}, executor)
			if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
				t.Fatalf("Run=%+v err=%v", waiting, err)
			}
			var digest string
			if queryErr := productStore.DB.QueryRow(`SELECT plan_digest FROM workflow_runs WHERE id=?`, waiting.RunID).Scan(&digest); queryErr != nil {
				t.Fatal(queryErr)
			}
			material, err := state.LoadPlanMaterial(t.Context(), digest)
			if err != nil {
				t.Fatal(err)
			}
			encoded := test.drift(material)
			if _, dropErr := productStore.DB.Exec(`DROP TRIGGER workflow_plan_materials_no_update`); dropErr != nil {
				t.Fatal(dropErr)
			}
			if _, updateErr := productStore.DB.Exec(test.statement, encoded, values.SHA256Digest(encoded), digest); updateErr != nil {
				t.Fatal(updateErr)
			}
			if closeErr := productStore.Close(t.Context()); closeErr != nil {
				t.Fatal(closeErr)
			}
			reopened, err := nanitestore.New(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close(context.Background()) })
			restartedState, err := NewWorkflowStateStore(reopened)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := NewEngine(restartedState)
			if err != nil {
				t.Fatal(err)
			}
			_, err = restarted.Resume(t.Context(), waiting.RunID, executor)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Resume(identity drift) error=%v", err)
			}
		})
	}
}

func TestHadronEngineMinimalTeamLoopAndCallbackWaitPrimitives(t *testing.T) {
	for _, test := range []struct {
		name string
		kind agentworkflow.StepKind
	}{
		{name: "team", kind: agentworkflow.StepKindFlex},
		{name: "loop", kind: agentworkflow.StepKindLoop},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), test.name+".db"))
			engine, _ := NewEngine(state)
			executor := &recordingStepExecutor{}
			failed, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
				Name: test.name + " primitive", Engine: agentworkflow.EngineHadron,
				Steps: []agentworkflow.StepDefinition{{ID: test.name, Kind: test.kind}},
			}, agentworkflow.WorkflowInput{}, executor)
			if err != nil || failed.Status != agentworkflow.RunStatusFailed || !strings.Contains(failed.Error, "host is not configured") {
				t.Fatalf("Run without required host = %+v, %v", failed, err)
			}
		})
	}

	t.Run("external callback", func(t *testing.T) {
		_, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "callback.db"))
		engine, _ := NewEngine(state)
		executor := &recordingStepExecutor{}
		source := `workflow:
  name: nanite callback pilot
  version: 1.0.0
steps:
  - id: callback
    kind: nanite-external-callback
    kind_version: v1
    config:
      product_step_id: callback
      product_kind: gate
      correlation: callback-42
    outputs:
      result:
        type: object
`
		waiting, err := engine.RunSource(t.Context(), "callback.workflow.yaml", []byte(source), agentworkflow.WorkflowInput{}, executor)
		if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
			t.Fatalf("RunSource = %+v, %v", waiting, err)
		}
		waits, err := state.RecoverOpenWaits(t.Context(), workflowruntime.OpenWaitQuery{RunID: workflowruntime.RunID(waiting.RunID)})
		if err != nil || len(waits) != 1 || waits[0].Kind != "callback" || waits[0].WakeSource != "callback" {
			t.Fatalf("callback wait = %+v, %v", waits, err)
		}
		completed, err := engine.ResumeWait(t.Context(), ResumeWaitRequest{
			WaitID: string(waits[0].Ref.ID), Payload: map[string]any{"event": "complete"},
			ResponderKind: "callback", ResponderReference: "remote-job-42",
			IdempotencyKey: "callback-42-complete",
		}, executor)
		if err != nil || completed.Status != agentworkflow.RunStatusCompleted {
			t.Fatalf("ResumeWait callback = %+v, %v", completed, err)
		}
	})
}

func TestNaniteHadronStepKindCatalogIsExactAndDeterministic(t *testing.T) {
	executor := &recordingStepExecutor{}
	first, err := newFrozenRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newFrozenRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	firstSpecs, firstDigest, err := first.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	secondSpecs, secondDigest, err := second.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || len(firstSpecs) != 10 || len(secondSpecs) != 10 {
		t.Fatalf("catalog snapshots differ: first=%d/%s second=%d/%s", len(firstSpecs), firstDigest, len(secondSpecs), secondDigest)
	}
	want := []string{
		StepKindApproval, StepKindExternalCallback, StepKindExternalEngine, StepKindLLM, StepKindLoop,
		StepKindReviewer, StepKindTeam, StepKindTool, StepKindTurn, StepKindWorker,
	}
	got := make([]string, len(firstSpecs))
	for index, spec := range firstSpecs {
		got[index] = spec.Name
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("catalog names = %v, want %v", got, want)
	}
}
