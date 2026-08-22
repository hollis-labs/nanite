package store

// Regression coverage for TASKS/loops/05-workflow-runs-loop-scoping-columns.md's
// schema piece: migration 140_workflow_runs_loop_scoping.sql's nullable
// workflow_runs.loop_run_id/loop_iteration columns.

import (
	"context"
	"errors"
	"testing"
)

// TestWorkflowRun_LoopScope_RoundTrips confirms the two new columns read
// back through CreateWorkflowRun/GetWorkflowRun exactly as inserted (or
// left NULL when unset), and that UpdateWorkflowRunLoopScope -- the
// follow-up narrow updater this task adds because WorkflowLaunchRequest has
// no natural pass-through for these two values -- stamps an existing,
// previously-unscoped row correctly.
func TestWorkflowRun_LoopScope_RoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	goal := makeTestGoal(t, s, "goal for workflow_runs loop scoping round trip")
	lr := &LoopRun{GoalID: goal.ID, DefinitionName: "implementation_iteration"}
	if err := s.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}

	// Plain run: both new fields left nil -- must persist and read back as
	// NULL, the pre-existing shape every non-loop-launched run has today.
	if err := s.CreateWorkflowRun(&WorkflowRunRow{
		ID:             "run-loopscope-plain",
		DefinitionName: "demo",
	}); err != nil {
		t.Fatalf("CreateWorkflowRun (plain): %v", err)
	}
	plain, err := s.GetWorkflowRun("run-loopscope-plain")
	if err != nil {
		t.Fatalf("GetWorkflowRun (plain): %v", err)
	}
	if plain.LoopRunID != nil {
		t.Errorf("plain.LoopRunID = %v, want nil", plain.LoopRunID)
	}
	if plain.LoopIteration != nil {
		t.Errorf("plain.LoopIteration = %v, want nil", plain.LoopIteration)
	}

	// Loop-scoped run: both fields set at insert time -- confirms
	// CreateWorkflowRun's pass-through for a caller that already knows the
	// values (even though no real caller does yet -- see this file's own
	// doc comment).
	iteration := 3
	if err := s.CreateWorkflowRun(&WorkflowRunRow{
		ID:             "run-loopscope-set",
		DefinitionName: "demo",
		LoopRunID:      &lr.ID,
		LoopIteration:  &iteration,
	}); err != nil {
		t.Fatalf("CreateWorkflowRun (loop-scoped): %v", err)
	}
	scoped, err := s.GetWorkflowRun("run-loopscope-set")
	if err != nil {
		t.Fatalf("GetWorkflowRun (loop-scoped): %v", err)
	}
	if scoped.LoopRunID == nil || *scoped.LoopRunID != lr.ID {
		t.Errorf("scoped.LoopRunID = %v, want %q", scoped.LoopRunID, lr.ID)
	}
	if scoped.LoopIteration == nil || *scoped.LoopIteration != 3 {
		t.Errorf("scoped.LoopIteration = %v, want 3", scoped.LoopIteration)
	}

	// UpdateWorkflowRunLoopScope: the documented real call path (task 08's
	// LoopEngine, after WorkflowLauncher.Launch returns) -- stamp an
	// initially-unscoped row after the fact.
	if err := s.UpdateWorkflowRunLoopScope("run-loopscope-plain", lr.ID, 7); err != nil {
		t.Fatalf("UpdateWorkflowRunLoopScope: %v", err)
	}
	updated, err := s.GetWorkflowRun("run-loopscope-plain")
	if err != nil {
		t.Fatalf("GetWorkflowRun after UpdateWorkflowRunLoopScope: %v", err)
	}
	if updated.LoopRunID == nil || *updated.LoopRunID != lr.ID {
		t.Errorf("updated.LoopRunID = %v, want %q", updated.LoopRunID, lr.ID)
	}
	if updated.LoopIteration == nil || *updated.LoopIteration != 7 {
		t.Errorf("updated.LoopIteration = %v, want 7", updated.LoopIteration)
	}

	if err := s.UpdateWorkflowRunLoopScope("missing-run", lr.ID, 1); !errors.Is(err, ErrWorkflowRunNotFound) {
		t.Errorf("UpdateWorkflowRunLoopScope(missing-run) = %v, want ErrWorkflowRunNotFound", err)
	}

	if err := s.UpdateWorkflowRunLoopScope("run-loopscope-plain", "", 1); err == nil {
		t.Fatal("UpdateWorkflowRunLoopScope(empty loop_run_id) = nil error, want an error")
	}
}
