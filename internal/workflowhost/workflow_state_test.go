package workflowhost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

func TestWorkflowStateLifecycleCASIdempotencyAndProductProjection(t *testing.T) {
	store, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "lifecycle.db"))
	ctx := context.Background()
	base := workflowTestTime()
	plan := workflowTestPlan("lifecycle")
	request := workflowruntime.CreateRunRequest{
		ID: "run-lifecycle", Plan: plan, Status: workflowruntime.RunPending,
		StartIdempotencyKey: "start-lifecycle", CreatedAt: base,
	}
	run, outcome, err := state.CreateRun(ctx, request)
	if err != nil || outcome != workflowruntime.IdempotencyApplied {
		t.Fatalf("CreateRun = (%+v, %q, %v)", run, outcome, err)
	}
	replay := request
	replay.CreatedAt = base.In(time.FixedZone("same-instant", -5*60*60))
	if replayed, replayOutcome, replayErr := state.CreateRun(ctx, replay); replayErr != nil || replayOutcome != workflowruntime.IdempotencyReplayed || replayed.ID != run.ID {
		t.Fatalf("CreateRun replay = (%+v, %q, %v)", replayed, replayOutcome, replayErr)
	}
	conflict := request
	conflict.ID = "run-other"
	if _, _, conflictErr := state.CreateRun(ctx, conflict); !errors.Is(conflictErr, workflowruntime.ErrIdempotencyConflict) {
		t.Fatalf("CreateRun conflicting idempotency error = %v", conflictErr)
	}
	if _, staleErr := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
		RunID: run.ID, ExpectedGeneration: 0, To: workflowruntime.RunRunning, At: base.Add(time.Second),
	}); !errors.Is(staleErr, workflowruntime.ErrCASMismatch) {
		t.Fatalf("stale TransitionRun error = %v", staleErr)
	}

	if projectionErr := state.RecordPlanNodeProjections(ctx, plan, []PlanNodeProjection{{
		NodeID: "approval", ProductStepID: "approval-step", ProductKind: "gate", WaitClass: "gate",
	}}); projectionErr != nil {
		t.Fatalf("RecordPlanNodeProjections: %v", projectionErr)
	}
	if projectionErr := state.RecordPlanNodeProjections(ctx, plan, []PlanNodeProjection{{
		NodeID: "approval", ProductStepID: "approval-step", ProductKind: "gate", WaitClass: "gate",
	}}); projectionErr != nil {
		t.Fatalf("RecordPlanNodeProjections exact replay: %v", projectionErr)
	}
	if projectionErr := state.RecordPlanNodeProjections(ctx, plan, []PlanNodeProjection{{
		NodeID: "approval", ProductStepID: "changed", ProductKind: "gate", WaitClass: "gate",
	}}); !errors.Is(projectionErr, workflowruntime.ErrAlreadyExists) {
		t.Fatalf("RecordPlanNodeProjections mutation error = %v", projectionErr)
	}

	running, err := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
		RunID: run.ID, ExpectedGeneration: run.Generation,
		To: workflowruntime.RunRunning, At: base.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("TransitionRun pending->running: %v", err)
	}
	node := createWorkflowTestNode(t, state, run.ID, "approval", base.Add(time.Second))
	ready, err := state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{
		InvocationID: node.ID, ExpectedGeneration: node.Generation,
		To: workflowruntime.NodeReady, At: base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("TransitionNode pending->ready: %v", err)
	}
	claim, err := state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{
		InvocationID: node.ID, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration,
		Owner: "worker", Token: "claim", IdempotencyKey: "claim-lifecycle",
		Now: base.Add(3 * time.Second), LeaseUntil: base.Add(time.Minute),
	})
	if err != nil || !claim.Acquired || claim.Lease == nil {
		t.Fatalf("ClaimNode = (%+v, %v)", claim, err)
	}
	claimed, err := state.LoadNodeInvocation(ctx, node.ID)
	if err != nil {
		t.Fatalf("LoadNodeInvocation after claim: %v", err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	started, err := state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{
		InvocationID: node.ID, ExpectedNodeGeneration: claimed.Generation, Claim: proof,
		Executor: workflowruntime.ExecutorMetadata{Kind: "test", Version: "v1"}, At: base.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("StartNodeAttempt: %v", err)
	}
	finished, err := state.FinishNodeAttempt(ctx, workflowruntime.FinishNodeAttemptRequest{
		InvocationID: node.ID, AttemptNumber: started.Attempt.ID.Number,
		ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation,
		Claim: proof, AttemptStatus: workflowruntime.NodeSucceeded,
		NextNodeStatus: workflowruntime.NodeSucceeded, At: base.Add(5 * time.Second),
	})
	if err != nil || finished.Node.Status != workflowruntime.NodeSucceeded {
		t.Fatalf("FinishNodeAttempt = (%+v, %v)", finished, err)
	}
	completed, err := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
		RunID: run.ID, ExpectedGeneration: running.Snapshot.Generation,
		To: workflowruntime.RunSucceeded, At: base.Add(6 * time.Second),
	})
	if err != nil || completed.Snapshot.Status != workflowruntime.RunSucceeded {
		t.Fatalf("TransitionRun running->succeeded = (%+v, %v)", completed, err)
	}

	var productRunStatus, runtimeStatus, engineKind, contractVersion, planDigest, completedAt string
	var runtimeGeneration int64
	if queryErr := store.DB.QueryRowContext(ctx, `
SELECT status, runtime_status, engine_kind, engine_contract_version, plan_digest,
       runtime_generation, completed_at
FROM workflow_runs WHERE id=?`, run.ID).Scan(
		&productRunStatus, &runtimeStatus, &engineKind, &contractVersion, &planDigest,
		&runtimeGeneration, &completedAt,
	); queryErr != nil {
		t.Fatalf("read projected workflow run: %v", queryErr)
	}
	if productRunStatus != "completed" || runtimeStatus != string(workflowruntime.RunSucceeded) ||
		engineKind != EngineKindGoWorkflow || contractVersion != EngineContractVersion || planDigest != plan.Digest ||
		strconv.FormatInt(runtimeGeneration, 10) != strconv.FormatUint(completed.Snapshot.Generation, 10) || completedAt == "" {
		t.Fatalf("projected run = product=%q runtime=%q engine=%q contract=%q plan=%q generation=%d completed=%q",
			productRunStatus, runtimeStatus, engineKind, contractVersion, planDigest, runtimeGeneration, completedAt)
	}
	var stepID, kind, stepStatus, stepCompletedAt string
	if queryErr := store.DB.QueryRowContext(ctx, `
SELECT step_id, kind, status, completed_at
FROM workflow_run_steps WHERE workflow_run_id=?`, run.ID).Scan(&stepID, &kind, &stepStatus, &stepCompletedAt); queryErr != nil {
		t.Fatalf("read projected workflow step: %v", queryErr)
	}
	if stepID != "approval-step" || kind != "gate" || stepStatus != "completed" || stepCompletedAt == "" {
		t.Fatalf("projected step = id=%q kind=%q status=%q completed=%q", stepID, kind, stepStatus, stepCompletedAt)
	}

	// A product projection failure must roll back the canonical mutation too.
	rollbackRun := createWorkflowTestRun(t, state, "run-projection-rollback", base.Add(10*time.Second))
	if _, triggerErr := store.DB.ExecContext(ctx, `
CREATE TRIGGER workflow_test_reject_product_projection
BEFORE UPDATE OF status ON workflow_runs
WHEN NEW.id = 'run-projection-rollback'
BEGIN SELECT RAISE(ABORT, 'test projection failure'); END`); triggerErr != nil {
		t.Fatalf("create projection failure trigger: %v", triggerErr)
	}
	if _, transitionErr := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
		RunID: rollbackRun.ID, ExpectedGeneration: rollbackRun.Generation,
		To: workflowruntime.RunRunning, At: base.Add(11 * time.Second),
	}); transitionErr == nil {
		t.Fatal("TransitionRun unexpectedly succeeded with failing product projection")
	}
	loaded, err := state.LoadRun(ctx, rollbackRun.ID)
	if err != nil || loaded.Status != workflowruntime.RunPending || loaded.Generation != rollbackRun.Generation {
		t.Fatalf("projection failure mutated canonical run: %+v, %v", loaded, err)
	}
	if _, triggerErr := store.DB.ExecContext(ctx, `
CREATE TRIGGER workflow_test_reject_product_step_projection
BEFORE INSERT ON workflow_run_steps
WHEN NEW.workflow_run_id = 'run-projection-rollback'
BEGIN SELECT RAISE(ABORT, 'test step projection failure'); END`); triggerErr != nil {
		t.Fatalf("create step projection failure trigger: %v", triggerErr)
	}
	if _, createErr := state.CreateNodeInvocation(ctx, workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{
		ID:     workflowruntime.NodeInvocationID{RunID: rollbackRun.ID, NodeID: "must-rollback"},
		Status: workflowruntime.NodePending, CreatedAt: base.Add(12 * time.Second), UpdatedAt: base.Add(12 * time.Second),
	}}); createErr == nil {
		t.Fatal("CreateNodeInvocation unexpectedly succeeded with failing product projection")
	}
	var canonicalNodes int
	if queryErr := store.DB.QueryRowContext(ctx, `
SELECT COUNT(1) FROM workflow_node_invocations
WHERE run_id=? AND node_id='must-rollback'`, rollbackRun.ID).Scan(&canonicalNodes); queryErr != nil {
		t.Fatalf("count canonical nodes after projection rollback: %v", queryErr)
	}
	if canonicalNodes != 0 {
		t.Fatalf("step projection failure left %d canonical node rows", canonicalNodes)
	}
}

func TestWorkflowStateWaitResumeRecoveryAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wait-restart.db")
	store, state := openWorkflowStateTest(t, path)
	ctx := context.Background()
	fixture := prepareWorkflowSQLiteWait(t, state, "restart", workflowTestTime(), time.Hour)
	coordinator := workflowruntime.WaitCoordinator{Store: state}
	readyNode := createWorkflowTestNode(t, state, fixture.invocation.RunID, "ready-after-restart", fixture.base)
	ready, err := state.TransitionNode(ctx, workflowruntime.NodeTransitionRequest{
		InvocationID: readyNode.ID, ExpectedGeneration: readyNode.Generation,
		To: workflowruntime.NodeReady, At: fixture.base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("prepare ready node for restart: %v", err)
	}
	suspended, err := coordinator.Suspend(ctx, workflowruntime.SuspendCommand{Request: fixture.request, ResumeToken: fixture.token})
	if err != nil || suspended.Node.Status != workflowruntime.NodeWaiting || suspended.Node.Lease != nil {
		t.Fatalf("Suspend = (%+v, %v)", suspended, err)
	}
	run, err := state.LoadRun(ctx, fixture.invocation.RunID)
	if err != nil {
		t.Fatalf("LoadRun before waiting transition: %v", err)
	}
	waitingRun, err := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
		RunID: run.ID, ExpectedGeneration: run.Generation,
		To: workflowruntime.RunWaiting, At: fixture.base.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("TransitionRun running->waiting: %v", err)
	}
	var productRunStatus, productStepStatus string
	if queryErr := store.DB.QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, run.ID).Scan(&productRunStatus); queryErr != nil {
		t.Fatal(queryErr)
	}
	if queryErr := store.DB.QueryRowContext(ctx, `SELECT status FROM workflow_run_steps WHERE workflow_run_id=? AND step_id='wait-step'`, run.ID).Scan(&productStepStatus); queryErr != nil {
		t.Fatal(queryErr)
	}
	if productRunStatus != "waiting_on_gate" || productStepStatus != "waiting_on_gate" {
		t.Fatalf("waiting projection run=%q step=%q", productRunStatus, productStepStatus)
	}
	if closeErr := store.Close(ctx); closeErr != nil {
		t.Fatalf("close before restart: %v", closeErr)
	}

	reopenedStore, reopened := openWorkflowStateTest(t, path)
	recovery, err := reopened.Recovery(ctx, workflowruntime.RecoveryQuery{RunID: run.ID, Now: fixture.base.Add(5 * time.Second)})
	if err != nil || len(recovery.Waiting) != 1 || recovery.Waiting[0].ID != fixture.invocation ||
		len(recovery.Ready) != 1 || recovery.Ready[0].ID != ready.Snapshot.ID {
		t.Fatalf("Recovery after restart = (%+v, %v)", recovery, err)
	}
	loadedWait, err := reopened.LoadWait(ctx, fixture.waitID)
	if err != nil || loadedWait.Status != workflowruntime.WaitOpen || loadedWait.Invocation != fixture.invocation {
		t.Fatalf("LoadWait after restart = (%+v, %v)", loadedWait, err)
	}
	command := fixture.resumeCommand(t, "resume-restart", fixture.base.Add(6*time.Second))
	resumed, err := (workflowruntime.WaitCoordinator{Store: reopened}).Resume(ctx, command)
	if err != nil || resumed.Outcome != workflowruntime.ResumeApplied || resumed.Wait.Status != workflowruntime.WaitResumed || resumed.Node.Status != workflowruntime.NodeReady {
		t.Fatalf("Resume after restart = (%+v, %v)", resumed, err)
	}
	replayed, err := (workflowruntime.WaitCoordinator{Store: reopened}).Resume(ctx, command)
	if err != nil || replayed.Outcome != workflowruntime.ResumeReplayed || replayed.Values != resumed.Values {
		t.Fatalf("Resume replay after restart = (%+v, %v)", replayed, err)
	}
	if _, transitionErr := reopened.TransitionRun(ctx, workflowruntime.RunTransitionRequest{
		RunID: run.ID, ExpectedGeneration: waitingRun.Snapshot.Generation,
		To: workflowruntime.RunRunning, At: fixture.base.Add(7 * time.Second),
	}); transitionErr != nil {
		t.Fatalf("TransitionRun waiting->running: %v", transitionErr)
	}
	if queryErr := reopenedStore.DB.QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, run.ID).Scan(&productRunStatus); queryErr != nil {
		t.Fatal(queryErr)
	}
	if productRunStatus != "running" {
		t.Fatalf("resumed product run status = %q", productRunStatus)
	}
}

func TestWorkflowStateEventsAreConcurrentOrderedAndAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	firstStore, first := openWorkflowStateTest(t, path)
	secondStore, second := openWorkflowStateTest(t, path)
	ctx := context.Background()
	run := createWorkflowTestRun(t, first, "run-events", workflowTestTime())
	stores := []*WorkflowStateStore{first, second}
	const total = 20
	errs := make(chan error, total)
	var group sync.WaitGroup
	for index := 0; index < total; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, err := stores[index%len(stores)].AppendEvent(ctx, workflowruntime.AppendEventRequest{
				RunID: run.ID, Type: fmt.Sprintf("test.%02d", index),
				OccurredAt: workflowTestTime().Add(time.Duration(index) * time.Millisecond),
				Redaction:  values.RedactionPrivate, Retention: values.RetentionRun,
			})
			errs <- err
		}(index)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendEvent concurrent: %v", err)
		}
	}
	events, err := first.ListEvents(ctx, workflowruntime.EventQuery{RunID: run.ID})
	if err != nil || len(events) != total {
		t.Fatalf("ListEvents = %d events, %v", len(events), err)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event[%d].Sequence = %d", index, event.Sequence)
		}
	}
	if _, updateErr := firstStore.DB.ExecContext(ctx, `UPDATE workflow_events SET event_type='mutated' WHERE run_id=?`, run.ID); updateErr == nil {
		t.Fatal("workflow event update unexpectedly succeeded")
	}
	if _, deleteErr := secondStore.DB.ExecContext(ctx, `DELETE FROM workflow_events WHERE run_id=?`, run.ID); deleteErr == nil {
		t.Fatal("workflow event delete unexpectedly succeeded")
	}
}

func openWorkflowStateTest(t *testing.T, path string) (*nanitestore.Store, *WorkflowStateStore) {
	t.Helper()
	store, err := nanitestore.New(context.Background(), path)
	if err != nil {
		t.Fatalf("store.New(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	state, err := NewWorkflowStateStore(store)
	if err != nil {
		t.Fatalf("NewWorkflowStateStore: %v", err)
	}
	return store, state
}

func workflowTestPlan(suffix string) workflowruntime.PlanRef {
	return workflowruntime.PlanRef{
		ID: "plan-" + suffix, Version: "v1", SchemaVersion: "workflow.execution-plan/v1",
		Digest: values.SHA256Digest([]byte("plan-" + suffix)),
	}
}

func workflowTestTime() time.Time {
	return time.Date(2026, time.September, 4, 12, 0, 0, 123456789, time.UTC)
}

func createWorkflowTestRun(t *testing.T, state *WorkflowStateStore, id string, at time.Time) workflowruntime.RunSnapshot {
	t.Helper()
	run, outcome, err := state.CreateRun(context.Background(), workflowruntime.CreateRunRequest{
		ID: workflowruntime.RunID(id), Plan: workflowTestPlan(id), Status: workflowruntime.RunPending,
		StartIdempotencyKey: "start-" + id, CreatedAt: at,
	})
	if err != nil || outcome != workflowruntime.IdempotencyApplied {
		t.Fatalf("CreateRun(%s) = (%+v, %q, %v)", id, run, outcome, err)
	}
	return run
}

func createWorkflowTestNode(t *testing.T, state *WorkflowStateStore, runID workflowruntime.RunID, nodeID string, at time.Time) workflowruntime.NodeInvocationSnapshot {
	t.Helper()
	node, err := state.CreateNodeInvocation(context.Background(), workflowruntime.CreateNodeInvocationRequest{Snapshot: workflowruntime.NodeInvocationSnapshot{
		ID:     workflowruntime.NodeInvocationID{RunID: runID, NodeID: nodeID},
		Status: workflowruntime.NodePending, CreatedAt: at, UpdatedAt: at,
	}})
	if err != nil {
		t.Fatalf("CreateNodeInvocation(%s): %v", nodeID, err)
	}
	return node
}

type workflowSQLiteWaitFixture struct {
	base        time.Time
	waitID      workflowruntime.WaitID
	invocation  workflowruntime.NodeInvocationID
	token       string
	source      workflowwait.WakeSource
	correlation string
	request     workflowruntime.SuspendNodeWaitRequest
}

func prepareWorkflowSQLiteWait(t *testing.T, state *WorkflowStateStore, suffix string, base time.Time, timeout time.Duration) workflowSQLiteWaitFixture {
	t.Helper()
	run := createWorkflowTestRun(t, state, "run-wait-"+suffix, base)
	running, err := state.TransitionRun(context.Background(), workflowruntime.RunTransitionRequest{
		RunID: run.ID, ExpectedGeneration: run.Generation, To: workflowruntime.RunRunning, At: base,
	})
	if err != nil || running.Snapshot.Status != workflowruntime.RunRunning {
		t.Fatalf("TransitionRun wait fixture = (%+v, %v)", running, err)
	}
	if projectionErr := state.RecordPlanNodeProjections(context.Background(), run.Plan, []PlanNodeProjection{{
		NodeID: "wait-node", ProductStepID: "wait-step", ProductKind: "gate", WaitClass: "gate",
	}}); projectionErr != nil {
		t.Fatalf("RecordPlanNodeProjections wait fixture: %v", projectionErr)
	}
	node := createWorkflowTestNode(t, state, run.ID, "wait-node", base)
	ready, err := state.TransitionNode(context.Background(), workflowruntime.NodeTransitionRequest{
		InvocationID: node.ID, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := state.ClaimNode(context.Background(), workflowruntime.ClaimNodeRequest{
		InvocationID: node.ID, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration,
		Owner: "worker", Token: "claim-" + suffix, IdempotencyKey: "claim-key-" + suffix,
		Now: base.Add(time.Second), LeaseUntil: base.Add(time.Hour),
	})
	if err != nil || !claim.Acquired {
		t.Fatalf("ClaimNode = %+v, %v", claim, err)
	}
	claimed, err := state.LoadNodeInvocation(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	started, err := state.StartNodeAttempt(context.Background(), workflowruntime.StartNodeAttemptRequest{
		InvocationID: node.ID, ExpectedNodeGeneration: claimed.Generation, Claim: proof,
		Executor: workflowruntime.ExecutorMetadata{Kind: "test", Version: "v1"}, At: base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := workflowwait.NewSchemaRef(graph.Schema{"type": "string"})
	if err != nil {
		t.Fatal(err)
	}
	token := "resume-" + suffix
	digest, err := workflowwait.DigestToken(token)
	if err != nil {
		t.Fatal(err)
	}
	waitID := workflowruntime.WaitID("wait-" + suffix)
	correlation := "correlation-" + suffix
	record := workflowwait.Record{
		Kind: workflowwait.KindCallback, Correlation: correlation, Deadline: base.Add(timeout),
		ResumeSchema: schema, ResumeTokenDigest: digest, ResumeURL: "https://example.test/waits/" + suffix,
		Visibility: workflowwait.VisibilityPrivate, Authority: workflowwait.ResponderAuthority{Kind: "test"},
		WakeSource: workflowwait.WakeCallback, Status: workflowruntime.WaitOpen,
	}
	request := workflowruntime.SuspendNodeWaitRequest{
		Wait:                   workflowruntime.WaitSnapshot{Ref: workflowruntime.WaitRef{ID: waitID}, Invocation: node.ID, Record: record},
		ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation,
		Claim: proof, At: base.Add(3 * time.Second),
	}
	return workflowSQLiteWaitFixture{
		base: base, waitID: waitID, invocation: node.ID, token: token,
		source: record.WakeSource, correlation: correlation, request: request,
	}
}

func (f workflowSQLiteWaitFixture) resumeCommand(t *testing.T, key string, at time.Time) workflowruntime.ResumeCommand {
	t.Helper()
	value, err := values.NewInline("accepted", values.Metadata{
		Producer:  values.Producer{Kind: "wait_response", Reference: string(f.waitID)},
		MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workflowruntime.ResumeCommand{
		WaitID: f.waitID, Correlation: f.correlation, Token: f.token, WakeSource: f.source,
		Responder: workflowwait.Responder{Kind: "test", Reference: "responder"}, Payload: value,
		IdempotencyKey: key, ReceivedAt: at,
	}
}
