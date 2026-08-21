package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- fakes ---

// fakeStepExecutor is a scriptable agentworkflow.StepExecutor test double.
// Every call is recorded so tests can assert exactly which steps ran (and,
// for skip/gate propagation tests, that ones that shouldn't run didn't).
type fakeStepExecutor struct {
	mu          sync.Mutex
	llmCalls    []agentworkflow.LLMStepRequest
	toolCalls   []agentworkflow.ToolStepRequest
	verifyCalls []agentworkflow.VerifyRequest

	llmFunc    func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error)
	toolFunc   func(req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error)
	verifyFunc func(req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error)
}

func (f *fakeStepExecutor) ExecuteLLMStep(_ context.Context, req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	f.mu.Lock()
	f.llmCalls = append(f.llmCalls, req)
	f.mu.Unlock()
	if f.llmFunc != nil {
		return f.llmFunc(req)
	}
	return agentworkflow.LLMStepResult{Text: "ok:" + req.StepID}, nil
}

func (f *fakeStepExecutor) ExecuteToolStep(_ context.Context, req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	f.mu.Lock()
	f.toolCalls = append(f.toolCalls, req)
	f.mu.Unlock()
	if f.toolFunc != nil {
		return f.toolFunc(req)
	}
	return agentworkflow.ToolStepResult{Output: "ok:" + req.Tool}, nil
}

func (f *fakeStepExecutor) Verify(_ context.Context, req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	f.mu.Lock()
	f.verifyCalls = append(f.verifyCalls, req)
	f.mu.Unlock()
	if f.verifyFunc != nil {
		return f.verifyFunc(req)
	}
	return agentworkflow.VerifyResult{Passed: true}, nil
}

var _ agentworkflow.StepExecutor = (*fakeStepExecutor)(nil)

func (f *fakeStepExecutor) toolCallCount(tool string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.toolCalls {
		if c.Tool == tool {
			n++
		}
	}
	return n
}

func newTestWorkflowStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- tests ---

func TestBuiltinWorkflowEngine_RejectsInvalidDefinition(t *testing.T) {
	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	wf := agentworkflow.WorkflowDefinition{
		Name: "cyclic",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, DependsOn: []string{"b"}, Config: map[string]any{"tool": "noop"}},
			{ID: "b", Kind: agentworkflow.StepKindTool, DependsOn: []string{"a"}, Config: map[string]any{"tool": "noop"}},
		},
	}
	_, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, &fakeStepExecutor{})
	if err == nil {
		t.Fatal("expected error for cyclic definition, got nil")
	}
}

func TestBuiltinWorkflowEngine_LevelOrderAndTypedDataFlow(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "fan-in",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "fetch_a", "agent_id": "agent-1"}},
			{ID: "b", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "fetch_b", "agent_id": "agent-1"}},
			{
				ID: "combine", Kind: agentworkflow.StepKindLLM, DependsOn: []string{"a", "b"},
				Config: map[string]any{
					"provider": "anthropic",
					"prompt":   "A={{ steps.a.output }} B={{ steps.b.output }} in={{ input.topic }}",
					"agent_id": "agent-1",
				},
			},
		},
	}

	exec := &fakeStepExecutor{
		toolFunc: func(req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
			switch req.Tool {
			case "fetch_a":
				return agentworkflow.ToolStepResult{Output: "alpha"}, nil
			case "fetch_b":
				return agentworkflow.ToolStepResult{Output: "beta"}, nil
			}
			return agentworkflow.ToolStepResult{}, nil
		},
	}

	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	result, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{Params: map[string]any{"topic": "widgets"}}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status = %q, want completed (error=%s)", result.Status, result.Error)
	}
	if len(exec.llmCalls) != 1 {
		t.Fatalf("len(llmCalls) = %d, want 1", len(exec.llmCalls))
	}
	got := exec.llmCalls[0].Messages[0].Content
	want := "A=alpha B=beta in=widgets"
	if got != want {
		t.Fatalf("combine prompt = %q, want %q", got, want)
	}
	if result.StepResults["combine"].Output != "ok:combine" {
		t.Fatalf("combine output = %q", result.StepResults["combine"].Output)
	}
}

func TestBuiltinWorkflowEngine_TemplateResolutionScopedToDependsOn(t *testing.T) {
	// c depends only on b, but its config templates a reference to a's
	// output. a completes in the same level as b (both level 0), so a's
	// result IS present in the run's full results map by the time c
	// executes — but c never declared a dependency on a, so the
	// reference must still be rejected.
	wf := agentworkflow.WorkflowDefinition{
		Name: "undeclared-reference",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "fetch_a"}},
			{ID: "b", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "fetch_b"}},
			{
				ID: "c", Kind: agentworkflow.StepKindTool, DependsOn: []string{"b"},
				Config: map[string]any{"tool": "use", "args": map[string]any{"input": "{{ steps.a.output }}"}},
			},
		},
	}
	exec := &fakeStepExecutor{}
	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	result, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.StepResults["c"].IsError {
		t.Fatal("c references a step outside its depends_on and must fail, not silently resolve it")
	}
	if exec.toolCallCount("use") != 0 {
		t.Fatal("c's tool must never have been called once config resolution failed")
	}
}

func TestBuiltinWorkflowEngine_SkipPropagatesOnFailure(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "skip-chain",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "failing"}},
			{ID: "b", Kind: agentworkflow.StepKindTool, DependsOn: []string{"a"}, Config: map[string]any{"tool": "unreached"}},
			{ID: "c", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "independent"}},
		},
	}
	exec := &fakeStepExecutor{
		toolFunc: func(req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
			if req.Tool == "failing" {
				return agentworkflow.ToolStepResult{Output: "boom", IsError: true}, nil
			}
			return agentworkflow.ToolStepResult{Output: "ok"}, nil
		},
	}

	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	result, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != agentworkflow.RunStatusFailed {
		t.Fatalf("Status = %q, want failed", result.Status)
	}
	if !result.StepResults["a"].IsError {
		t.Fatal("a should be marked failed")
	}
	if !result.StepResults["b"].IsError {
		t.Fatal("b should be marked skipped(=error)")
	}
	if exec.toolCallCount("unreached") != 0 {
		t.Fatal("b's tool must never have been called")
	}
	if result.StepResults["c"].IsError {
		t.Fatal("c is independent of a and must still complete")
	}
	if exec.toolCallCount("independent") != 1 {
		t.Fatalf("c's tool call count = %d, want 1", exec.toolCallCount("independent"))
	}
}

func TestBuiltinWorkflowEngine_GatePausesDependents(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "gated",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "prep"}},
			{ID: "approve", Kind: agentworkflow.StepKindGate, DependsOn: []string{"a"}, Config: map[string]any{"description": "human approval"}},
			{ID: "publish", Kind: agentworkflow.StepKindTool, DependsOn: []string{"approve"}, Config: map[string]any{"tool": "publish"}},
		},
	}
	exec := &fakeStepExecutor{}
	runStore := newTestWorkflowStore(t)
	eng := NewBuiltinWorkflowEngine(runStore)

	result, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaiting {
		t.Fatalf("Status = %q, want waiting_on_gate", result.Status)
	}
	if exec.toolCallCount("publish") != 0 {
		t.Fatal("publish must not run while its gate is unresolved")
	}

	// Find the run id the engine generated and confirm persisted state:
	// approve is waiting_on_gate, publish is still pending.
	runs := listAllRuns(t, runStore)
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	steps, err := runStore.ListWorkflowRunSteps(runs[0])
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	byID := map[string]*store.WorkflowRunStepRow{}
	for _, s := range steps {
		byID[s.StepID] = s
	}
	if byID["approve"].Status != "waiting_on_gate" {
		t.Fatalf("approve status = %q", byID["approve"].Status)
	}
	if byID["publish"].Status != "pending" {
		t.Fatalf("publish status = %q, want pending", byID["publish"].Status)
	}
}

func TestBuiltinWorkflowEngine_VerifyEngineModeFailsStep(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "verified",
		Steps: []agentworkflow.StepDefinition{
			{
				ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "flaky"},
				Verify: &agentworkflow.VerifySpec{Mode: agentworkflow.VerifyModeEngine, EngineCheck: "no_error"},
			},
		},
	}
	exec := &fakeStepExecutor{
		toolFunc: func(agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
			return agentworkflow.ToolStepResult{Output: "looked fine"}, nil // step itself reports success
		},
		verifyFunc: func(req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
			return agentworkflow.VerifyResult{Passed: false, Reason: "reviewer disagrees"}, nil
		},
	}

	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	result, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != agentworkflow.RunStatusFailed {
		t.Fatalf("Status = %q, want failed (verify should override a nominally-successful step)", result.Status)
	}
	if !result.StepResults["a"].IsError {
		t.Fatal("a should be marked failed once verify rejects it")
	}
	if len(exec.verifyCalls) != 1 {
		t.Fatalf("len(verifyCalls) = %d, want 1", len(exec.verifyCalls))
	}
	if !strings.Contains(result.StepResults["a"].Output, "reviewer disagrees") {
		t.Fatalf("Output = %q, want it to include the verify failure reason", result.StepResults["a"].Output)
	}
}

func TestBuiltinWorkflowEngine_VerifyRunsEvenWhenStepAlreadyErrored(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "verify-on-error",
		Steps: []agentworkflow.StepDefinition{
			{
				ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "broken"},
				Verify: &agentworkflow.VerifySpec{Mode: agentworkflow.VerifyModeEngine, EngineCheck: "no_error"},
			},
		},
	}
	exec := &fakeStepExecutor{
		toolFunc: func(agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
			return agentworkflow.ToolStepResult{Output: "boom", IsError: true}, nil
		},
		verifyFunc: func(req agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
			if !req.Subject.IsError {
				t.Fatalf("verify Subject.IsError = false, want true")
			}
			return agentworkflow.VerifyResult{Passed: false, Reason: "subject step returned an error"}, nil
		},
	}
	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	if _, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(exec.verifyCalls) != 1 {
		t.Fatalf("len(verifyCalls) = %d, want 1 (verify must run even on an already-errored step)", len(exec.verifyCalls))
	}
}

// TestBuiltinWorkflowEngine_FlexStepEntersWaitingState supersedes task 03's
// own placeholder regression test (TestBuiltinWorkflowEngine_
// FlexStepReachesDispatchNotYetImplemented, which asserted the "not yet
// implemented" failure task 06 is chartered to replace — see 03's own
// comment on the StepKindFlex dispatch case in workflow_engine.go). Real
// flex-step execution (TASKS/teams/06-stepkindflex-executor.md) makes a
// flex step reuse gate's pause shape on first entry: a distinct
// waiting_on_flex status, not a terminal failure. Exit-trigger firing and
// resolution are covered separately in workflow_engine_flex_test.go.
//
// Still proves flex landing alongside an ordinary tool step in the same
// run doesn't disturb that sibling step — the same regression 03's
// placeholder test cared about, now updated for the real waiting outcome.
func TestBuiltinWorkflowEngine_FlexStepEntersWaitingState(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "flex-scope-work",
		Steps: []agentworkflow.StepDefinition{
			{
				ID: "scope_work", Kind: agentworkflow.StepKindFlex,
				Config: map[string]any{
					"active_slots": []any{"orchestrator", "engineer"},
					"exit_trigger": map[string]any{"self_tool": "mark_ready_for_review"},
				},
			},
			{ID: "independent", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "unrelated"}},
		},
	}
	exec := &fakeStepExecutor{}
	runStore := newTestWorkflowStore(t)
	eng := NewBuiltinWorkflowEngine(runStore)

	result, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status = %q, want %q", result.Status, agentworkflow.RunStatusWaitingOnFlex)
	}
	if _, ok := result.StepResults["scope_work"]; ok {
		t.Fatal("scope_work must not appear in StepResults while still waiting — same convention gate steps already follow")
	}

	// A still-waiting flex step must dispatch to neither ExecuteLLMStep
	// nor ExecuteToolStep — those calls belong to llm/tool steps only,
	// and a flex step silently routing into one of them (rather than its
	// own explicit runStep branch) would itself be a bug this test needs
	// to catch.
	if len(exec.llmCalls) != 0 {
		t.Fatalf("len(llmCalls) = %d, want 0 — flex must not fall through into the llm dispatch path", len(exec.llmCalls))
	}
	if exec.toolCallCount("unrelated") != 1 {
		t.Fatalf("independent tool step's own call count = %d, want 1 — a flex step in the same run must not disturb an unrelated sibling step", exec.toolCallCount("unrelated"))
	}
	if result.StepResults["independent"].IsError {
		t.Fatal("independent tool step must still succeed; it has no dependency on the flex step")
	}

	// Persisted state must reflect this too: kind='flex' actually landed
	// in workflow_run_steps (proving migration 130's CHECK widening is
	// exercised end-to-end) with the new waiting_on_flex status (migration
	// 133), not left dangling in "running" and not the old placeholder's
	// "failed".
	runs := listAllRuns(t, runStore)
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	steps, err := runStore.ListWorkflowRunSteps(runs[0])
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	var flexRow *store.WorkflowRunStepRow
	for _, s := range steps {
		if s.StepID == "scope_work" {
			flexRow = s
		}
	}
	if flexRow == nil {
		t.Fatal("persisted workflow_run_steps has no row for the flex step")
	}
	if flexRow.Kind != "flex" {
		t.Fatalf("persisted kind = %q, want %q", flexRow.Kind, "flex")
	}
	if flexRow.Status != "waiting_on_flex" {
		t.Fatalf("persisted status = %q, want %q", flexRow.Status, "waiting_on_flex")
	}
	if flexRow.IsError {
		t.Fatal("persisted is_error = true, want false — waiting is not a failure")
	}
}

func TestBuiltinWorkflowEngine_ConcurrentLevelFanOut(t *testing.T) {
	wf := agentworkflow.WorkflowDefinition{
		Name: "fan-out",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "slow"}},
			{ID: "b", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "slow"}},
			{ID: "c", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "slow"}},
		},
	}

	var mu sync.Mutex
	current, maxConcurrent := 0, 0
	exec := &fakeStepExecutor{
		toolFunc: func(agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
			mu.Lock()
			current++
			if current > maxConcurrent {
				maxConcurrent = current
			}
			mu.Unlock()

			time.Sleep(30 * time.Millisecond)

			mu.Lock()
			current--
			mu.Unlock()
			return agentworkflow.ToolStepResult{Output: "ok"}, nil
		},
	}

	eng := NewBuiltinWorkflowEngine(newTestWorkflowStore(t))
	if _, err := eng.Run(context.Background(), wf, agentworkflow.WorkflowInput{}, exec); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if maxConcurrent < 2 {
		t.Fatalf("maxConcurrent = %d, want >= 2 (same-level steps should fan out across goroutines)", maxConcurrent)
	}
}

func TestBuiltinWorkflowEngine_ResumeContinuesFromPersistedState(t *testing.T) {
	runStore := newTestWorkflowStore(t)
	wf := agentworkflow.WorkflowDefinition{
		Name: "resumable",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "fetch"}},
			{ID: "b", Kind: agentworkflow.StepKindTool, DependsOn: []string{"a"}, Config: map[string]any{
				"tool": "use", "args": map[string]any{"input": "{{ steps.a.output }}"},
			}},
		},
	}

	// Seed persisted state as though a prior process completed step "a"
	// and then crashed before starting "b".
	if err := runStore.CreateWorkflowRun(&store.WorkflowRunRow{ID: "run-crash-1", DefinitionName: wf.Name, Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	if err := runStore.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: "run-crash-1", StepID: "a", Kind: "tool", Status: "completed", Output: "persisted-value",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(a): %v", err)
	}
	if err := runStore.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: "run-crash-1", StepID: "b", Kind: "tool", Status: "pending",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(b): %v", err)
	}

	exec := &fakeStepExecutor{}
	eng := NewBuiltinWorkflowEngine(runStore)
	result, err := eng.Resume(context.Background(), "run-crash-1", wf, exec)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status = %q, want completed (error=%s)", result.Status, result.Error)
	}
	if exec.toolCallCount("fetch") != 0 {
		t.Fatal("a was already completed before the crash and must not be re-executed")
	}
	if len(exec.toolCalls) != 1 || exec.toolCalls[0].Tool != "use" {
		t.Fatalf("toolCalls = %+v, want exactly one call to \"use\"", exec.toolCalls)
	}
	if exec.toolCalls[0].Args["input"] != "persisted-value" {
		t.Fatalf("b's templated arg = %v, want the persisted output of a", exec.toolCalls[0].Args["input"])
	}
}

func TestBuiltinWorkflowEngine_ResumeRerunsInterruptedRunningStep(t *testing.T) {
	runStore := newTestWorkflowStore(t)
	wf := agentworkflow.WorkflowDefinition{
		Name: "interrupted",
		Steps: []agentworkflow.StepDefinition{
			{ID: "a", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "fetch"}},
		},
	}
	if err := runStore.CreateWorkflowRun(&store.WorkflowRunRow{ID: "run-crash-2", DefinitionName: wf.Name, Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	// "running" simulates a step that was mid-flight when the process died.
	if err := runStore.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: "run-crash-2", StepID: "a", Kind: "tool", Status: "running",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(a): %v", err)
	}

	exec := &fakeStepExecutor{}
	eng := NewBuiltinWorkflowEngine(runStore)
	result, err := eng.Resume(context.Background(), "run-crash-2", wf, exec)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	if exec.toolCallCount("fetch") != 1 {
		t.Fatalf("interrupted step must be re-run exactly once, got %d calls", exec.toolCallCount("fetch"))
	}
}

// listAllRuns is a small test-only helper: the engine generates run ids
// internally, so tests that need the id scan workflow_run_steps for its
// distinct workflow_run_id values via the one step we know exists.
func listAllRuns(t *testing.T, s *store.Store) []string {
	t.Helper()
	rows, err := s.DB.Query(`SELECT id FROM workflow_runs`)
	if err != nil {
		t.Fatalf("query workflow_runs: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}
