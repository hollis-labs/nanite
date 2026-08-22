package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// makeTestGoal (loop_runs.goal_id is FK-enforced, so every LoopRun test
// needs a real goals row to point at) is defined once in
// goal_evidence_test.go and shared across this package's test files.

// TestLoopRun_RoundTrip exercises the basic CRUD path: create (with a
// Budget sub-structure round-trip), get, status update (with completedAt),
// iteration bump, no-progress-streak update, delete -- per
// TASKS/loops/03-loop-runs-schema.md's "Done means".
func TestLoopRun_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "Implement durable loop execution in Nanite")

	lr := &LoopRun{
		GoalID:         goal.ID,
		DefinitionName: "implementation_iteration",
	}
	if err := lr.SetBudget(Budget{
		MaxIterations:           20,
		MaxFailures:             3,
		MaxRuntimeSeconds:       3600,
		MaxNoProgressIterations: 2,
		OnExhausted:             LoopRunOnExhaustedEscalate,
	}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := s.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	if lr.ID == "" {
		t.Fatal("CreateLoopRun did not populate ID")
	}
	if lr.StartedAt == "" || lr.UpdatedAt == "" {
		t.Fatalf("CreateLoopRun did not populate StartedAt/UpdatedAt: %+v", lr)
	}
	if lr.Status != LoopRunStatusRunning {
		t.Errorf("default status = %q, want %q", lr.Status, LoopRunStatusRunning)
	}

	got, err := s.GetLoopRun(ctx, lr.ID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if got.GoalID != goal.ID || got.DefinitionName != "implementation_iteration" {
		t.Errorf("unexpected row: %+v", got)
	}
	budget, err := got.Budget()
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if budget.MaxIterations != 20 || budget.MaxFailures != 3 ||
		budget.MaxRuntimeSeconds != 3600 || budget.MaxNoProgressIterations != 2 ||
		budget.OnExhausted != LoopRunOnExhaustedEscalate {
		t.Errorf("Budget round-trip: got %+v", budget)
	}

	// UpdateLoopRunStatus with a nil completedAt leaves completed_at
	// untouched.
	if err := s.UpdateLoopRunStatus(ctx, lr.ID, LoopRunStatusWaitingOnGate, nil); err != nil {
		t.Fatalf("UpdateLoopRunStatus (waiting_on_gate): %v", err)
	}
	afterWait, err := s.GetLoopRun(ctx, lr.ID)
	if err != nil {
		t.Fatalf("GetLoopRun after status update: %v", err)
	}
	if afterWait.Status != LoopRunStatusWaitingOnGate {
		t.Errorf("status after update: %q", afterWait.Status)
	}
	if afterWait.CompletedAt != "" {
		t.Errorf("CompletedAt set with nil completedAt param: %q", afterWait.CompletedAt)
	}
	// UpdatedAt is RFC3339 (1-second resolution) and this test runs fast
	// enough that it can legitimately land in the same wall-clock second as
	// CreateLoopRun's own UpdatedAt -- only assert it's still populated and
	// parseable, not that it strictly advanced.
	if afterWait.UpdatedAt == "" {
		t.Error("UpdatedAt not populated after UpdateLoopRunStatus")
	}
	if _, err := time.Parse(time.RFC3339, afterWait.UpdatedAt); err != nil {
		t.Errorf("UpdatedAt %q did not parse as RFC3339: %v", afterWait.UpdatedAt, err)
	}

	// UpdateLoopRunStatus with a non-nil completedAt sets it.
	completedAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	if err := s.UpdateLoopRunStatus(ctx, lr.ID, LoopRunStatusCompleted, &completedAt); err != nil {
		t.Fatalf("UpdateLoopRunStatus (completed): %v", err)
	}
	afterComplete, err := s.GetLoopRun(ctx, lr.ID)
	if err != nil {
		t.Fatalf("GetLoopRun after completion: %v", err)
	}
	if afterComplete.Status != LoopRunStatusCompleted {
		t.Errorf("status after completion: %q", afterComplete.Status)
	}
	parsedCompletedAt, err := time.Parse(time.RFC3339, afterComplete.CompletedAt)
	if err != nil {
		t.Fatalf("CompletedAt %q did not parse: %v", afterComplete.CompletedAt, err)
	}
	if !parsedCompletedAt.Equal(completedAt) {
		t.Errorf("CompletedAt = %v, want %v", parsedCompletedAt, completedAt)
	}

	// BumpLoopRunIteration increments current_iteration.
	if err := s.BumpLoopRunIteration(ctx, lr.ID); err != nil {
		t.Fatalf("BumpLoopRunIteration: %v", err)
	}
	if err := s.BumpLoopRunIteration(ctx, lr.ID); err != nil {
		t.Fatalf("BumpLoopRunIteration (second): %v", err)
	}
	afterBump, err := s.GetLoopRun(ctx, lr.ID)
	if err != nil {
		t.Fatalf("GetLoopRun after bump: %v", err)
	}
	if afterBump.CurrentIteration != 2 {
		t.Errorf("CurrentIteration = %d, want 2", afterBump.CurrentIteration)
	}

	// UpdateLoopRunNoProgressStreak sets (not increments) the streak.
	if err := s.UpdateLoopRunNoProgressStreak(ctx, lr.ID, 3); err != nil {
		t.Fatalf("UpdateLoopRunNoProgressStreak: %v", err)
	}
	afterStreak, err := s.GetLoopRun(ctx, lr.ID)
	if err != nil {
		t.Fatalf("GetLoopRun after streak update: %v", err)
	}
	if afterStreak.NoProgressStreak != 3 {
		t.Errorf("NoProgressStreak = %d, want 3", afterStreak.NoProgressStreak)
	}
	if err := s.UpdateLoopRunNoProgressStreak(ctx, lr.ID, 0); err != nil {
		t.Fatalf("UpdateLoopRunNoProgressStreak (reset): %v", err)
	}
	afterReset, err := s.GetLoopRun(ctx, lr.ID)
	if err != nil {
		t.Fatalf("GetLoopRun after streak reset: %v", err)
	}
	if afterReset.NoProgressStreak != 0 {
		t.Errorf("NoProgressStreak after reset = %d, want 0", afterReset.NoProgressStreak)
	}

	if err := s.DeleteLoopRun(ctx, lr.ID); err != nil {
		t.Fatalf("DeleteLoopRun: %v", err)
	}
	if _, err := s.GetLoopRun(ctx, lr.ID); !errors.Is(err, ErrLoopRunNotFound) {
		t.Errorf("post-delete GetLoopRun: got %v, want ErrLoopRunNotFound", err)
	}
}

// TestLoopRun_BudgetOnExhaustedBothValues confirms Budget's JSON sub-
// structure round-trips correctly with OnExhausted set to each of its two
// valid values, per the task's "Done means".
func TestLoopRun_BudgetOnExhaustedBothValues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "budget on_exhausted coverage")

	for _, onExhausted := range []string{LoopRunOnExhaustedEscalate, LoopRunOnExhaustedFail} {
		lr := &LoopRun{GoalID: goal.ID, DefinitionName: "def"}
		if err := lr.SetBudget(Budget{MaxIterations: 5, OnExhausted: onExhausted}); err != nil {
			t.Fatalf("SetBudget(%q): %v", onExhausted, err)
		}
		if err := s.CreateLoopRun(ctx, lr); err != nil {
			t.Fatalf("CreateLoopRun(%q): %v", onExhausted, err)
		}
		got, err := s.GetLoopRun(ctx, lr.ID)
		if err != nil {
			t.Fatalf("GetLoopRun(%q): %v", onExhausted, err)
		}
		budget, err := got.Budget()
		if err != nil {
			t.Fatalf("Budget(%q): %v", onExhausted, err)
		}
		if budget.OnExhausted != onExhausted {
			t.Errorf("OnExhausted round-trip: got %q, want %q", budget.OnExhausted, onExhausted)
		}
	}

	// An unset OnExhausted defaults to "escalate" (this design's own
	// decision, TASKS/loops/03-loop-runs-schema.md's Context section).
	defaulted := &LoopRun{GoalID: goal.ID, DefinitionName: "def"}
	if err := defaulted.SetBudget(Budget{MaxIterations: 1}); err != nil {
		t.Fatalf("SetBudget (default OnExhausted): %v", err)
	}
	if err := s.CreateLoopRun(ctx, defaulted); err != nil {
		t.Fatalf("CreateLoopRun (default OnExhausted): %v", err)
	}
	gotDefaulted, err := s.GetLoopRun(ctx, defaulted.ID)
	if err != nil {
		t.Fatalf("GetLoopRun (default OnExhausted): %v", err)
	}
	budget, err := gotDefaulted.Budget()
	if err != nil {
		t.Fatalf("Budget (default OnExhausted): %v", err)
	}
	if budget.OnExhausted != LoopRunOnExhaustedEscalate {
		t.Errorf("default OnExhausted = %q, want %q", budget.OnExhausted, LoopRunOnExhaustedEscalate)
	}
}

// TestLoopRun_BudgetValidation confirms validateBudget rejects an invalid
// OnExhausted and a negative max_* field, both via SetBudget, before
// anything reaches the DB.
func TestLoopRun_BudgetValidation(t *testing.T) {
	lr := &LoopRun{}
	if err := lr.SetBudget(Budget{OnExhausted: "bogus"}); err == nil {
		t.Error("SetBudget: expected error for invalid OnExhausted, got nil")
	}
	if lr.BudgetJSON != "" {
		t.Errorf("SetBudget left BudgetJSON set after a validation failure: %q", lr.BudgetJSON)
	}
	if err := lr.SetBudget(Budget{OnExhausted: LoopRunOnExhaustedFail, MaxIterations: -1}); err == nil {
		t.Error("SetBudget: expected error for negative MaxIterations, got nil")
	}
	if err := lr.SetBudget(Budget{OnExhausted: LoopRunOnExhaustedFail, MaxFailures: -1}); err == nil {
		t.Error("SetBudget: expected error for negative MaxFailures, got nil")
	}
	if err := lr.SetBudget(Budget{OnExhausted: LoopRunOnExhaustedFail, MaxRuntimeSeconds: -1}); err == nil {
		t.Error("SetBudget: expected error for negative MaxRuntimeSeconds, got nil")
	}
	if err := lr.SetBudget(Budget{OnExhausted: LoopRunOnExhaustedFail, MaxNoProgressIterations: -1}); err == nil {
		t.Error("SetBudget: expected error for negative MaxNoProgressIterations, got nil")
	}
}

// TestLoopRun_ListLoopRuns_GoalAndActiveStatusFilter is the regression test
// for the task's "Done means" bullet on ListLoopRuns: filtered by goal_id +
// an "active" status set returns the expected subset across multiple
// LoopRuns spanning different goals and statuses.
func TestLoopRun_ListLoopRuns_GoalAndActiveStatusFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalA := makeTestGoal(t, s, "goal A")
	goalB := makeTestGoal(t, s, "goal B")

	mustCreate := func(goalID, status string) *LoopRun {
		lr := &LoopRun{GoalID: goalID, DefinitionName: "def", Status: status}
		if err := s.CreateLoopRun(ctx, lr); err != nil {
			t.Fatalf("CreateLoopRun(goal=%s status=%s): %v", goalID, status, err)
		}
		return lr
	}

	aRunning := mustCreate(goalA.ID, LoopRunStatusRunning)
	aWaitingGate := mustCreate(goalA.ID, LoopRunStatusWaitingOnGate)
	aWaitingEscalation := mustCreate(goalA.ID, LoopRunStatusWaitingOnEscalation)
	_ = mustCreate(goalA.ID, LoopRunStatusCompleted) // goal A, but terminal -- excluded
	_ = mustCreate(goalB.ID, LoopRunStatusRunning)   // goal B, active -- excluded by goal filter

	// Unfiltered: every row across both goals.
	all, err := s.ListLoopRuns(ctx, LoopRunFilter{})
	if err != nil {
		t.Fatalf("ListLoopRuns (unfiltered): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("ListLoopRuns (unfiltered): got %d, want 5", len(all))
	}

	// goal_id + active-status-set filter -- task 10's exact intended call
	// shape.
	active, err := s.ListLoopRuns(ctx, LoopRunFilter{GoalID: goalA.ID, Statuses: LoopRunActiveStatuses})
	if err != nil {
		t.Fatalf("ListLoopRuns (goal + active statuses): %v", err)
	}
	gotIDs := map[string]bool{}
	for _, lr := range active {
		gotIDs[lr.ID] = true
	}
	wantIDs := map[string]bool{
		aRunning.ID:           true,
		aWaitingGate.ID:       true,
		aWaitingEscalation.ID: true,
	}
	if len(active) != len(wantIDs) {
		t.Fatalf("ListLoopRuns (goal + active statuses): got %d rows, want %d", len(active), len(wantIDs))
	}
	for id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("ListLoopRuns (goal + active statuses): missing expected id %s", id)
		}
	}

	// goal_id alone.
	byGoal, err := s.ListLoopRuns(ctx, LoopRunFilter{GoalID: goalB.ID})
	if err != nil {
		t.Fatalf("ListLoopRuns (goal B): %v", err)
	}
	if len(byGoal) != 1 {
		t.Fatalf("ListLoopRuns (goal B): got %d, want 1", len(byGoal))
	}
}

// TestLoopRun_DefaultsAndNotFound confirms JSON sub-structure defaults
// apply when left unset, and that unknown-id lookups/mutations return
// ErrLoopRunNotFound rather than a generic error.
func TestLoopRun_DefaultsAndNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "defaults only")

	lr := &LoopRun{GoalID: goal.ID, DefinitionName: "def"}
	if err := s.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	if lr.BudgetJSON != "{}" || lr.ContinuationPolicyJSON != "{}" {
		t.Errorf("unexpected JSON column defaults: budget=%q continuation_policy=%q", lr.BudgetJSON, lr.ContinuationPolicyJSON)
	}
	if lr.CurrentIteration != 0 || lr.NoProgressStreak != 0 {
		t.Errorf("unexpected counter defaults: current_iteration=%d no_progress_streak=%d", lr.CurrentIteration, lr.NoProgressStreak)
	}

	if _, err := s.GetLoopRun(ctx, "does-not-exist"); !errors.Is(err, ErrLoopRunNotFound) {
		t.Errorf("GetLoopRun unknown id: got %v, want ErrLoopRunNotFound", err)
	}
	if err := s.UpdateLoopRunStatus(ctx, "does-not-exist", LoopRunStatusRunning, nil); !errors.Is(err, ErrLoopRunNotFound) {
		t.Errorf("UpdateLoopRunStatus unknown id: got %v, want ErrLoopRunNotFound", err)
	}
	if err := s.BumpLoopRunIteration(ctx, "does-not-exist"); !errors.Is(err, ErrLoopRunNotFound) {
		t.Errorf("BumpLoopRunIteration unknown id: got %v, want ErrLoopRunNotFound", err)
	}
	if err := s.UpdateLoopRunNoProgressStreak(ctx, "does-not-exist", 1); !errors.Is(err, ErrLoopRunNotFound) {
		t.Errorf("UpdateLoopRunNoProgressStreak unknown id: got %v, want ErrLoopRunNotFound", err)
	}
	if err := s.DeleteLoopRun(ctx, "does-not-exist"); !errors.Is(err, ErrLoopRunNotFound) {
		t.Errorf("DeleteLoopRun unknown id: got %v, want ErrLoopRunNotFound", err)
	}
}

// TestLoopRun_StatusValidation confirms CreateLoopRun/UpdateLoopRunStatus
// reject an unknown status string before it ever reaches the DB's own
// CHECK constraint.
func TestLoopRun_StatusValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "bad status")

	if err := s.CreateLoopRun(ctx, &LoopRun{GoalID: goal.ID, DefinitionName: "def", Status: "bogus"}); err == nil {
		t.Error("CreateLoopRun: expected error for invalid status, got nil")
	}

	lr := &LoopRun{GoalID: goal.ID, DefinitionName: "def"}
	if err := s.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	if err := s.UpdateLoopRunStatus(ctx, lr.ID, "bogus", nil); err == nil {
		t.Error("UpdateLoopRunStatus: expected error for invalid status, got nil")
	}
}

// TestLoopRun_StatusCheckConstraint confirms migration 141's DB-level
// CHECK constraint rejects an invalid status even bypassing Go-layer
// validation (a raw INSERT), mirroring goals_test.go's
// TestGoal_StatusCheckConstraint.
func TestLoopRun_StatusCheckConstraint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "raw insert status")

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO loop_runs (id, goal_id, definition_name, status) VALUES (?, ?, ?, ?)`,
		"raw-bad-status", goal.ID, "def", "not-a-real-status",
	)
	if err == nil {
		t.Fatal("raw INSERT with invalid status: expected CHECK violation, got nil")
	}
}

// TestLoopRun_GoalFKEnforced confirms goal_id is a real, enforced FK (this
// codebase runs with PRAGMA foreign_keys=1) -- an unknown goal_id is
// rejected at the DB layer.
func TestLoopRun_GoalFKEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.CreateLoopRun(ctx, &LoopRun{GoalID: "does-not-exist", DefinitionName: "def"})
	if err == nil {
		t.Fatal("CreateLoopRun with unknown goal_id: expected FK violation, got nil")
	}
}
