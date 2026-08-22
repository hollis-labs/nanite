package store

import (
	"context"
	"errors"
	"testing"
)

// TestGoal_RoundTrip exercises the basic CRUD path: create, get, list,
// update, delete -- including all four JSON sub-structure columns and a
// parent_goal_id self-reference, per TASKS/loops/01-goals-schema.md's
// "Done means".
func TestGoal_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	parent := &Goal{
		Intent:   "Implement durable loop execution in Nanite",
		Priority: "high",
		Scope:    "internal/loop",
		Owner:    "operator",
		Source:   "planning-session",
	}
	if err := parent.SetDesiredState([]string{
		"loops can execute agent-workflow iterations",
		"state persists across iterations",
		"evaluators can terminate execution",
	}); err != nil {
		t.Fatalf("SetDesiredState: %v", err)
	}
	if err := parent.SetAcceptanceCriteria([]string{"loop_unit_tests_pass", "ralph_integration_test_passes"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := parent.SetConstraints([]string{"existing agentworkflow API remains backwards compatible"}); err != nil {
		t.Fatalf("SetConstraints: %v", err)
	}
	if err := parent.SetInvariants([]string{"agentworkflow stays DAG-only, no cycles"}); err != nil {
		t.Fatalf("SetInvariants: %v", err)
	}

	if err := s.CreateGoal(ctx, parent); err != nil {
		t.Fatalf("CreateGoal (parent): %v", err)
	}
	if parent.ID == "" {
		t.Fatal("CreateGoal did not populate ID")
	}
	if parent.CreatedAt == "" {
		t.Fatalf("CreateGoal did not populate CreatedAt: %+v", parent)
	}
	if parent.Status != GoalStatusDraft {
		t.Errorf("default status = %q, want %q", parent.Status, GoalStatusDraft)
	}

	// Get, and verify every JSON sub-structure column round-trips.
	got, err := s.GetGoal(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.Intent != parent.Intent || got.Priority != "high" || got.Scope != "internal/loop" ||
		got.Owner != "operator" || got.Source != "planning-session" {
		t.Errorf("unexpected row: %+v", got)
	}
	desired, err := got.DesiredState()
	if err != nil {
		t.Fatalf("DesiredState: %v", err)
	}
	if len(desired) != 3 || desired[0] != "loops can execute agent-workflow iterations" {
		t.Errorf("DesiredState round-trip: got %v", desired)
	}
	criteria, err := got.AcceptanceCriteria()
	if err != nil {
		t.Fatalf("AcceptanceCriteria: %v", err)
	}
	if len(criteria) != 2 || criteria[1] != "ralph_integration_test_passes" {
		t.Errorf("AcceptanceCriteria round-trip: got %v", criteria)
	}
	constraints, err := got.Constraints()
	if err != nil {
		t.Fatalf("Constraints: %v", err)
	}
	if len(constraints) != 1 || constraints[0] != "existing agentworkflow API remains backwards compatible" {
		t.Errorf("Constraints round-trip: got %v", constraints)
	}
	invariants, err := got.Invariants()
	if err != nil {
		t.Fatalf("Invariants: %v", err)
	}
	if len(invariants) != 1 || invariants[0] != "agentworkflow stays DAG-only, no cycles" {
		t.Errorf("Invariants round-trip: got %v", invariants)
	}

	// parent_goal_id self-reference: create a child goal pointing at parent.
	child := &Goal{
		ParentGoalID: parent.ID,
		Intent:       "Implement StepKindLoop",
	}
	if err := s.CreateGoal(ctx, child); err != nil {
		t.Fatalf("CreateGoal (child): %v", err)
	}
	gotChild, err := s.GetGoal(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetGoal (child): %v", err)
	}
	if gotChild.ParentGoalID != parent.ID {
		t.Errorf("child ParentGoalID = %q, want %q", gotChild.ParentGoalID, parent.ID)
	}

	// List: unfiltered returns both; filtered by parent_goal_id returns only
	// the child; filtered by status returns only draft goals (both, here).
	all, err := s.ListGoals(ctx, GoalFilter{})
	if err != nil {
		t.Fatalf("ListGoals (unfiltered): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListGoals (unfiltered): got %d, want 2", len(all))
	}
	byParent, err := s.ListGoals(ctx, GoalFilter{ParentGoalID: parent.ID})
	if err != nil {
		t.Fatalf("ListGoals (by parent): %v", err)
	}
	if len(byParent) != 1 || byParent[0].ID != child.ID {
		t.Fatalf("ListGoals (by parent): got %+v", byParent)
	}
	byStatus, err := s.ListGoals(ctx, GoalFilter{Status: GoalStatusDraft})
	if err != nil {
		t.Fatalf("ListGoals (by status): %v", err)
	}
	if len(byStatus) != 2 {
		t.Fatalf("ListGoals (by status=draft): got %d, want 2", len(byStatus))
	}

	// UpdateGoal: full mutable-column replace. Status/activated_at/
	// completed_at are untouched by this call.
	updated := *got
	updated.Intent = "Implement durable loop execution in Nanite (revised)"
	updated.Priority = "medium"
	if err := updated.SetDesiredState([]string{"revised desired state"}); err != nil {
		t.Fatalf("SetDesiredState (update): %v", err)
	}
	if err := s.UpdateGoal(ctx, &updated); err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	afterUpdate, err := s.GetGoal(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetGoal after update: %v", err)
	}
	if afterUpdate.Intent != updated.Intent || afterUpdate.Priority != "medium" {
		t.Errorf("update did not apply: %+v", afterUpdate)
	}
	if afterUpdate.Status != GoalStatusDraft {
		t.Errorf("UpdateGoal changed status: got %q, want unchanged %q", afterUpdate.Status, GoalStatusDraft)
	}
	revisedDesired, err := afterUpdate.DesiredState()
	if err != nil {
		t.Fatalf("DesiredState after update: %v", err)
	}
	if len(revisedDesired) != 1 || revisedDesired[0] != "revised desired state" {
		t.Errorf("DesiredState after update: got %v", revisedDesired)
	}

	// UpdateGoalStatus: draft -> active sets activated_at; a second call
	// (active -> active, no-op transition for this task's scope) does not
	// clobber the first activated_at.
	if err := s.UpdateGoalStatus(ctx, parent.ID, GoalStatusActive); err != nil {
		t.Fatalf("UpdateGoalStatus (active): %v", err)
	}
	afterActive, err := s.GetGoal(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetGoal after activate: %v", err)
	}
	if afterActive.Status != GoalStatusActive {
		t.Errorf("status after activate: %q", afterActive.Status)
	}
	if afterActive.ActivatedAt == "" {
		t.Fatal("ActivatedAt not set after transition to active")
	}
	firstActivatedAt := afterActive.ActivatedAt

	if err := s.UpdateGoalStatus(ctx, parent.ID, GoalStatusActive); err != nil {
		t.Fatalf("UpdateGoalStatus (active again): %v", err)
	}
	afterActiveAgain, err := s.GetGoal(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetGoal after second activate: %v", err)
	}
	if afterActiveAgain.ActivatedAt != firstActivatedAt {
		t.Errorf("ActivatedAt changed on redundant activate call: got %q, want %q", afterActiveAgain.ActivatedAt, firstActivatedAt)
	}

	// UpdateGoalStatus: active -> satisfied (terminal) sets completed_at.
	if err := s.UpdateGoalStatus(ctx, parent.ID, GoalStatusSatisfied); err != nil {
		t.Fatalf("UpdateGoalStatus (satisfied): %v", err)
	}
	afterSatisfied, err := s.GetGoal(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetGoal after satisfy: %v", err)
	}
	if afterSatisfied.Status != GoalStatusSatisfied {
		t.Errorf("status after satisfy: %q", afterSatisfied.Status)
	}
	if afterSatisfied.CompletedAt == "" {
		t.Fatal("CompletedAt not set after transition to a terminal status")
	}

	// UpdateGoalStatus: blocked is a paused, not terminal, status -- does
	// not set completed_at.
	blockedGoal := &Goal{Intent: "blocked goal for status test"}
	if err := s.CreateGoal(ctx, blockedGoal); err != nil {
		t.Fatalf("CreateGoal (blocked fixture): %v", err)
	}
	if err := s.UpdateGoalStatus(ctx, blockedGoal.ID, GoalStatusBlocked); err != nil {
		t.Fatalf("UpdateGoalStatus (blocked): %v", err)
	}
	afterBlocked, err := s.GetGoal(ctx, blockedGoal.ID)
	if err != nil {
		t.Fatalf("GetGoal after block: %v", err)
	}
	if afterBlocked.Status != GoalStatusBlocked {
		t.Errorf("status after block: %q", afterBlocked.Status)
	}
	if afterBlocked.CompletedAt != "" {
		t.Errorf("CompletedAt set for non-terminal status blocked: %q", afterBlocked.CompletedAt)
	}
	if err := s.DeleteGoal(ctx, blockedGoal.ID); err != nil {
		t.Fatalf("DeleteGoal (blocked fixture cleanup): %v", err)
	}

	// Delete child first (parent_goal_id FK has no ON DELETE, so deleting
	// the still-referenced parent would fail).
	if err := s.DeleteGoal(ctx, child.ID); err != nil {
		t.Fatalf("DeleteGoal (child): %v", err)
	}
	if err := s.DeleteGoal(ctx, parent.ID); err != nil {
		t.Fatalf("DeleteGoal (parent): %v", err)
	}
	if _, err := s.GetGoal(ctx, parent.ID); !errors.Is(err, ErrGoalNotFound) {
		t.Errorf("post-delete GetGoal: got %v, want ErrGoalNotFound", err)
	}
}

// TestGoal_ParentFKEnforced confirms parent_goal_id is a real, enforced FK
// (this codebase runs with PRAGMA foreign_keys=1) -- an unknown parent id
// is rejected at the DB layer, and deleting a goal that is still some other
// row's parent fails rather than silently orphaning the child.
func TestGoal_ParentFKEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateGoal(ctx, &Goal{Intent: "orphan", ParentGoalID: "does-not-exist"}); err == nil {
		t.Fatal("CreateGoal with unknown parent_goal_id: expected FK violation, got nil")
	}

	parent := &Goal{Intent: "parent"}
	if err := s.CreateGoal(ctx, parent); err != nil {
		t.Fatalf("CreateGoal (parent): %v", err)
	}
	child := &Goal{Intent: "child", ParentGoalID: parent.ID}
	if err := s.CreateGoal(ctx, child); err != nil {
		t.Fatalf("CreateGoal (child): %v", err)
	}
	if err := s.DeleteGoal(ctx, parent.ID); err == nil {
		t.Fatal("DeleteGoal on a goal still referenced by a child: expected FK violation, got nil")
	}
}

// TestGoal_DefaultsAndNotFound confirms JSON sub-structure defaults apply
// when left unset, and that unknown-id lookups/mutations return
// ErrGoalNotFound rather than a generic error.
func TestGoal_DefaultsAndNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g := &Goal{Intent: "defaults only"}
	if err := s.CreateGoal(ctx, g); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if g.DesiredStateJSON != "[]" || g.ConstraintsJSON != "[]" ||
		g.AcceptanceCriteriaJSON != "[]" || g.InvariantsJSON != "[]" {
		t.Errorf("unexpected JSON column defaults: %+v", g)
	}
	desired, err := g.DesiredState()
	if err != nil {
		t.Fatalf("DesiredState: %v", err)
	}
	if len(desired) != 0 {
		t.Errorf("DesiredState on default-empty goal: got %d, want 0", len(desired))
	}

	if _, err := s.GetGoal(ctx, "does-not-exist"); !errors.Is(err, ErrGoalNotFound) {
		t.Errorf("GetGoal unknown id: got %v, want ErrGoalNotFound", err)
	}
	if err := s.UpdateGoal(ctx, &Goal{ID: "does-not-exist", Intent: "x"}); !errors.Is(err, ErrGoalNotFound) {
		t.Errorf("UpdateGoal unknown id: got %v, want ErrGoalNotFound", err)
	}
	if err := s.UpdateGoalStatus(ctx, "does-not-exist", GoalStatusActive); !errors.Is(err, ErrGoalNotFound) {
		t.Errorf("UpdateGoalStatus unknown id: got %v, want ErrGoalNotFound", err)
	}
	if err := s.DeleteGoal(ctx, "does-not-exist"); !errors.Is(err, ErrGoalNotFound) {
		t.Errorf("DeleteGoal unknown id: got %v, want ErrGoalNotFound", err)
	}
}

// TestGoal_StatusValidation confirms CreateGoal/UpdateGoalStatus reject an
// unknown status string before it ever reaches the DB's own CHECK
// constraint.
func TestGoal_StatusValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateGoal(ctx, &Goal{Intent: "bad status", Status: "bogus"}); err == nil {
		t.Error("CreateGoal: expected error for invalid status, got nil")
	}

	g := &Goal{Intent: "valid status"}
	if err := s.CreateGoal(ctx, g); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := s.UpdateGoalStatus(ctx, g.ID, "bogus"); err == nil {
		t.Error("UpdateGoalStatus: expected error for invalid status, got nil")
	}
}

// TestGoal_StatusCheckConstraint confirms migration 138's DB-level CHECK
// constraint rejects an invalid status even bypassing Go-layer validation
// (a raw INSERT), mirroring TestAgentSchedule_ScheduleKindCheckRejectsRetiredValues's
// own "confirm the DB, not just the Go layer, enforces this" discipline.
func TestGoal_StatusCheckConstraint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO goals (id, intent, status) VALUES (?, ?, ?)`,
		"raw-bad-status", "raw insert bypassing Go validation", "not-a-real-status",
	)
	if err == nil {
		t.Fatal("raw INSERT with invalid status: expected CHECK violation, got nil")
	}
}
