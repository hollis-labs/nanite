package workflowhost

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

func TestExternalReceiptCrashAfterCompletionReplaysWithoutDuplicateEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external-completed-replay.db")
	product, state := openWorkflowStateTest(t, path)
	if _, err := product.DB.ExecContext(t.Context(), `
CREATE TRIGGER workflow_test_crash_after_external_receipt
BEFORE UPDATE OF status ON workflow_external_operations
WHEN NEW.status <> 'pending'
BEGIN SELECT RAISE(ABORT, 'injected crash after external receipt'); END`); err != nil {
		t.Fatal(err)
	}
	host := &recordingExternalHost{result: ExternalStepResult{Output: "published"}}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithExternalStepHost(host)
	_, runErr := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{
		Name: "external completed receipt replay", Engine: agentworkflow.EngineLangGraph,
		Steps: []agentworkflow.StepDefinition{{ID: "ignored", Kind: agentworkflow.StepKindLLM}},
	}, agentworkflow.WorkflowInput{Params: map[string]any{"target": "staging"}, SessionID: "session-1"}, &recordingStepExecutor{})
	if runErr == nil {
		t.Fatal("external run succeeded despite injected post-receipt persistence crash")
	}
	if len(host.calls) != 1 {
		t.Fatalf("external host calls before restart = %d, want 1", len(host.calls))
	}
	receiptID := host.calls[0].IdempotencyKey
	runID := effectRunID(t, receiptID, "nanite:external-engine", "external-run")
	committed, err := state.LoadExternalExecutionReceipt(t.Context(), receiptID)
	if err != nil || committed.State != ExternalExecutionCompleted || committed.Result == nil || committed.Result.Output != "published" {
		t.Fatalf("committed receipt = %+v, %v", committed, err)
	}
	if _, triggerErr := product.DB.ExecContext(t.Context(), `DROP TRIGGER workflow_test_crash_after_external_receipt`); triggerErr != nil {
		t.Fatal(triggerErr)
	}
	if closeErr := product.Close(context.Background()); closeErr != nil {
		t.Fatal(closeErr)
	}

	_, reopened := openWorkflowStateTest(t, path)
	restarted, err := NewEngine(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restarted.WithExternalStepHost(host)
	result, err := restarted.Resume(t.Context(), runID, &recordingStepExecutor{})
	if err != nil || result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("replayed external run = %+v, %v", result, err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("external host calls = %d, want 1", len(host.calls))
	}
}

func TestExternalReceiptCrashInCallBecomesAmbiguousAndOperatorResolves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external-ambiguous.db")
	product, state := openWorkflowStateTest(t, path)
	createExternalReceiptNode(t, state, "run-external-ambiguous", "external")
	request := externalExecutionRequest{
		Request: ExternalStepRequest{
			IdempotencyKey: hostEffectIdempotencyKey("nanite:external-engine", stepkind.InvocationIdentity{
				RunID: "run-external-ambiguous", NodeID: "external",
			}),
			Engine: "crewai", WorkflowName: "research", Params: map[string]any{"topic": "recovery"}, SessionID: "session-2",
		},
		ProductStepID: "run", ProductKind: "tool", NodeID: "external",
	}
	receipt, err := state.prepareExternalExecution(t.Context(), "run-external-ambiguous", "external", "", request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	host := &crashingExternalHost{}
	ref := externalTestRef(receipt)
	assertWorkflowPanic(t, func() { _, _ = (&naniteExternalKind{host: host, store: state}).Observe(t.Context(), ref) })
	pending, err := state.LoadExternalExecutionReceipt(t.Context(), receipt.ID)
	if err != nil || pending.State != ExternalExecutionPending {
		t.Fatalf("pending receipt = %+v, %v", pending, err)
	}
	if closeErr := product.Close(context.Background()); closeErr != nil {
		t.Fatal(closeErr)
	}

	_, reopened := openWorkflowStateTest(t, path)
	restarted := &naniteExternalKind{host: host, store: reopened}
	observation, err := restarted.Observe(t.Context(), ref)
	if err != nil || observation.State != stepkind.ObservationPending || observation.Progress["execution_state"] != "ambiguous" {
		t.Fatalf("ambiguous observation = %+v, %v", observation, err)
	}
	if host.Calls() != 1 {
		t.Fatalf("external host calls after restart = %d, want 1", host.Calls())
	}
	engine, err := NewEngine(reopened)
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := engine.AmbiguousExternalExecutions(t.Context(), 10)
	if err != nil || len(ambiguous) != 1 || ambiguous[0].ID != receipt.ID {
		t.Fatalf("ambiguous receipts = %+v, %v", ambiguous, err)
	}
	resolution := ExternalExecutionResolutionRequest{
		ReceiptID: receipt.ID, Action: ExternalResolutionSuccess, Output: "operator-confirmed",
		AuthenticatedPrincipal: "admin@example.test", Reason: "verified external framework audit log",
		IdempotencyKey: "resolve-external-1", ResolvedAt: time.Now().UTC().Add(time.Second),
	}
	resolved, err := reopened.resolveExternalExecution(t.Context(), resolution)
	if err != nil || resolved.State != ExternalExecutionCompleted || resolved.Result == nil || resolved.Result.Output != "operator-confirmed" ||
		resolved.ResolutionActor != resolution.AuthenticatedPrincipal || resolved.ResolutionReason != resolution.Reason {
		t.Fatalf("resolved receipt = %+v, %v", resolved, err)
	}
	replayed, err := reopened.resolveExternalExecution(t.Context(), resolution)
	if err != nil || replayed.State != ExternalExecutionCompleted {
		t.Fatalf("resolution replay = %+v, %v", replayed, err)
	}
	conflict := resolution
	conflict.Output = "different"
	if _, conflictErr := reopened.resolveExternalExecution(t.Context(), conflict); !errors.Is(conflictErr, workflowruntime.ErrIdempotencyConflict) {
		t.Fatalf("conflicting resolution error = %v", conflictErr)
	}
	observation, err = restarted.Observe(t.Context(), ref)
	if err != nil || observation.State != stepkind.ObservationSucceeded || observation.Result == nil {
		t.Fatalf("resolved observation = %+v, %v", observation, err)
	}
	if host.Calls() != 1 {
		t.Fatalf("resolved replay called external host %d times, want 1", host.Calls())
	}
}

func TestExternalOperationCrashRestartsAmbiguousAndProductionResolutionContinuesRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external-operation-crash.db")
	product, state := openWorkflowStateTest(t, path)
	host := &crashingExternalHost{}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithExternalStepHost(host)
	exec := &recordingStepExecutor{}
	definition := agentworkflow.WorkflowDefinition{
		Name: "external crash operator recovery", Engine: agentworkflow.EngineLangGraph,
		Steps: []agentworkflow.StepDefinition{{ID: "ignored", Kind: agentworkflow.StepKindLLM}},
	}
	assertWorkflowPanic(t, func() {
		_, _ = engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{
			Params: map[string]any{"target": "production"}, SessionID: "external-session",
		}, exec)
	})
	receiptID := host.LastKey()
	runID := effectRunID(t, receiptID, "nanite:external-engine", "external-run")
	if runID == "" || runID == receiptID || host.Calls() != 1 {
		t.Fatalf("crashed external identity receipt=%q run=%q calls=%d", receiptID, runID, host.Calls())
	}
	var operations int
	if queryErr := product.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workflow_external_operations WHERE run_id=?`, runID).Scan(&operations); queryErr != nil || operations != 1 {
		t.Fatalf("durable external operations = %d, %v", operations, queryErr)
	}
	if closeErr := product.Close(context.Background()); closeErr != nil {
		t.Fatal(closeErr)
	}

	_, reopened := openWorkflowStateTest(t, path)
	restarted, err := NewEngine(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restarted.WithExternalStepHost(host)
	waiting, err := restarted.Resume(t.Context(), runID, exec)
	if err != nil || waiting.Status != agentworkflow.RunStatusRunning {
		t.Fatalf("ambiguous recovery = %+v, %v", waiting, err)
	}
	if host.Calls() != 1 {
		t.Fatalf("ambiguous recovery called external host %d times, want 1", host.Calls())
	}
	ambiguous, err := reopened.LoadExternalExecutionReceipt(t.Context(), receiptID)
	if err != nil || ambiguous.State != ExternalExecutionAmbiguous {
		t.Fatalf("ambiguous receipt = %+v, %v", ambiguous, err)
	}
	resolved, err := restarted.ResolveExternalExecution(t.Context(), ExternalExecutionResolutionRequest{
		ReceiptID: receiptID, Action: ExternalResolutionSuccess, Output: "confirmed by framework",
		AuthenticatedPrincipal: "release-operator", Reason: "matched immutable provider execution id",
		IdempotencyKey: "external-operator-resolution", ResolvedAt: time.Now().UTC().Add(time.Second),
	}, exec)
	if err != nil || resolved.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("operator-resolved run = %+v, %v", resolved, err)
	}
	if host.Calls() != 1 {
		t.Fatalf("operator resolution called external host %d times, want 1", host.Calls())
	}
	receipt, err := reopened.LoadExternalExecutionReceipt(t.Context(), receiptID)
	if err != nil || receipt.ResolutionActor != "release-operator" || receipt.ResolutionReason == "" || receipt.ResolutionKey != "external-operator-resolution" {
		t.Fatalf("audited resolution receipt = %+v, %v", receipt, err)
	}
}

func TestLoopCrashAfterChildCreationReusesChildAndPersistsWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop-crash-window.db")
	product, state := openWorkflowStateTest(t, path)
	goal := &nanitestore.Goal{ID: "goal-loop-crash", Intent: "survive the child creation crash window"}
	if err := product.CreateGoal(t.Context(), goal); err != nil {
		t.Fatal(err)
	}
	firstHost := &crashAfterCreateLoopHost{store: product, id: "loop-child-once", crash: true}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithLoopStepHost(firstHost)
	exec := &recordingStepExecutor{}
	definition := agentworkflow.WorkflowDefinition{Name: "loop crash recovery", Steps: []agentworkflow.StepDefinition{{
		ID: "loop", Kind: agentworkflow.StepKindLoop,
		Config: map[string]any{"workflow_name": "inner", "agent_profile_id": "agent", "goal_id": goal.ID},
	}}}
	assertWorkflowPanic(t, func() { _, _ = engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{}, exec) })
	runID := firstHost.OuterRunID()
	if runID == "" || firstHost.Calls() != 1 || firstHost.Creations() != 1 {
		t.Fatalf("first launch run=%q calls=%d creations=%d", runID, firstHost.Calls(), firstHost.Creations())
	}
	var waits int
	if queryErr := product.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workflow_waits WHERE run_id=?`, runID).Scan(&waits); queryErr != nil || waits != 0 {
		t.Fatalf("waits before recovery = %d, %v", waits, queryErr)
	}
	if closeErr := product.Close(context.Background()); closeErr != nil {
		t.Fatal(closeErr)
	}

	reopenedProduct, reopened := openWorkflowStateTest(t, path)
	secondHost := &crashAfterCreateLoopHost{store: reopenedProduct, id: firstHost.id}
	restarted, err := NewEngine(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restarted.WithLoopStepHost(secondHost)
	quiescent, err := restarted.Resume(t.Context(), runID, exec)
	if err != nil || quiescent.Status != agentworkflow.RunStatusRunning {
		t.Fatalf("immediate restart with unexpired claim = %+v, %v", quiescent, err)
	}
	if secondHost.Calls() != 0 || secondHost.Creations() != 0 {
		t.Fatalf("unexpired claim recovery calls=%d creations=%d, want 0/0", secondHost.Calls(), secondHost.Creations())
	}
	if _, updateErr := reopenedProduct.DB.ExecContext(t.Context(), `UPDATE workflow_node_leases SET expires_at=? WHERE run_id=?`, workflowTime(time.Now().UTC().Add(-time.Minute)), runID); updateErr != nil {
		t.Fatal(updateErr)
	}
	result, err := restarted.Resume(t.Context(), runID, exec)
	if err != nil || result.Status != agentworkflow.RunStatusWaitingOnLoop {
		t.Fatalf("recovered Loop result = %+v, %v", result, err)
	}
	if secondHost.Calls() != 1 || secondHost.Creations() != 0 {
		t.Fatalf("recovery calls=%d creations=%d, want 1/0", secondHost.Calls(), secondHost.Creations())
	}
	if queryErr := reopenedProduct.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workflow_waits WHERE run_id=? AND status='open'`, runID).Scan(&waits); queryErr != nil || waits != 1 {
		t.Fatalf("open waits after recovery = %d, %v", waits, queryErr)
	}
	var attempts, children int
	if queryErr := reopenedProduct.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workflow_attempts WHERE run_id=? AND node_id='loop'`, runID).Scan(&attempts); queryErr != nil {
		t.Fatal(queryErr)
	}
	if queryErr := reopenedProduct.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM loop_runs WHERE id=?`, firstHost.id).Scan(&children); queryErr != nil {
		t.Fatal(queryErr)
	}
	if attempts != 2 || children != 1 {
		t.Fatalf("attempts=%d children=%d, want 2/1", attempts, children)
	}
}

func TestHostEffectIdempotencyKeysScopeDynamicIterationNotAttempt(t *testing.T) {
	base := stepkind.InvocationIdentity{RunID: "run", NodeID: "node", Iteration: "item-a", Attempt: 1}
	otherIteration := base
	otherIteration.Iteration = "item-b"
	retry := base
	retry.Attempt = 2
	for _, namespace := range []string{"nanite:workflow-loop", "nanite:external-engine"} {
		first := hostEffectIdempotencyKey(namespace, base)
		if second := hostEffectIdempotencyKey(namespace, otherIteration); second == first {
			t.Fatalf("%s aliases dynamic iterations: %q", namespace, first)
		}
		if replay := hostEffectIdempotencyKey(namespace, retry); replay != first {
			t.Fatalf("%s retry key = %q, want %q", namespace, replay, first)
		}
	}
}

func TestActiveRecoveryCursorFairlyWrapsBoundedBatches(t *testing.T) {
	_, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "recovery-cursor.db"))
	base := time.Now().UTC().Add(-time.Minute)
	for index, id := range []string{"active-a", "active-b", "active-c"} {
		createWorkflowTestRun(t, state, id, base.Add(time.Duration(index)*time.Second))
	}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	want := []workflowruntime.RunID{"active-a", "active-b", "active-c", "active-a"}
	for index, expected := range want {
		batch, batchErr := engine.nextActiveRecoveryRunIDs(t.Context(), 1)
		if batchErr != nil || len(batch) != 1 || batch[0] != expected {
			t.Fatalf("batch %d = %v, %v; want [%s]", index, batch, batchErr, expected)
		}
	}
}

func TestActiveRecoveryLoopRetriesTerminalLoopWithoutRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "periodic-loop-recovery.db")
	product, state := openWorkflowStateTest(t, path)
	goal := &nanitestore.Goal{ID: "goal-periodic-loop", Intent: "resume despite a lost terminal push"}
	if err := product.CreateGoal(t.Context(), goal); err != nil {
		t.Fatal(err)
	}
	baseHost := &persistentLoopHost{store: product, id: "periodic-loop-child"}
	host := &flakyLoopObserver{LoopStepHost: baseHost}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithLoopStepHost(host)
	exec := &recordingStepExecutor{}
	waiting, err := engine.Run(t.Context(), agentworkflow.WorkflowDefinition{Name: "periodic loop recovery", Steps: []agentworkflow.StepDefinition{{
		ID: "loop", Kind: agentworkflow.StepKindLoop,
		Config: map[string]any{"workflow_name": "inner", "agent_profile_id": "agent", "goal_id": goal.ID},
	}}}, agentworkflow.WorkflowInput{}, exec)
	if err != nil || waiting.Status != agentworkflow.RunStatusWaitingOnLoop {
		t.Fatalf("launch = %+v, %v", waiting, err)
	}
	completedAt := time.Now().UTC()
	if err := product.UpdateLoopRunStatus(t.Context(), baseHost.id, nanitestore.LoopRunStatusCompleted, &completedAt); err != nil {
		t.Fatal(err)
	}
	host.FailNext(2) // terminal scan and ordinary Resume both fail in the first pass
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstFailed := make(chan struct{})
	var failedOnce sync.Once
	go engine.RunActiveRecoveryLoop(ctx, exec, 10*time.Millisecond, 1, func(_ ActiveRecoveryReport, passErr error) {
		if passErr != nil {
			failedOnce.Do(func() { close(firstFailed) })
		}
	})
	select {
	case <-firstFailed:
	case <-time.After(5 * time.Second):
		t.Fatal("periodic recovery did not report the injected first-pass failure")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, loadErr := state.LoadRun(t.Context(), workflowruntime.RunID(waiting.RunID))
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if run.Status == workflowruntime.RunSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("periodic recovery left run %s in %s", waiting.RunID, run.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if host.ObserveCalls() < 3 {
		t.Fatalf("LoopRun observations = %d, want retry after failed pass", host.ObserveCalls())
	}
}

func createExternalReceiptNode(t *testing.T, state *WorkflowStateStore, runID, nodeID string) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Minute)
	run := createWorkflowTestRun(t, state, runID, base)
	createWorkflowTestNode(t, state, run.ID, nodeID, base.Add(time.Second))
}

func externalTestRef(receipt ExternalExecutionReceipt) stepkind.ExternalOperationRef {
	return stepkind.ExternalOperationRef{Kind: "nanite-external-subprocess", ID: receipt.ID, Metadata: map[string]string{
		"request_digest": receipt.RequestDigest, "receipt_contract": "nanite-external-execution@v1",
	}}
}

func effectRunID(t *testing.T, key, namespace, nodeID string) string {
	t.Helper()
	prefix := namespace + ":"
	remainder := strings.TrimPrefix(key, prefix)
	parts := strings.SplitN(remainder, ":"+nodeID+":", 2)
	if remainder == key || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		t.Fatalf("effect key %q does not match %s/<run>/%s/<iteration>", key, namespace, nodeID)
	}
	return parts[0]
}

func assertWorkflowPanic(t *testing.T, run func()) {
	t.Helper()
	deferred := false
	func() {
		defer func() { deferred = recover() != nil }()
		run()
	}()
	if !deferred {
		t.Fatal("expected injected process crash panic")
	}
}

type crashingExternalHost struct {
	mu      sync.Mutex
	calls   int
	lastKey string
}

func (h *crashingExternalHost) ExecuteWorkflowExternalStep(_ context.Context, request ExternalStepRequest) (ExternalStepResult, error) {
	h.mu.Lock()
	h.calls++
	h.lastKey = request.IdempotencyKey
	h.mu.Unlock()
	panic("process crashed during external call")
}

func (h *crashingExternalHost) LastKey() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastKey
}

func (h *crashingExternalHost) Calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

type crashAfterCreateLoopHost struct {
	store *nanitestore.Store
	id    string
	crash bool

	mu         sync.Mutex
	calls      int
	creations  int
	outerRunID string
}

func (h *crashAfterCreateLoopHost) LaunchWorkflowLoop(ctx context.Context, request LoopStepLaunchRequest) (LoopStepResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	prefix := "nanite:workflow-loop:"
	remainder := strings.TrimPrefix(request.IdempotencyKey, prefix)
	h.outerRunID = strings.SplitN(remainder, ":loop:", 2)[0]
	if existing, err := h.store.GetLoopRun(ctx, h.id); err == nil {
		return LoopStepResult{LoopRunID: existing.ID, Status: existing.Status, CurrentIteration: existing.CurrentIteration}, nil
	}
	run := &nanitestore.LoopRun{ID: h.id, GoalID: request.GoalID, DefinitionName: request.WorkflowName, Status: nanitestore.LoopRunStatusWaitingOnEscalation}
	if err := h.store.CreateLoopRun(ctx, run); err != nil {
		return LoopStepResult{}, err
	}
	h.creations++
	if h.crash {
		h.crash = false
		panic("process crashed after LoopRun creation")
	}
	return LoopStepResult{LoopRunID: run.ID, Status: run.Status}, nil
}

func (h *crashAfterCreateLoopHost) ObserveWorkflowLoop(ctx context.Context, id string) (LoopStepResult, error) {
	run, err := h.store.GetLoopRun(ctx, id)
	if err != nil {
		return LoopStepResult{}, err
	}
	return LoopStepResult{LoopRunID: run.ID, Status: run.Status, CurrentIteration: run.CurrentIteration}, nil
}

func (h *crashAfterCreateLoopHost) Calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func (h *crashAfterCreateLoopHost) Creations() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.creations
}

func (h *crashAfterCreateLoopHost) OuterRunID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.outerRunID
}

type flakyLoopObserver struct {
	LoopStepHost
	mu        sync.Mutex
	remaining int
	calls     int
}

func (h *flakyLoopObserver) FailNext(count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remaining = count
}

func (h *flakyLoopObserver) ObserveWorkflowLoop(ctx context.Context, id string) (LoopStepResult, error) {
	h.mu.Lock()
	h.calls++
	if h.remaining > 0 {
		h.remaining--
		h.mu.Unlock()
		return LoopStepResult{}, errors.New("injected transient LoopRun observation failure")
	}
	h.mu.Unlock()
	return h.LoopStepHost.ObserveWorkflowLoop(ctx, id)
}

func (h *flakyLoopObserver) ObserveCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}
