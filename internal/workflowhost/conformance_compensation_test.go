package workflowhost

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	workflowcompile "github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/conformance"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"

	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

type naniteQualificationPlans struct{ graph graph.Graph }

func (s naniteQualificationPlans) LoadRecoveryPlan(_ context.Context, run workflowruntime.RunSnapshot) (workflowruntime.RecoveryPlan, error) {
	plan := workflowcompile.ExecutionPlan{SchemaVersion: run.Plan.SchemaVersion, ID: run.Plan.ID, Digest: run.Plan.Digest, Graph: s.graph}
	inferred := workflowcompile.InferValueDependencies(&plan, workflowcompile.DependencyOptions{})
	return workflowruntime.RecoveryPlan{Ref: run.Plan, Plan: plan, Visibility: inferred.Visibility}, nil
}

func runNaniteCompensationFixture(ctx context.Context, fixture conformance.Fixture, state *WorkflowStateStore) error {
	var input struct {
		Scenario string `json:"scenario"`
	}
	if err := json.Unmarshal(fixture.Input, &input); err != nil {
		return err
	}
	if input.Scenario == "unsupported_claim" {
		return runNaniteUnsupportedCompensation(ctx, state)
	}
	if input.Scenario == "crash_recovery" {
		return runNaniteCompensationRecovery(ctx, state)
	}
	return runNaniteCompensationScenario(ctx, state, input.Scenario)
}

func runNaniteCompensationScenario(ctx context.Context, state *WorkflowStateStore, scenario string) error {
	base := qualificationTime()
	runID := workflowruntime.RunID("comp-" + scenario)
	run, err := createQualificationRun(ctx, state, runID, base)
	if err != nil {
		return err
	}
	sources := []string{"effect"}
	if scenario == "reverse_order" || scenario == "independent_parallel" || scenario == "partial" || scenario == "failed" {
		sources = []string{"first", "second"}
	}
	var childID workflowruntime.RunID
	if scenario == "nested_child" {
		childID = "comp-child"
		child, childErr := createQualificationRun(ctx, state, childID, base)
		if childErr != nil {
			return childErr
		}
		if _, childErr = state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: childID, ExpectedGeneration: child.Generation, To: workflowruntime.RunSucceeded, At: base.Add(time.Second)}); childErr != nil {
			return childErr
		}
	}
	entries := make([]workflowruntime.CompensationEntrySnapshot, 0, len(sources))
	requests := make([]workflowruntime.FinishCompensableAttemptRequest, 0, len(sources))
	nodes := make([]graph.Node, 0, len(sources)*2)
	for index, source := range sources {
		handler := "undo-" + source
		entry, request, seedErr := seedNaniteCompensation(ctx, state, run, source, handler, base.Add(time.Duration(2+index*3)*time.Second), childID)
		if seedErr != nil {
			return seedErr
		}
		entries = append(entries, entry)
		requests = append(requests, request)
		nodes = append(nodes, graph.Node{ID: source, Compensation: &graph.CompensationSpec{Handler: handler}}, graph.Node{ID: handler})
	}
	current, err := state.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	terminal, err := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: runID, ExpectedGeneration: current.Generation, To: workflowruntime.RunSucceeded, At: base.Add(10 * time.Second)})
	if err != nil {
		return err
	}
	var dependencies map[string][]string
	if scenario == "reverse_order" {
		dependencies = map[string][]string{"first": {"second"}}
	}
	manualRequest := workflowruntime.BeginManualCompensationRequest{RunID: runID, PlanDigest: run.Plan.Digest, ExpectedRunGeneration: terminal.Snapshot.Generation, OriginalStatus: workflowruntime.RunSucceeded, Dependencies: dependencies, IdempotencyKey: "manual-" + scenario, Authorization: values.SHA256Digest([]byte("authorize-" + scenario)), At: base.Add(11 * time.Second)}
	manual, err := state.BeginManualCompensation(ctx, manualRequest)
	if err != nil || manual.Ledger.Status != workflowruntime.CompensationFrozen {
		return fmt.Errorf("begin manual compensation = %#v: %w", manual, err)
	}
	workflow := graph.Graph{ID: run.Plan.ID, Version: run.Plan.Version, Compensation: &graph.CompensationPolicy{Triggers: []graph.CompensationTrigger{graph.CompensationManual}}, Nodes: nodes}
	coordinator := workflowruntime.CompensationCoordinator{Store: state, Compensation: state, Plans: naniteQualificationPlans{graph: workflow}}

	switch scenario {
	case "reverse_order":
		progress, progressErr := coordinator.Progress(ctx, runID, base.Add(12*time.Second))
		if progressErr != nil || len(progress.Activated) != 1 || progress.Activated[0].Entry.Source.NodeID != "second" {
			return fmt.Errorf("reverse first activation = %#v: %w", progress, progressErr)
		}
		if finishErr := finishNaniteCompensationHandler(ctx, state, progress.Activated[0].Node.ID, workflowruntime.NodeSucceeded, base.Add(13*time.Second)); finishErr != nil {
			return finishErr
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(15*time.Second))
		if progressErr != nil || len(progress.Activated) != 1 || progress.Activated[0].Entry.Source.NodeID != "first" {
			return fmt.Errorf("reverse second activation = %#v: %w", progress, progressErr)
		}
		if finishErr := finishNaniteCompensationHandler(ctx, state, progress.Activated[0].Node.ID, workflowruntime.NodeSucceeded, base.Add(16*time.Second)); finishErr != nil {
			return finishErr
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(18*time.Second))
		if progressErr != nil || progress.Ledger.Outcome != workflowruntime.CompensationOutcomeSucceeded {
			return fmt.Errorf("reverse completion = %#v: %w", progress, progressErr)
		}
		return nil
	case "independent_parallel", "partial", "failed":
		progress, progressErr := coordinator.Progress(ctx, runID, base.Add(12*time.Second))
		if progressErr != nil || len(progress.Activated) != 2 {
			return fmt.Errorf("parallel activation = %#v: %w", progress, progressErr)
		}
		for index, activated := range progress.Activated {
			status := workflowruntime.NodeSucceeded
			if scenario == "failed" || scenario == "partial" && index == 1 {
				status = workflowruntime.NodeFailed
			}
			if finishErr := finishNaniteCompensationHandler(ctx, state, activated.Node.ID, status, base.Add(time.Duration(13+index*2)*time.Second)); finishErr != nil {
				return finishErr
			}
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(18*time.Second))
		var want workflowruntime.CompensationOutcome
		switch scenario {
		case "partial":
			want = workflowruntime.CompensationOutcomePartial
		case "failed":
			want = workflowruntime.CompensationOutcomeFailed
		default:
			want = workflowruntime.CompensationOutcomeSucceeded
		}
		if progressErr != nil || progress.Ledger.Outcome != want || len(progress.Sealed) != 2 {
			return fmt.Errorf("parallel completion = %#v, want %q: %w", progress, want, progressErr)
		}
		return nil
	case "separate_cancel":
		canceled, cancelErr := state.CancelCompensation(ctx, workflowruntime.CancelCompensationRequest{RunID: runID, ExpectedLedgerGeneration: manual.Ledger.Generation, IdempotencyKey: "cancel-compensation", Reason: "operator canceled rollback", At: base.Add(12 * time.Second)})
		if cancelErr != nil || canceled.Outcome != workflowruntime.CompensationOutcomeCanceled || canceled.Status != workflowruntime.CompensationTerminal {
			return fmt.Errorf("cancel compensation = %#v: %w", canceled, cancelErr)
		}
		unchanged, loadErr := state.LoadRun(ctx, runID)
		if loadErr != nil || unchanged.Status != workflowruntime.RunSucceeded || unchanged.Generation != terminal.Snapshot.Generation {
			return fmt.Errorf("cancel mutated original run = %#v: %w", unchanged, loadErr)
		}
		return nil
	case "stable_retry":
		progress, progressErr := coordinator.Progress(ctx, runID, base.Add(12*time.Second))
		if progressErr != nil || len(progress.Activated) != 1 {
			return fmt.Errorf("retry activation = %#v: %w", progress, progressErr)
		}
		firstHandler := progress.Activated[0].Node.ID
		if finishErr := finishNaniteCompensationHandler(ctx, state, firstHandler, workflowruntime.NodeFailed, base.Add(13*time.Second)); finishErr != nil {
			return finishErr
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(15*time.Second))
		if progressErr != nil || progress.Ledger.Outcome != workflowruntime.CompensationOutcomeFailed {
			return fmt.Errorf("retry failed cycle = %#v: %w", progress, progressErr)
		}
		retried, retryErr := state.RetryCompensation(ctx, workflowruntime.RetryCompensationRequest{RunID: runID, ExpectedLedgerGeneration: progress.Ledger.Generation, IdempotencyKey: "retry-compensation", Attestation: values.SHA256Digest([]byte("retry-attestation")), At: base.Add(16 * time.Second)})
		if retryErr != nil || len(retried.Cycles) != 2 {
			return fmt.Errorf("retry ledger = %#v: %w", retried, retryErr)
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(17*time.Second))
		if progressErr != nil || len(progress.Activated) != 1 || progress.Activated[0].Node.ID == firstHandler || len(progress.Activated[0].Entry.History) != 1 {
			return fmt.Errorf("retry second activation = %#v: %w", progress, progressErr)
		}
		if finishErr := finishNaniteCompensationHandler(ctx, state, progress.Activated[0].Node.ID, workflowruntime.NodeSucceeded, base.Add(18*time.Second)); finishErr != nil {
			return finishErr
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(20*time.Second))
		if progressErr != nil || progress.Ledger.Outcome != workflowruntime.CompensationOutcomeSucceeded || len(progress.Ledger.Cycles) != 2 {
			return fmt.Errorf("retry completion = %#v: %w", progress, progressErr)
		}
		return nil
	case "nested_child":
		progress, progressErr := coordinator.Progress(ctx, runID, base.Add(12*time.Second))
		if progressErr != nil || len(progress.Activated) != 1 || progress.Activated[0].Entry.ChildResolution != workflowruntime.CompensationChildNoLedger {
			return fmt.Errorf("nested activation = %#v: %w", progress, progressErr)
		}
		if finishErr := finishNaniteCompensationHandler(ctx, state, progress.Activated[0].Node.ID, workflowruntime.NodeSucceeded, base.Add(13*time.Second)); finishErr != nil {
			return finishErr
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(15*time.Second))
		if progressErr != nil || progress.Ledger.Outcome != workflowruntime.CompensationOutcomeSucceeded {
			return fmt.Errorf("nested completion = %#v: %w", progress, progressErr)
		}
		return nil
	case "replay":
		replayed, replayErr := state.BeginManualCompensation(ctx, manualRequest)
		if replayErr != nil || replayed.Outcome != workflowruntime.IdempotencyReplayed {
			return fmt.Errorf("manual replay = %#v: %w", replayed, replayErr)
		}
		forwardReplay, forwardReplayErr := state.FinishCompensableAttempt(ctx, requests[0])
		if forwardReplayErr != nil || forwardReplay.Entry.ID != entries[0].ID {
			return fmt.Errorf("forward replay = %#v: %w", forwardReplay, forwardReplayErr)
		}
		progress, progressErr := coordinator.Progress(ctx, runID, base.Add(12*time.Second))
		if progressErr != nil || len(progress.Activated) != 1 {
			return fmt.Errorf("replay activation = %#v: %w", progress, progressErr)
		}
		if finishErr := finishNaniteCompensationHandler(ctx, state, progress.Activated[0].Node.ID, workflowruntime.NodeSucceeded, base.Add(13*time.Second)); finishErr != nil {
			return finishErr
		}
		progress, progressErr = coordinator.Progress(ctx, runID, base.Add(15*time.Second))
		if progressErr != nil || progress.Ledger.Outcome != workflowruntime.CompensationOutcomeSucceeded {
			return fmt.Errorf("replay completion = %#v: %w", progress, progressErr)
		}
		return nil
	default:
		return fmt.Errorf("unknown compensation scenario %q", scenario)
	}
}

func runNaniteUnsupportedCompensation(ctx context.Context, state *WorkflowStateStore) error {
	base := qualificationTime()
	run, err := createQualificationRun(ctx, state, "comp-unsupported", base)
	if err != nil {
		return err
	}
	_, request, err := beginNaniteCompensationAttempt(ctx, state, run, "effect", base.Add(2*time.Second))
	if err != nil {
		return err
	}
	request.Eligibility = workflowruntime.CompensationEligibility{PlanDigest: run.Plan.Digest, HandlerNodeID: "undo", Evidence: stepkind.ReversibilityEvidence{}, Receipt: stepkind.CompensationReceipt{}}
	_, err = state.FinishCompensableAttempt(ctx, request)
	return err
}

func runNaniteCompensationRecovery(ctx context.Context, state *WorkflowStateStore) error {
	base := qualificationTime()
	run, err := createQualificationRun(ctx, state, "comp-recovery", base)
	if err != nil {
		return err
	}
	if _, _, err = seedNaniteCompensation(ctx, state, run, "effect", "undo-effect", base.Add(2*time.Second), ""); err != nil {
		return err
	}
	current, err := state.LoadRun(ctx, run.ID)
	if err != nil {
		return err
	}
	terminal, err := state.TransitionRun(ctx, workflowruntime.RunTransitionRequest{RunID: run.ID, ExpectedGeneration: current.Generation, To: workflowruntime.RunSucceeded, At: base.Add(6 * time.Second)})
	if err != nil {
		return err
	}
	if _, err = state.BeginManualCompensation(ctx, workflowruntime.BeginManualCompensationRequest{RunID: run.ID, PlanDigest: run.Plan.Digest, ExpectedRunGeneration: terminal.Snapshot.Generation, OriginalStatus: workflowruntime.RunSucceeded, IdempotencyKey: "recovery-manual", Authorization: values.SHA256Digest([]byte("recovery-authorization")), At: base.Add(7 * time.Second)}); err != nil {
		return err
	}
	var sequence int
	var name, path string
	if queryErr := state.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&sequence, &name, &path); queryErr != nil {
		return queryErr
	}
	if closeErr := state.db.Close(); closeErr != nil {
		return closeErr
	}
	reopenedStore, err := nanitestore.New(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = reopenedStore.Close(context.Background()) }()
	reopened, err := NewWorkflowStateStore(reopenedStore)
	if err != nil {
		return err
	}
	recovered, err := reopened.RecoverCompensation(ctx, 10)
	if err != nil || len(recovered) != 1 || recovered[0].RunID != run.ID {
		return fmt.Errorf("recover compensation = %#v: %w", recovered, err)
	}
	workflow := graph.Graph{ID: run.Plan.ID, Version: run.Plan.Version, Compensation: &graph.CompensationPolicy{Triggers: []graph.CompensationTrigger{graph.CompensationManual}}, Nodes: []graph.Node{{ID: "effect", Compensation: &graph.CompensationSpec{Handler: "undo-effect"}}, {ID: "undo-effect"}}}
	coordinator := workflowruntime.CompensationCoordinator{Store: reopened, Compensation: reopened, Plans: naniteQualificationPlans{graph: workflow}}
	progress, err := coordinator.Progress(ctx, run.ID, base.Add(8*time.Second))
	if err != nil || len(progress.Activated) != 1 {
		return fmt.Errorf("recovered activation = %#v: %w", progress, err)
	}
	if finishErr := finishNaniteCompensationHandler(ctx, reopened, progress.Activated[0].Node.ID, workflowruntime.NodeSucceeded, base.Add(9*time.Second)); finishErr != nil {
		return finishErr
	}
	progress, err = coordinator.Progress(ctx, run.ID, base.Add(11*time.Second))
	if err != nil || progress.Ledger.Outcome != workflowruntime.CompensationOutcomeSucceeded {
		return fmt.Errorf("recovered completion = %#v: %w", progress, err)
	}
	return nil
}

func beginNaniteCompensationAttempt(ctx context.Context, state *WorkflowStateStore, run workflowruntime.RunSnapshot, source string, at time.Time) (workflowruntime.StartNodeAttemptResult, workflowruntime.FinishCompensableAttemptRequest, error) {
	id := workflowruntime.NodeInvocationID{RunID: run.ID, NodeID: source}
	node, err := createQualificationNode(ctx, state, id, at, true)
	if err != nil {
		return workflowruntime.StartNodeAttemptResult{}, workflowruntime.FinishCompensableAttemptRequest{}, err
	}
	claim, err := state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: id, ExpectedClaimGeneration: node.ClaimGeneration, Owner: "forward-" + source, Token: "token-" + source, IdempotencyKey: "claim-" + source, Now: at.Add(time.Second), LeaseUntil: at.Add(time.Hour)})
	if err != nil || claim.Lease == nil {
		return workflowruntime.StartNodeAttemptResult{}, workflowruntime.FinishCompensableAttemptRequest{}, fmt.Errorf("claim compensable node: %w", err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	claimed, err := state.LoadNodeInvocation(ctx, id)
	if err != nil {
		return workflowruntime.StartNodeAttemptResult{}, workflowruntime.FinishCompensableAttemptRequest{}, err
	}
	started, err := state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: id, ExpectedNodeGeneration: claimed.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "effect", Version: "v1"}, At: at.Add(time.Second)})
	request := workflowruntime.FinishCompensableAttemptRequest{Finish: workflowruntime.FinishNodeAttemptRequest{InvocationID: id, AttemptNumber: started.Attempt.ID.Number, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: workflowruntime.NodeSucceeded, NextNodeStatus: workflowruntime.NodeSucceeded, At: at.Add(2 * time.Second)}}
	return started, request, err
}

func seedNaniteCompensation(ctx context.Context, state *WorkflowStateStore, run workflowruntime.RunSnapshot, source, handler string, at time.Time, childID workflowruntime.RunID) (workflowruntime.CompensationEntrySnapshot, workflowruntime.FinishCompensableAttemptRequest, error) {
	_, request, err := beginNaniteCompensationAttempt(ctx, state, run, source, at)
	if err != nil {
		return workflowruntime.CompensationEntrySnapshot{}, request, err
	}
	if childID != "" {
		if recordErr := state.RecordChildRun(ctx, workflowruntime.ChildRunLink{ParentRunID: run.ID, Invocation: request.Finish.InvocationID, ChildRunID: childID, Policy: graph.ParentCloseCancel, CreatedAt: at}); recordErr != nil {
			return workflowruntime.CompensationEntrySnapshot{}, request, recordErr
		}
	}
	evidence := stepkind.ReversibilityEvidence{Operation: "fixture." + source, ReceiptSchema: graph.Schema{}}
	request.Eligibility = workflowruntime.CompensationEligibility{PlanDigest: run.Plan.Digest, HandlerNodeID: handler, Evidence: evidence, Receipt: stepkind.CompensationReceipt{Operation: evidence.Operation, Values: values.ValueSet{}, ChildRunID: string(childID)}, ChildRunID: childID}
	finished, err := state.FinishCompensableAttempt(ctx, request)
	return finished.Entry, request, err
}

func finishNaniteCompensationHandler(ctx context.Context, state *WorkflowStateStore, id workflowruntime.NodeInvocationID, status workflowruntime.NodeStatus, at time.Time) error {
	node, err := state.LoadNodeInvocation(ctx, id)
	if err != nil {
		return err
	}
	claim, err := state.ClaimNode(ctx, workflowruntime.ClaimNodeRequest{InvocationID: id, ExpectedClaimGeneration: node.ClaimGeneration, Owner: "rollback-" + id.Iteration, Token: "token-" + id.Iteration, IdempotencyKey: "claim-" + id.Iteration, Now: at, LeaseUntil: at.Add(time.Hour)})
	if err != nil || claim.Lease == nil {
		return fmt.Errorf("claim compensation handler: %w", err)
	}
	proof := workflowruntime.ClaimProof{Owner: claim.Lease.Owner, Token: claim.Lease.Token, Generation: claim.Lease.Generation}
	claimed, err := state.LoadNodeInvocation(ctx, id)
	if err != nil {
		return err
	}
	started, err := state.StartNodeAttempt(ctx, workflowruntime.StartNodeAttemptRequest{InvocationID: id, ExpectedNodeGeneration: claimed.Generation, Claim: proof, Executor: workflowruntime.ExecutorMetadata{Kind: "undo", Version: "v1"}, Inputs: claimed.Inputs, At: at})
	if err != nil {
		return err
	}
	var failure *workflowruntime.Failure
	if status != workflowruntime.NodeSucceeded {
		failure = &workflowruntime.Failure{Code: "rollback_failed", Message: "rollback failed"}
	}
	_, err = state.FinishNodeAttempt(ctx, workflowruntime.FinishNodeAttemptRequest{InvocationID: id, AttemptNumber: started.Attempt.ID.Number, ExpectedNodeGeneration: started.Node.Generation, ExpectedAttemptGeneration: started.Attempt.Generation, Claim: proof, AttemptStatus: status, NextNodeStatus: status, Failure: failure, At: at.Add(time.Second)})
	return err
}
