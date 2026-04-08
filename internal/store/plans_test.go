package store

import (
	"encoding/json"
	"testing"
)

func TestCreatePlan(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{
		Scope: "project",
		ScopeID: "proj-1",
		Title: "Refactoring plan",
	}
	if err := s.CreatePlan(plan); err != nil {
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
	if err := s.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := s.GetPlan(plan.ID)
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
		if err := s.CreatePlan(plan); err != nil {
			t.Fatalf("CreatePlan %s: %v", p.title, err)
		}
	}

	plans, err := s.ListPlans(PlanFilter{Scope: "workspace"})
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) != 2 {
		t.Errorf("expected 2 workspace plans, got %d", len(plans))
	}

	plans, err = s.ListPlans(PlanFilter{Status: "proposed"})
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
	if err := s.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	plan.Title = "Updated"
	plan.Status = "approved"
	if err := s.UpdatePlan(plan); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	got, err := s.GetPlan(plan.ID)
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
	if err := s.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	// Update step-1 status.
	if err := s.UpdatePlanStep(plan.ID, "step-1", PlanStep{Status: "done"}); err != nil {
		t.Fatalf("UpdatePlanStep: %v", err)
	}

	got, err := s.GetPlan(plan.ID)
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
	if err := s.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	err := s.UpdatePlanStep(plan.ID, "nonexistent", PlanStep{Status: "done"})
	if err == nil {
		t.Error("expected error for nonexistent step")
	}
}

func TestDeletePlan(t *testing.T) {
	s := newTestStore(t)

	plan := &Plan{Scope: "workspace", Title: "To delete"}
	if err := s.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	if err := s.DeletePlan(plan.ID); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}

	_, err := s.GetPlan(plan.ID)
	if err == nil {
		t.Error("expected error getting deleted plan")
	}
}
