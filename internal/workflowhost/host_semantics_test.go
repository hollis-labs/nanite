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
	"github.com/hollis-labs/go-workflow/values"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

type persistentLoopHost struct {
	store *nanitestore.Store
	mu    sync.Mutex
	calls int
	id    string
}

func (h *persistentLoopHost) LaunchWorkflowLoop(ctx context.Context, request LoopStepLaunchRequest) (LoopStepResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if existing, err := h.store.GetLoopRun(ctx, h.id); err == nil {
		return LoopStepResult{LoopRunID: existing.ID, Status: existing.Status, CurrentIteration: existing.CurrentIteration}, nil
	}
	run := &nanitestore.LoopRun{ID: h.id, GoalID: request.GoalID, DefinitionName: request.WorkflowName, Status: nanitestore.LoopRunStatusWaitingOnEscalation}
	if err := h.store.CreateLoopRun(ctx, run); err != nil {
		return LoopStepResult{}, err
	}
	return LoopStepResult{LoopRunID: run.ID, Status: run.Status}, nil
}

func (h *persistentLoopHost) ObserveWorkflowLoop(ctx context.Context, id string) (LoopStepResult, error) {
	run, err := h.store.GetLoopRun(ctx, id)
	if err != nil {
		return LoopStepResult{}, err
	}
	return LoopStepResult{LoopRunID: run.ID, Status: run.Status, CurrentIteration: run.CurrentIteration}, nil
}

type recordingTeamHost struct {
	mu       sync.Mutex
	calls    int
	resolved bool
	output   string
}

func (h *recordingTeamHost) ResolveWorkflowTeamStep(_ context.Context, _ TeamStepResolveRequest) (TeamStepResolveResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	return TeamStepResolveResult{Resolved: h.resolved, Output: h.output, ResponderReference: "member-1"}, nil
}

type recordingExternalHost struct {
	mu     sync.Mutex
	calls  []ExternalStepRequest
	result ExternalStepResult
	err    error
}

func (h *recordingExternalHost) ExecuteWorkflowExternalStep(_ context.Context, request ExternalStepRequest) (ExternalStepResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, request)
	return h.result, h.err
}

func TestGoWorkflowIdentityAndPilotAliasRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	product, state := openWorkflowStateTest(t, path)
	exec := &recordingStepExecutor{}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	definition := agentworkflow.WorkflowDefinition{
		Name: "pilot alias", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{{ID: "approval", Kind: agentworkflow.StepKindGate, Config: map[string]any{"authority_ref": "operator"}}},
	}
	pilotRegistry, err := newPilotRegistry(exec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileWorkflowDefinition(t.Context(), definition, pilotRegistry)
	if err != nil {
		t.Fatal(err)
	}
	compiled.material.HostContract, compiled.material.HostContractDigest, err = pilotHostContract()
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := engine.launch(t.Context(), compiled.material, compiled.projections, map[string]any{"params": map[string]any{}, "session": ""}, pilotRegistry)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("pilot launch = %+v, %v", waiting, err)
	}
	graphDigest, err := workflowcompile.GraphDigest(compiled.material.Plan.Graph)
	if err != nil {
		t.Fatal(err)
	}
	pilotRevision := nanitestore.WorkflowDefinitionRevision{
		RevisionID:     "pilot-" + strings.TrimPrefix(compiled.material.Plan.Digest, "sha256:"),
		DefinitionName: compiled.material.ProductDefinitionName, SourceLocator: compiled.material.SourceLocator,
		SourceFormat: string(compiled.material.SourceFormat), SchemaVersion: compiled.material.Plan.SchemaVersion,
		SourceDigest: compiled.material.SourceDigest, SourceContent: append([]byte(nil), compiled.material.SourceContent...),
		CompiledGraphDigest: graphDigest, CompiledPlanDigest: compiled.material.Plan.Digest,
		Engine: nanitestore.WorkflowEngineIdentityPilot, RegisteredBy: "pilot-recovery-fixture",
	}
	if _, createErr := product.CreateWorkflowDefinitionRevision(t.Context(), pilotRevision); createErr != nil {
		t.Fatalf("create exact pilot revision: %v", createErr)
	}
	if _, updateErr := product.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET engine_kind=?,engine_contract_version=?,definition_revision_id=? WHERE id=?`, EngineKindPilotHadron, PilotContractVersion, pilotRevision.RevisionID, waiting.RunID); updateErr != nil {
		t.Fatal(updateErr)
	}
	identity, err := state.LoadRunEngineIdentity(t.Context(), workflowruntime.RunID(waiting.RunID))
	if err != nil || identity.Kind != EngineKindPilotHadron || identity.ContractVersion != PilotContractVersion {
		t.Fatalf("pilot identity = %+v, %v", identity, err)
	}
	boundRevision, err := product.GetWorkflowRunDefinitionRevision(t.Context(), waiting.RunID)
	if err != nil || boundRevision.RevisionID != pilotRevision.RevisionID || boundRevision.Engine != nanitestore.WorkflowEngineIdentityPilot {
		t.Fatalf("pilot run revision = %+v, %v", boundRevision, err)
	}
	wait, err := state.loadProductWait(t.Context(), waiting.RunID, "approval", false)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := restarted.ResumeWait(t.Context(), ResumeWaitRequest{
		WaitID: string(wait.Ref.ID), Payload: "approved", ResponderKind: "operator", ResponderReference: "operator",
		IdempotencyKey: "pilot-approval", ReceivedAt: wait.CreatedAt.Add(time.Second),
	}, exec)
	if err != nil || completed.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("pilot recovery = %+v, %v", completed, err)
	}

	current, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{Name: "current identity", Steps: []agentworkflow.StepDefinition{{ID: "tool", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}}}}, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	identity, err = state.LoadRunEngineIdentity(t.Context(), workflowruntime.RunID(current.RunID))
	if err != nil || identity.Kind != EngineKindGoWorkflow || identity.ContractVersion != EngineContractVersion {
		t.Fatalf("current identity = %+v, %v", identity, err)
	}
}

func TestDefinitionPublicationIsIdempotentAndEveryRunBindsExactRevision(t *testing.T) {
	product, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "definition-publication.db"))
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	exec := &recordingStepExecutor{}
	definition := agentworkflow.WorkflowDefinition{
		Name:  "immutable publication",
		Steps: []agentworkflow.StepDefinition{{ID: "tool", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}}},
	}
	first, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	firstRevision, err := product.GetWorkflowRunDefinitionRevision(t.Context(), first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	secondRevision, err := product.GetWorkflowRunDefinitionRevision(t.Context(), second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	head, err := product.GetWorkflowDefinitionHead(t.Context(), definition.Name)
	if err != nil || head.Generation != 1 || head.RevisionID != firstRevision.RevisionID || secondRevision.RevisionID != firstRevision.RevisionID {
		t.Fatalf("idempotent publication head=%+v first=%+v second=%+v err=%v", head, firstRevision, secondRevision, err)
	}
}

func TestLoopWaitPersistsCorrelationAndRecoversAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop-recovery.db")
	product, state := openWorkflowStateTest(t, path)
	goal := &nanitestore.Goal{ID: "goal-workflow-loop", Intent: "finish host loop"}
	if err := product.CreateGoal(t.Context(), goal); err != nil {
		t.Fatal(err)
	}
	host := &persistentLoopHost{store: product, id: "loop-workflow-host"}
	engine, _ := NewEngine(state)
	engine.WithLoopStepHost(host).WithWaitCoordinator(&workflowruntime.WaitCoordinator{Store: state, Authorizer: NaniteResponderAuthorizer{}})
	exec := &recordingStepExecutor{}
	waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
		Name: "loop child",
		Steps: []agentworkflow.StepDefinition{{
			ID: "loop", Kind: agentworkflow.StepKindLoop,
			Config: map[string]any{"workflow_name": "inner", "agent_profile_id": "agent", "goal_id": goal.ID},
		}},
	}, agentworkflow.WorkflowInput{}, exec)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaitingOnLoop {
		t.Fatalf("loop launch = %+v, %v", waiting, err)
	}
	var projected string
	if queryErr := product.DB.QueryRowContext(t.Context(), `SELECT loop_run_id FROM workflow_run_steps WHERE workflow_run_id=? AND step_id='loop'`, waiting.RunID).Scan(&projected); queryErr != nil || projected != host.id {
		t.Fatalf("projected loop correlation = %q, %v", projected, queryErr)
	}
	completedAt := time.Now().UTC()
	if updateErr := product.UpdateLoopRunStatus(t.Context(), host.id, nanitestore.LoopRunStatusCompleted, &completedAt); updateErr != nil {
		t.Fatal(updateErr)
	}
	restarted, _ := NewEngine(state)
	restarted.WithLoopStepHost(host).WithWaitCoordinator(&workflowruntime.WaitCoordinator{Store: state, Authorizer: NaniteResponderAuthorizer{}})
	var wg sync.WaitGroup
	reports := make([]ActiveRecoveryReport, 2)
	errorsSeen := make([]error, 2)
	for index := range reports {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			reports[index], errorsSeen[index] = restarted.RecoverActive(context.Background(), exec, 10)
		}(index)
	}
	wg.Wait()
	resumed := 0
	for index := range reports {
		if errorsSeen[index] != nil {
			t.Fatalf("concurrent RecoverActive[%d] = %+v, %v", index, reports[index], errorsSeen[index])
		}
		resumed += reports[index].Resumed
	}
	if resumed == 0 {
		t.Fatalf("concurrent RecoverActive made no progress: %+v", reports)
	}
	run, err := state.LoadRun(t.Context(), workflowruntime.RunID(waiting.RunID))
	if err != nil || run.Status != workflowruntime.RunSucceeded {
		t.Fatalf("recovered run = %+v, %v", run, err)
	}
	replay, err := restarted.RecoverActive(t.Context(), exec, 10)
	if err != nil || replay.Resumed != 0 {
		t.Fatalf("idempotent recovery = %+v, %v", replay, err)
	}
	if host.calls != 1 {
		t.Fatalf("LoopRun launch calls = %d, want 1", host.calls)
	}
}

func TestOrdinaryResumeReconcilesTerminalLoopWait(t *testing.T) {
	product, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "loop-resume.db"))
	goal := &nanitestore.Goal{ID: "goal-workflow-loop-resume", Intent: "resume outer workflow"}
	if err := product.CreateGoal(t.Context(), goal); err != nil {
		t.Fatal(err)
	}
	host := &persistentLoopHost{store: product, id: "loop-workflow-resume"}
	engine, _ := NewEngine(state)
	engine.WithLoopStepHost(host).WithWaitCoordinator(&workflowruntime.WaitCoordinator{Store: state, Authorizer: NaniteResponderAuthorizer{}})
	exec := &recordingStepExecutor{}
	waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
		Name: "loop ordinary resume",
		Steps: []agentworkflow.StepDefinition{{
			ID: "loop", Kind: agentworkflow.StepKindLoop,
			Config: map[string]any{"workflow_name": "inner", "agent_profile_id": "agent", "goal_id": goal.ID},
		}},
	}, agentworkflow.WorkflowInput{}, exec)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaitingOnLoop {
		t.Fatalf("loop launch = %+v, %v", waiting, err)
	}
	completedAt := time.Now().UTC()
	if updateErr := product.UpdateLoopRunStatus(t.Context(), host.id, nanitestore.LoopRunStatusCompleted, &completedAt); updateErr != nil {
		t.Fatal(updateErr)
	}
	restarted, _ := NewEngine(state)
	restarted.WithLoopStepHost(host).WithWaitCoordinator(&workflowruntime.WaitCoordinator{Store: state, Authorizer: NaniteResponderAuthorizer{}})
	completed, err := restarted.Resume(t.Context(), waiting.RunID, exec)
	if err != nil || completed.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("ordinary Resume terminal loop = %+v, %v", completed, err)
	}
	replayed, err := restarted.Resume(t.Context(), waiting.RunID, exec)
	if err != nil || replayed.Status != agentworkflow.RunStatusCompleted || host.calls != 1 {
		t.Fatalf("ordinary Resume replay = %+v, %v; launch calls=%d", replayed, err, host.calls)
	}
}

func TestTeamSignalDelegatesResolutionAndExternalEngineIsStepKind(t *testing.T) {
	_, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "collaborators.db"))
	exec := &recordingStepExecutor{}
	team := &recordingTeamHost{resolved: true, output: "phase closed"}
	engine, _ := NewEngine(state)
	engine.WithTeamStepHost(team).WithWaitCoordinator(&workflowruntime.WaitCoordinator{Store: state, Authorizer: NaniteResponderAuthorizer{}})
	waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{Name: "team", Steps: []agentworkflow.StepDefinition{{ID: "work", Kind: agentworkflow.StepKindFlex, Config: map[string]any{"active_slots": []any{"engineer"}, "exit_trigger": map[string]any{"event": "done"}}}}}, agentworkflow.WorkflowInput{}, exec)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("team wait = %+v, %v", waiting, err)
	}
	completed, err := engine.ResolveTeamSignal(t.Context(), waiting.RunID, "work", exec)
	if err != nil || completed.Status != agentworkflow.RunStatusCompleted || team.calls != 1 {
		t.Fatalf("team resolve = %+v calls=%d, %v", completed, team.calls, err)
	}
	replayedTeam, err := engine.ResolveTeamSignal(t.Context(), waiting.RunID, "work", exec)
	if err != nil || replayedTeam.Status != agentworkflow.RunStatusCompleted || team.calls != 1 {
		t.Fatalf("team resolve replay = %+v calls=%d, %v", replayedTeam, team.calls, err)
	}

	external := &recordingExternalHost{result: ExternalStepResult{Output: "python complete"}}
	externalEngine, _ := NewEngine(state)
	externalEngine.WithExternalStepHost(external)
	result, err := externalEngine.Run(t.Context(), agentworkflow.WorkflowDefinition{
		Name: "python graph", Engine: agentworkflow.EngineLangGraph,
		Steps: []agentworkflow.StepDefinition{{ID: "documented", Kind: agentworkflow.StepKindLLM, Config: map[string]any{"provider": "unused", "prompt": "unused"}}},
	}, agentworkflow.WorkflowInput{Params: map[string]any{"topic": "x"}, SessionID: "session-1"}, exec)
	if err != nil || result.Status != agentworkflow.RunStatusCompleted || len(external.calls) != 1 {
		t.Fatalf("external result = %+v calls=%+v, %v", result, external.calls, err)
	}
	if external.calls[0].Engine != agentworkflow.EngineLangGraph || external.calls[0].WorkflowName != "python graph" || external.calls[0].SessionID != "session-1" || external.calls[0].IdempotencyKey == "" {
		t.Fatalf("external request = %+v", external.calls[0])
	}
}

func TestCallbackResumeAuthenticatesAndReplays(t *testing.T) {
	_, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "callback-auth.db"))
	engine, _ := NewEngine(state)
	exec := &recordingStepExecutor{}
	source := []byte(`workflow:
  name: callback auth
  version: 1.0.0
steps:
  - id: callback
    kind: nanite-external-callback
    kind_version: v1
    config:
      product_step_id: callback
      product_kind: gate
      authority_ref: callback-principal
    outputs:
      result:
        type: object
`)
	waiting, err := engine.RunSource(t.Context(), "callback-auth.workflow.yaml", source, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Second)
	request := CallbackResumeRequest{RunID: waiting.RunID, StepID: "callback", Payload: map[string]any{"ok": true}, AuthenticatedKind: "callback", AuthenticatedPrincipal: "wrong", IdempotencyKey: "callback-event-1", ReceivedAt: at}
	if _, resumeErr := engine.ResumeCallback(t.Context(), request, exec); !errors.Is(resumeErr, ErrWorkflowWaitUnauthorized) {
		t.Fatalf("unauthorized callback error = %v", resumeErr)
	}
	request.AuthenticatedPrincipal = "callback-principal"
	completed, err := engine.ResumeCallback(t.Context(), request, exec)
	if err != nil || completed.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("callback completion = %+v, %v", completed, err)
	}
	replayed, err := engine.ResumeCallback(t.Context(), request, exec)
	if err != nil || replayed.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("callback replay = %+v, %v", replayed, err)
	}
}

func TestPilotHostContractBytesRemainExact(t *testing.T) {
	contract, digest, err := pilotHostContract()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if values.SHA256Digest(encoded) != digest {
		t.Fatal("pilot host contract digest is not exact")
	}
}
