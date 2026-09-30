package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestLoopServicePatchGoalAppliesDefinitionAndStatus(t *testing.T) {
	ctx := context.Background()
	svc := NewLoopService(newConfigTestStore(t))

	g, err := svc.CreateGoal(ctx, GoalInput{Intent: "ship it", DesiredState: []string{"a"}})
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	intent := "ship it well"
	criteria := []string{"tests pass"}
	status := store.GoalStatusDefined
	got, err := svc.PatchGoal(ctx, g.ID, GoalPatch{Intent: &intent, AcceptanceCriteria: &criteria, Status: &status})
	if err != nil {
		t.Fatalf("PatchGoal: %v", err)
	}
	if got.Intent != intent || got.Status != status || got.AcceptanceCriteriaJSON != `["tests pass"]` {
		t.Fatalf("patched = %+v", got)
	}
	if got.DesiredStateJSON != g.DesiredStateJSON {
		t.Fatalf("untouched desired_state changed: %q -> %q", g.DesiredStateJSON, got.DesiredStateJSON)
	}
}

// The definition write and the status write are separate statements: a
// rejected status leaves an accepted definition change in place.
func TestLoopServicePatchGoalIsTwoStatements(t *testing.T) {
	ctx := context.Background()
	svc := NewLoopService(newConfigTestStore(t))

	g, err := svc.CreateGoal(ctx, GoalInput{Intent: "before"})
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	intent := "after"
	bad := "not-a-status"
	_, err = svc.PatchGoal(ctx, g.ID, GoalPatch{Intent: &intent, Status: &bad})
	var writeErr *GoalWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("err = %v, want *GoalWriteError", err)
	}
	saved, err := svc.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if saved.Intent != "after" {
		t.Fatalf("intent = %q, want the definition write kept", saved.Intent)
	}
}

func TestLoopServiceNotFoundSentinels(t *testing.T) {
	ctx := context.Background()
	svc := NewLoopService(newConfigTestStore(t))

	if _, err := svc.PatchGoal(ctx, "missing", GoalPatch{}); !errors.Is(err, store.ErrGoalNotFound) {
		t.Fatalf("PatchGoal err = %v", err)
	}
	if err := svc.DeleteGoal(ctx, "missing"); !errors.Is(err, store.ErrGoalNotFound) {
		t.Fatalf("DeleteGoal err = %v", err)
	}
	if _, err := svc.ListGoalEvidence(ctx, "missing", store.GoalEvidenceFilter{}); !errors.Is(err, store.ErrGoalNotFound) {
		t.Fatalf("ListGoalEvidence err = %v", err)
	}
	if _, err := svc.ListLoopRunIterations(ctx, "missing"); !errors.Is(err, store.ErrLoopRunNotFound) {
		t.Fatalf("ListLoopRunIterations err = %v", err)
	}
}

func TestLoopServiceCreateGoalRejectsMissingIntent(t *testing.T) {
	_, err := NewLoopService(newConfigTestStore(t)).CreateGoal(context.Background(), GoalInput{})
	var writeErr *GoalWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("err = %v, want *GoalWriteError", err)
	}
}
