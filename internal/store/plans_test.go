package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCreatePlan(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{
		Scope:   "project",
		ScopeID: "proj-1",
		Title:   "Refactoring plan",
	}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	if plan.ID == "" {
		t.Error("expected ID to be generated")
	}
	if plan.Status != "proposed" {
		t.Errorf("expected status 'proposed', got %q", plan.Status)
	}
}

func TestGetPlan(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{Scope: "workspace", Title: "Migration plan"}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := s.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Title != "Migration plan" {
		t.Errorf("Title mismatch: got %q", got.Title)
	}
}

func TestListPlans(t *testing.T) {
	s := newTestStore(t)

	for _, p := range []struct {
		title  string
		scope  string
		status string
	}{
		{"Plan A", "workspace", "proposed"},
		{"Plan B", "project", "approved"},
		{"Plan C", "workspace", "complete"},
	} {
		plan := &Plan{Scope: p.scope, Title: p.title, Status: p.status}
		if err := s.CreatePlan(context.Background(), plan); err != nil {
			t.Fatalf("CreatePlan %s: %v", p.title, err)
		}
	}

	plans, err := s.ListPlans(context.Background(), PlanFilter{Scope: "workspace"})
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) != 2 {
		t.Errorf("expected 2 workspace plans, got %d", len(plans))
	}

	plans, err = s.ListPlans(context.Background(), PlanFilter{Status: "proposed"})
	if err != nil {
		t.Fatalf("ListPlans proposed: %v", err)
	}
	if len(plans) != 1 {
		t.Errorf("expected 1 proposed plan, got %d", len(plans))
	}
}

func TestUpdatePlan(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{Scope: "workspace", Title: "Original"}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	plan.Title = "Updated"
	plan.Status = "approved"
	if err := s.UpdatePlan(context.Background(), plan); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	got, err := s.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("Title not updated: got %q", got.Title)
	}
	if got.Status != "approved" {
		t.Errorf("Status not updated: got %q", got.Status)
	}
}

func TestUpdatePlanStep(t *testing.T) {
	s := newTestStore(t)

	steps := []PlanStep{
		{ID: "step-1", Title: "Design", Status: "pending"},
		{ID: "step-2", Title: "Implement", Status: "pending", DependsOn: []string{"step-1"}},
	}
	stepsJSON, _ := json.Marshal(steps)

	plan := &Plan{
		Scope: "workspace",
		Title: "Multi-step plan",
		Steps: string(stepsJSON),
	}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	// Update step-1 status.
	if err := s.UpdatePlanStep(context.Background(), plan.ID, "step-1", PlanStep{Status: "done"}); err != nil {
		t.Fatalf("UpdatePlanStep: %v", err)
	}

	got, err := s.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}

	gotSteps, err := got.ParsePlanSteps()
	if err != nil {
		t.Fatalf("ParsePlanSteps: %v", err)
	}
	if len(gotSteps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(gotSteps))
	}
	if gotSteps[0].Status != "done" {
		t.Errorf("step-1 status: got %q, want 'done'", gotSteps[0].Status)
	}
	if gotSteps[1].Status != "pending" {
		t.Errorf("step-2 status should be unchanged: got %q", gotSteps[1].Status)
	}
}

func TestUpdatePlanStepNotFound(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{Scope: "workspace", Title: "Plan", Steps: "[]"}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	err := s.UpdatePlanStep(context.Background(), plan.ID, "nonexistent", PlanStep{Status: "done"})
	if err == nil {
		t.Error("expected error for nonexistent step")
	}
}

// TestAppendPlanSteps_AutoAssignsSequentialIDs verifies append at the end
// keeps existing steps stable and auto-assigns the next s<n>.
// CW-20260430-0001 (SP1).
func TestAppendPlanSteps_AutoAssignsSequentialIDs(t *testing.T) {
	s := newTestStore(t)

	steps := []PlanStep{
		{ID: "s1", Title: "Design", Status: "done"},
		{ID: "s2", Title: "Implement", Status: "in_progress"},
		{ID: "s3", Title: "Verify", Status: "pending"},
	}
	stepsJSON, _ := json.Marshal(steps)
	plan := &Plan{Scope: "workspace", Title: "Append target", Steps: string(stepsJSON)}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	appended, err := s.AppendPlanSteps(context.Background(), plan.ID, []PlanStep{
		{Title: "New step A"},
		{Title: "New step B", DependsOn: []string{"s4"}},
	})
	if err != nil {
		t.Fatalf("AppendPlanSteps: %v", err)
	}
	if len(appended) != 2 {
		t.Fatalf("expected 2 appended, got %d", len(appended))
	}
	if appended[0].ID != "s4" {
		t.Errorf("first appended id: got %q, want s4", appended[0].ID)
	}
	if appended[1].ID != "s5" {
		t.Errorf("second appended id: got %q, want s5", appended[1].ID)
	}
	if appended[0].Status != "pending" {
		t.Errorf("status default: got %q, want pending", appended[0].Status)
	}

	got, err := s.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	gotSteps, _ := got.ParsePlanSteps()
	if len(gotSteps) != 5 {
		t.Fatalf("expected 5 total steps, got %d", len(gotSteps))
	}
	// Existing steps must be unchanged in id+status+title.
	for i, want := range steps {
		if gotSteps[i].ID != want.ID || gotSteps[i].Title != want.Title || gotSteps[i].Status != want.Status {
			t.Errorf("step %d mutated: got %+v, want %+v", i, gotSteps[i], want)
		}
	}
}

// TestAppendPlanSteps_RejectsCollidingID verifies a user-supplied id that
// already exists is a structured error, not a silent overwrite.
// CW-20260430-0001 (SP1).
func TestAppendPlanSteps_RejectsCollidingID(t *testing.T) {
	s := newTestStore(t)

	stepsJSON, _ := json.Marshal([]PlanStep{{ID: "s1", Title: "Existing"}})
	plan := &Plan{Scope: "workspace", Title: "Collision", Steps: string(stepsJSON)}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	_, err := s.AppendPlanSteps(context.Background(), plan.ID, []PlanStep{{ID: "s1", Title: "Duplicate"}})
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}

	// Plan must still have exactly one step (no partial write).
	got, _ := s.GetPlan(context.Background(), plan.ID)
	gotSteps, _ := got.ParsePlanSteps()
	if len(gotSteps) != 1 {
		t.Errorf("expected 1 step after rejected append, got %d", len(gotSteps))
	}
}

// TestAppendPlanSteps_PlanNotFound verifies an unknown plan_id surfaces a
// structured error (not a silent create). CW-20260430-0001 (SP1).
func TestAppendPlanSteps_PlanNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AppendPlanSteps(context.Background(), "does-not-exist", []PlanStep{{Title: "x"}})
	if err == nil {
		t.Fatal("expected error for missing plan")
	}
}

// TestAppendPlanSteps_RequiresTitle verifies steps with no title fail
// closed (no fabrication of opaque values). CW-20260430-0001 (SP1).
func TestAppendPlanSteps_RequiresTitle(t *testing.T) {
	s := newTestStore(t)
	plan := &Plan{Scope: "workspace", Title: "Title check"}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	_, err := s.AppendPlanSteps(context.Background(), plan.ID, []PlanStep{{Title: ""}})
	if err == nil {
		t.Fatal("expected title-required error")
	}
}

// TestAppendPlanSteps_FallbackUUIDWhenNoNumericIDs verifies that when the
// existing scheme is not s<n>, the auto-assignment falls back to a UUID
// rather than a guessed numeric. CW-20260430-0001 (SP1).
func TestAppendPlanSteps_FallbackUUIDWhenNoNumericIDs(t *testing.T) {
	s := newTestStore(t)

	stepsJSON, _ := json.Marshal([]PlanStep{{ID: "design-step", Title: "Design"}})
	plan := &Plan{Scope: "workspace", Title: "UUID fallback", Steps: string(stepsJSON)}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	appended, err := s.AppendPlanSteps(context.Background(), plan.ID, []PlanStep{{Title: "Implement"}})
	if err != nil {
		t.Fatalf("AppendPlanSteps: %v", err)
	}
	if appended[0].ID == "" {
		t.Fatal("expected non-empty auto-assigned id")
	}
	if len(appended[0].ID) < 8 {
		t.Errorf("expected UUID-like fallback id, got %q", appended[0].ID)
	}
}

func TestDeletePlan(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{Scope: "workspace", Title: "To delete"}
	if err := s.CreatePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	if err := s.DeletePlan(context.Background(), plan.ID); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}

	_, err := s.GetPlan(context.Background(), plan.ID)
	if err == nil {
		t.Error("expected error getting deleted plan")
	}
}
