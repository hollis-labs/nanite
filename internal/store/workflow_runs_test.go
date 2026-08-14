package store

import (
	"errors"
	"testing"
)

func TestWorkflowRun_CreateGetSetStatus(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetWorkflowRun("missing"); !errors.Is(err, ErrWorkflowRunNotFound) {
		t.Fatalf("GetWorkflowRun(missing) = %v, want ErrWorkflowRunNotFound", err)
	}

	if err := s.CreateWorkflowRun(&WorkflowRunRow{ID: "run-1", DefinitionName: "demo", InputJSON: `{"task_id":"T-1"}`}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	row, err := s.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if row.Status != "running" || row.DefinitionName != "demo" || row.InputJSON != `{"task_id":"T-1"}` {
		t.Fatalf("row = %+v", row)
	}
	if row.StartedAt.IsZero() {
		t.Fatal("StartedAt not set")
	}

	if err := s.SetWorkflowRunStatus("run-1", "completed", "", row.StartedAt); err != nil {
		t.Fatalf("SetWorkflowRunStatus: %v", err)
	}
	row, err = s.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun after status set: %v", err)
	}
	if row.Status != "completed" {
		t.Fatalf("Status = %q, want completed", row.Status)
	}
	if row.CompletedAt.IsZero() {
		t.Fatal("CompletedAt not set after SetWorkflowRunStatus")
	}
}

func TestWorkflowRunStep_UpsertTransitionsAndList(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateWorkflowRun(&WorkflowRunRow{ID: "run-1", DefinitionName: "demo"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// Pre-register pending.
	if err := s.UpsertWorkflowRunStep(&WorkflowRunStepRow{
		WorkflowRunID: "run-1", StepID: "fetch", Kind: "tool", Status: "pending",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep (pending): %v", err)
	}

	steps, err := s.ListWorkflowRunSteps("run-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	if len(steps) != 1 || steps[0].Status != "pending" {
		t.Fatalf("steps = %+v", steps)
	}

	// Transition to running, then completed with output — same synthetic id.
	if err := s.UpsertWorkflowRunStep(&WorkflowRunStepRow{
		WorkflowRunID: "run-1", StepID: "fetch", Kind: "tool", Status: "running",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep (running): %v", err)
	}
	if err := s.UpsertWorkflowRunStep(&WorkflowRunStepRow{
		WorkflowRunID: "run-1", StepID: "fetch", Kind: "tool", Status: "completed",
		Output: "fetched-data", ToolCallsJSON: `[{"tool":"torque_task_get"}]`,
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep (completed): %v", err)
	}

	steps, err = s.ListWorkflowRunSteps("run-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps after completion: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1 (upsert must not duplicate rows)", len(steps))
	}
	if steps[0].Status != "completed" || steps[0].Output != "fetched-data" {
		t.Fatalf("steps[0] = %+v", steps[0])
	}
	if steps[0].ToolCallsJSON != `[{"tool":"torque_task_get"}]` {
		t.Fatalf("ToolCallsJSON = %q", steps[0].ToolCallsJSON)
	}
}

func TestWorkflowRunStep_MultipleStepsPreserveOrder(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateWorkflowRun(&WorkflowRunRow{ID: "run-1", DefinitionName: "demo"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := s.UpsertWorkflowRunStep(&WorkflowRunStepRow{WorkflowRunID: "run-1", StepID: id, Kind: "tool"}); err != nil {
			t.Fatalf("UpsertWorkflowRunStep(%s): %v", id, err)
		}
	}
	steps, err := s.ListWorkflowRunSteps("run-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("len(steps) = %d, want 3", len(steps))
	}
	for i, want := range []string{"a", "b", "c"} {
		if steps[i].StepID != want {
			t.Fatalf("steps[%d].StepID = %q, want %q", i, steps[i].StepID, want)
		}
	}
}
