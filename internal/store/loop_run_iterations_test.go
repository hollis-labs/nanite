package store

import (
	"context"
	"errors"
	"testing"
)

// makeTestLoopRun (loop_run_iterations.loop_run_id is FK-enforced, so every
// LoopRunIteration test needs a real loop_runs row to point at) inserts a
// minimal loop_runs row via a fresh goals row and returns it.
func makeTestLoopRun(t *testing.T, s *Store) *LoopRun {
	t.Helper()
	goal := makeTestGoal(t, s, "goal for loop_run_iterations tests")
	lr := &LoopRun{GoalID: goal.ID, DefinitionName: "implementation_iteration"}
	if err := s.CreateLoopRun(context.Background(), lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	return lr
}

// TestLoopRunIteration_RoundTrip exercises the exact shape the design doc's
// own illustrative example shows: create an in-flight row (decision/
// progress_state/workflow_run_id all null), then complete it, per this
// task's "Done means".
func TestLoopRunIteration_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loopRun := makeTestLoopRun(t, s)

	li := &LoopRunIteration{
		LoopRunID:       loopRun.ID,
		IterationNumber: 1,
	}
	if err := s.CreateLoopRunIteration(ctx, li); err != nil {
		t.Fatalf("CreateLoopRunIteration: %v", err)
	}
	if li.ID == "" {
		t.Fatal("CreateLoopRunIteration did not populate ID")
	}
	if li.StartedAt == "" {
		t.Fatal("CreateLoopRunIteration did not populate StartedAt")
	}

	inFlight, err := s.GetLoopRunIteration(ctx, loopRun.ID, 1)
	if err != nil {
		t.Fatalf("GetLoopRunIteration: %v", err)
	}
	if inFlight.WorkflowRunID != "" || inFlight.Decision != "" || inFlight.ProgressState != "" {
		t.Errorf("in-flight row should have empty workflow_run_id/decision/progress_state: %+v", inFlight)
	}
	if inFlight.CompletedAt != "" {
		t.Errorf("in-flight row should have empty completed_at: %q", inFlight.CompletedAt)
	}
	eval, err := inFlight.Evaluation()
	if err != nil {
		t.Fatalf("Evaluation (default): %v", err)
	}
	if eval.RemainingDelta != "" || eval.Confidence != 0 || len(eval.Regressions) != 0 {
		t.Errorf("in-flight row's default Evaluation should be zero-value, got %+v", eval)
	}

	// Complete the iteration -- fills in workflow_run_id, decision,
	// progress_state, evaluation_json, completed_at.
	workflowRunID := makeTestWorkflowRun(t, s)
	completedEval := Evaluation{
		RemainingDelta: "2 tests still failing",
		Confidence:     0.8,
		Regressions:    []string{"test_foo_regressed"},
	}
	if err := s.CompleteLoopRunIteration(ctx, li.ID, workflowRunID,
		LoopRunIterationDecisionContinue, LoopRunIterationProgressProgress, completedEval); err != nil {
		t.Fatalf("CompleteLoopRunIteration: %v", err)
	}

	completed, err := s.GetLoopRunIteration(ctx, loopRun.ID, 1)
	if err != nil {
		t.Fatalf("GetLoopRunIteration (after complete): %v", err)
	}
	if completed.WorkflowRunID != workflowRunID {
		t.Errorf("WorkflowRunID = %q, want %q", completed.WorkflowRunID, workflowRunID)
	}
	if completed.Decision != LoopRunIterationDecisionContinue {
		t.Errorf("Decision = %q, want %q", completed.Decision, LoopRunIterationDecisionContinue)
	}
	if completed.ProgressState != LoopRunIterationProgressProgress {
		t.Errorf("ProgressState = %q, want %q", completed.ProgressState, LoopRunIterationProgressProgress)
	}
	if completed.CompletedAt == "" {
		t.Error("CompletedAt not populated after CompleteLoopRunIteration")
	}
	gotEval, err := completed.Evaluation()
	if err != nil {
		t.Fatalf("Evaluation (after complete): %v", err)
	}
	if gotEval.RemainingDelta != completedEval.RemainingDelta ||
		gotEval.Confidence != completedEval.Confidence ||
		len(gotEval.Regressions) != 1 || gotEval.Regressions[0] != "test_foo_regressed" {
		t.Errorf("Evaluation round-trip: got %+v, want %+v", gotEval, completedEval)
	}
}

// TestLoopRunIteration_ListOrderedWithInFlightRow is this task's "Done
// means" regression test: ListLoopRunIterations returns a completed row
// alongside a still-in-flight row, ordered correctly by iteration_number.
func TestLoopRunIteration_ListOrderedWithInFlightRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loopRun := makeTestLoopRun(t, s)

	first := &LoopRunIteration{LoopRunID: loopRun.ID, IterationNumber: 1}
	if err := s.CreateLoopRunIteration(ctx, first); err != nil {
		t.Fatalf("CreateLoopRunIteration (1): %v", err)
	}
	workflowRunID := makeTestWorkflowRun(t, s)
	if err := s.CompleteLoopRunIteration(ctx, first.ID, workflowRunID,
		LoopRunIterationDecisionReplan, LoopRunIterationProgressNoProgress, Evaluation{}); err != nil {
		t.Fatalf("CompleteLoopRunIteration (1): %v", err)
	}

	second := &LoopRunIteration{LoopRunID: loopRun.ID, IterationNumber: 2}
	if err := s.CreateLoopRunIteration(ctx, second); err != nil {
		t.Fatalf("CreateLoopRunIteration (2): %v", err)
	}
	// second is left in-flight -- decision/progress_state/workflow_run_id
	// all null, matching 21-loops.md's own iteration-3 illustrative example.

	list, err := s.ListLoopRunIterations(ctx, loopRun.ID)
	if err != nil {
		t.Fatalf("ListLoopRunIterations: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListLoopRunIterations: got %d rows, want 2", len(list))
	}
	if list[0].IterationNumber != 1 || list[1].IterationNumber != 2 {
		t.Fatalf("ListLoopRunIterations not ordered by iteration_number: got %d, %d", list[0].IterationNumber, list[1].IterationNumber)
	}
	if list[0].Decision != LoopRunIterationDecisionReplan || list[0].ProgressState != LoopRunIterationProgressNoProgress {
		t.Errorf("iteration 1 (completed) unexpected state: %+v", list[0])
	}
	if list[1].Decision != "" || list[1].ProgressState != "" || list[1].WorkflowRunID != "" {
		t.Errorf("iteration 2 (in-flight) should still be null: %+v", list[1])
	}

	// ListLoopRunIterations for an unrelated loop_run returns an empty,
	// non-nil slice.
	other := makeTestLoopRun(t, s)
	empty, err := s.ListLoopRunIterations(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListLoopRunIterations (empty): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("ListLoopRunIterations (no rows): got %+v, want empty non-nil slice", empty)
	}
}

// TestLoopRunIteration_UniqueSeqConstraint is this task's "Done means"
// regression test for idx_loop_run_iterations_seq's uniqueness: a duplicate
// (loop_run_id, iteration_number) insert fails.
func TestLoopRunIteration_UniqueSeqConstraint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loopRun := makeTestLoopRun(t, s)

	if err := s.CreateLoopRunIteration(ctx, &LoopRunIteration{LoopRunID: loopRun.ID, IterationNumber: 1}); err != nil {
		t.Fatalf("CreateLoopRunIteration (first): %v", err)
	}
	err := s.CreateLoopRunIteration(ctx, &LoopRunIteration{LoopRunID: loopRun.ID, IterationNumber: 1})
	if err == nil {
		t.Fatal("CreateLoopRunIteration (duplicate iteration_number): expected unique-index violation, got nil")
	}

	// A different loop_run_id may reuse the same iteration_number -- the
	// unique index is scoped per loop_run_id, not global.
	otherLoopRun := makeTestLoopRun(t, s)
	if err := s.CreateLoopRunIteration(ctx, &LoopRunIteration{LoopRunID: otherLoopRun.ID, IterationNumber: 1}); err != nil {
		t.Fatalf("CreateLoopRunIteration (same iteration_number, different loop_run_id): %v", err)
	}
}

// TestLoopRunIteration_DecisionAndProgressStateValidation confirms
// Create/CompleteLoopRunIteration reject an unknown decision/progress_state
// before it ever reaches the DB's own CHECK constraints.
func TestLoopRunIteration_DecisionAndProgressStateValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loopRun := makeTestLoopRun(t, s)

	if err := s.CreateLoopRunIteration(ctx, &LoopRunIteration{
		LoopRunID: loopRun.ID, IterationNumber: 1, Decision: "bogus",
	}); err == nil {
		t.Error("CreateLoopRunIteration: expected error for invalid decision, got nil")
	}
	if err := s.CreateLoopRunIteration(ctx, &LoopRunIteration{
		LoopRunID: loopRun.ID, IterationNumber: 2, ProgressState: "bogus",
	}); err == nil {
		t.Error("CreateLoopRunIteration: expected error for invalid progress_state, got nil")
	}

	li := &LoopRunIteration{LoopRunID: loopRun.ID, IterationNumber: 3}
	if err := s.CreateLoopRunIteration(ctx, li); err != nil {
		t.Fatalf("CreateLoopRunIteration: %v", err)
	}
	workflowRunID := makeTestWorkflowRun(t, s)
	if err := s.CompleteLoopRunIteration(ctx, li.ID, workflowRunID, "bogus", LoopRunIterationProgressProgress, Evaluation{}); err == nil {
		t.Error("CompleteLoopRunIteration: expected error for invalid decision, got nil")
	}
	if err := s.CompleteLoopRunIteration(ctx, li.ID, workflowRunID, LoopRunIterationDecisionComplete, "bogus", Evaluation{}); err == nil {
		t.Error("CompleteLoopRunIteration: expected error for invalid progress_state, got nil")
	}
	if err := s.CompleteLoopRunIteration(ctx, li.ID, "", LoopRunIterationDecisionComplete, LoopRunIterationProgressGoalMet, Evaluation{}); err == nil {
		t.Error("CompleteLoopRunIteration: expected error for empty workflow_run_id, got nil")
	}
}

// TestLoopRunIteration_NotFound confirms unknown-row lookups/mutations
// return ErrLoopRunIterationNotFound rather than a generic error.
func TestLoopRunIteration_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loopRun := makeTestLoopRun(t, s)

	if _, err := s.GetLoopRunIteration(ctx, loopRun.ID, 99); !errors.Is(err, ErrLoopRunIterationNotFound) {
		t.Errorf("GetLoopRunIteration unknown iteration_number: got %v, want ErrLoopRunIterationNotFound", err)
	}
	if _, err := s.GetLoopRunIteration(ctx, "does-not-exist", 1); !errors.Is(err, ErrLoopRunIterationNotFound) {
		t.Errorf("GetLoopRunIteration unknown loop_run_id: got %v, want ErrLoopRunIterationNotFound", err)
	}

	workflowRunID := makeTestWorkflowRun(t, s)
	err := s.CompleteLoopRunIteration(ctx, "does-not-exist", workflowRunID,
		LoopRunIterationDecisionComplete, LoopRunIterationProgressGoalMet, Evaluation{})
	if !errors.Is(err, ErrLoopRunIterationNotFound) {
		t.Errorf("CompleteLoopRunIteration unknown id: got %v, want ErrLoopRunIterationNotFound", err)
	}
}

// TestLoopRunIteration_LoopRunFKEnforced confirms loop_run_id is a real,
// enforced FK -- an unknown loop_run_id is rejected at the DB layer.
func TestLoopRunIteration_LoopRunFKEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.CreateLoopRunIteration(ctx, &LoopRunIteration{LoopRunID: "does-not-exist", IterationNumber: 1})
	if err == nil {
		t.Fatal("CreateLoopRunIteration with unknown loop_run_id: expected FK violation, got nil")
	}
}

// TestLoopRunIteration_WorkflowRunFKEnforced confirms workflow_run_id is a
// real, enforced FK once set -- CompleteLoopRunIteration with an unknown
// workflow_run_id is rejected at the DB layer.
func TestLoopRunIteration_WorkflowRunFKEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loopRun := makeTestLoopRun(t, s)

	li := &LoopRunIteration{LoopRunID: loopRun.ID, IterationNumber: 1}
	if err := s.CreateLoopRunIteration(ctx, li); err != nil {
		t.Fatalf("CreateLoopRunIteration: %v", err)
	}
	err := s.CompleteLoopRunIteration(ctx, li.ID, "does-not-exist",
		LoopRunIterationDecisionComplete, LoopRunIterationProgressGoalMet, Evaluation{})
	if err == nil {
		t.Fatal("CompleteLoopRunIteration with unknown workflow_run_id: expected FK violation, got nil")
	}
}
