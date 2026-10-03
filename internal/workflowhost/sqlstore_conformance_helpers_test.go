package workflowhost

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type releasedTestDB struct{ store *nanitestore.Store }

func (d *releasedTestDB) Close() error { return d.store.Close(context.Background()) }
func releasedOpenStore(t *testing.T, path string) (*releasedTestDB, *SQLWorkflowStateStore, error) {
	st, err := storetest.New(t, context.Background(), path)
	if err != nil {
		return nil, nil, err
	}
	shared, err := NewSQLWorkflowStateStore(st)
	if err != nil {
		_ = st.Close(context.Background())
		return nil, nil, err
	}
	return &releasedTestDB{st}, shared, nil
}

func releasedCreateWorkflowTestRun(t *testing.T, state *SQLWorkflowStateStore, id string, at time.Time) workflowruntime.RunSnapshot {
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

func releasedCreateWorkflowTestNode(t *testing.T, state *SQLWorkflowStateStore, runID workflowruntime.RunID, nodeID string, at time.Time) workflowruntime.NodeInvocationSnapshot {
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

func releasedSeedSQLiteCompensationEligibility(t *testing.T, state *SQLWorkflowStateStore, run workflowruntime.RunSnapshot, source, handler string, at time.Time) workflowruntime.CompensationEntrySnapshot {
	t.Helper()
	entry, _ := releasedSeedSQLiteCompensationEligibilityRequest(t, state, run, source, handler, at, "")
	return entry
}

func releasedSeedSQLiteCompensationEligibilityRequest(t *testing.T, state *SQLWorkflowStateStore, run workflowruntime.RunSnapshot, source, handler string, at time.Time, child workflowruntime.RunID) (workflowruntime.CompensationEntrySnapshot, workflowruntime.FinishCompensableAttemptRequest) {
	t.Helper()
	node := releasedCreateWorkflowTestNode(t, state, run.ID, source, at)
	ready, err := state.TransitionNode(t.Context(), workflowruntime.NodeTransitionRequest{InvocationID: node.ID, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: at})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := state.ClaimNode(t.Context(), workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration, Owner: "forward-" + source, Token: "forward-token-" + source, IdempotencyKey: "forward-claim-" + source, Now: at.Add(time.Second), LeaseUntil: at.Add(time.Hour)})
	if err != nil || claimed.Lease == nil {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	proof := workflowruntime.ClaimProof{Owner: claimed.Lease.Owner, Token: claimed.Lease.Token, Generation: claimed.Lease.Generation}
	current, err := state.LoadNodeInvocation(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	started, err := state.StartNodeAttempt(t.Context(), workflowruntime.StartNodeAttemptRequest{InvocationID: node.ID, ExpectedNodeGeneration: current.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "effect", Version: "v1"}, At: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	evidence := stepkind.ReversibilityEvidence{Operation: "fixture.effect", ReceiptSchema: graph.Schema{}}
	if child != "" {
		if _, loadErr := state.LoadRun(t.Context(), child); errors.Is(loadErr, workflowruntime.ErrNotFound) {
			releasedCreateWorkflowTestRun(t, state, string(child), at.Add(-time.Second))
		} else if loadErr != nil {
			t.Fatal(loadErr)
		}
		if recordErr := state.RecordChildRun(t.Context(), workflowruntime.ChildRunLink{ParentRunID: run.ID, Invocation: node.ID, ChildRunID: child, Policy: graph.ParentCloseCancel, CreatedAt: at}); recordErr != nil {
			t.Fatal(recordErr)
		}
	}
	request := workflowruntime.FinishCompensableAttemptRequest{Finish: workflowruntime.FinishNodeAttemptRequest{InvocationID: node.ID, AttemptNumber: started.Attempt.ID.Number, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: workflowruntime.NodeSucceeded, NextNodeStatus: workflowruntime.NodeSucceeded, At: at.Add(2 * time.Second)}, Eligibility: workflowruntime.CompensationEligibility{PlanDigest: run.Plan.Digest, HandlerNodeID: handler, Evidence: evidence, Receipt: stepkind.CompensationReceipt{Operation: evidence.Operation, Values: values.ValueSet{}, ChildRunID: string(child)}, ChildRunID: child}}
	finished, err := state.FinishCompensableAttempt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return finished.Entry, request
}

func TestReleasedWorkflowStoreLeaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	db, shared, err := releasedOpenStore(t, path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 2, 12, 0, 0, 123456789, time.UTC)
	run := releasedCreateWorkflowTestRun(t, shared, "historical-run", at)
	node := releasedCreateWorkflowTestNode(t, shared, run.ID, "step", at)
	ready, err := shared.TransitionNode(t.Context(), workflowruntime.NodeTransitionRequest{InvocationID: node.ID, ExpectedGeneration: node.Generation, To: workflowruntime.NodeReady, At: at})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := shared.ClaimNode(t.Context(), workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration, Owner: "owner", Token: "persisted-token", IdempotencyKey: "persisted-claim", Now: at, LeaseUntil: at.Add(time.Minute)})
	if err != nil || !claim.Acquired || claim.Lease == nil {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	// Read the shared writer's state through CURRENT Nanite before closing.
	current, err := NewWorkflowStateStore(db.store)
	if err != nil {
		t.Fatal(err)
	}
	before, err := current.LoadNodeInvocation(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, next, err := releasedOpenStore(t, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	after, err := next.LoadNodeInvocation(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("lease/state changed on adapter reopen: before=%+v after=%+v", before, after)
	}
	replay, err := next.ClaimNode(t.Context(), workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: ready.Snapshot.ClaimGeneration, Owner: "owner", Token: "persisted-token", IdempotencyKey: "persisted-claim", Now: at, LeaseUntil: at.Add(time.Minute)})
	if err != nil || !replay.Replayed {
		t.Fatalf("claim replay: %+v %v", replay, err)
	}
	blocked, err := next.ClaimNode(t.Context(), workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: after.ClaimGeneration, Owner: "other", Token: "other-token", IdempotencyKey: "live-lease-probe", Now: at.Add(30 * time.Second), LeaseUntil: at.Add(2 * time.Minute)})
	if err != nil || blocked.Acquired {
		t.Fatalf("live lease not preserved: %+v %v", blocked, err)
	}
	recovered, err := next.ClaimNode(t.Context(), workflowruntime.ClaimNodeRequest{InvocationID: node.ID, ExpectedClaimGeneration: after.ClaimGeneration, Owner: "recovered", Token: "new-token", IdempotencyKey: "recovery-claim", Now: at.Add(time.Minute), LeaseUntil: at.Add(2 * time.Minute)})
	if err != nil || !recovered.Acquired || recovered.Lease == nil {
		t.Fatalf("recover: %+v %v", recovered, err)
	}
	if err := next.ReleaseNodeClaim(t.Context(), workflowruntime.ReleaseClaimRequest{InvocationID: node.ID, Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation, Now: at.Add(time.Minute)}); !errors.Is(err, workflowruntime.ErrClaimMismatch) {
		t.Fatalf("old owner not fenced: %v", err)
	}
	if recovered.Lease.Generation <= claim.Lease.Generation {
		t.Fatal("claim generation did not fence prior owner")
	}
}
