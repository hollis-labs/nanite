package store

// TASKS/loops/02-goal-evidence-schema.md's own "Done means" requirements:
//   - GoalEvidence CRUD round-trips correctly, including a row with
//     loop_run_id IS NULL (goal-only evidence, no loop yet) and a row with
//     both loop_run_id/iteration_number set.
//   - validateGoalEvidence rejects a row missing ref_table or ref_id.
//   - EvidenceSatisfiesGoal is unit-tested against at least: zero evidence
//     (not satisfied), partial evidence covering only some acceptance
//     criteria (not satisfied), and full coverage of all four clauses
//     (satisfied) -- using a real Goal row's acceptance_criteria_json/etc.,
//     not a hardcoded stub.

import (
	"context"
	"errors"
	"testing"
)

// makeTestGoal inserts a minimal goals row (the FK target for
// goal_evidence.goal_id) and returns it.
func makeTestGoal(t *testing.T, s *Store, intent string) *Goal {
	t.Helper()
	g := &Goal{Intent: intent}
	if err := s.CreateGoal(context.Background(), g); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return g
}

// TestGoalEvidence_RoundTrip exercises Record/Get/List/Delete, including a
// row with loop_run_id IS NULL and a row with both loop_run_id and
// iteration_number set.
func TestGoalEvidence_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "goal for evidence round-trip")

	// Goal-only evidence: no loop run yet (a human acceptance note against
	// a DEFINED goal with no loop launched, per this task's own Context
	// section).
	goalOnly := &GoalEvidence{
		GoalID:       goal.ID,
		EvidenceType: GoalEvidenceTypeHumanAcceptance,
		RefTable:     "event_log",
		RefID:        "evt-123",
		Summary:      "operator signed off manually",
	}
	if err := s.RecordGoalEvidence(ctx, goalOnly); err != nil {
		t.Fatalf("RecordGoalEvidence (goal-only): %v", err)
	}
	if goalOnly.ID == "" {
		t.Fatal("RecordGoalEvidence did not populate ID")
	}
	if goalOnly.RecordedAt == "" {
		t.Fatal("RecordGoalEvidence did not populate RecordedAt")
	}

	// Loop-scoped evidence: both loop_run_id and iteration_number set. No
	// loop_runs table exists yet in this task's scope (loop_run_id is a
	// plain nullable TEXT column with no FK -- migration 137's own doc
	// comment), so any string value is accepted here.
	iteration := int64(3)
	loopScoped := &GoalEvidence{
		GoalID:          goal.ID,
		LoopRunID:       "lr_test_001",
		IterationNumber: &iteration,
		EvidenceType:    GoalEvidenceTypeVerifyResult,
		RefTable:        "workflow_run_steps",
		RefID:           "step-abc",
		Result:          "pass",
		Summary:         "loop_unit_tests_pass",
	}
	if err := s.RecordGoalEvidence(ctx, loopScoped); err != nil {
		t.Fatalf("RecordGoalEvidence (loop-scoped): %v", err)
	}

	// Get.
	gotGoalOnly, err := s.GetGoalEvidence(ctx, goalOnly.ID)
	if err != nil {
		t.Fatalf("GetGoalEvidence (goal-only): %v", err)
	}
	if gotGoalOnly.LoopRunID != "" {
		t.Errorf("goal-only evidence LoopRunID = %q, want empty", gotGoalOnly.LoopRunID)
	}
	if gotGoalOnly.IterationNumber != nil {
		t.Errorf("goal-only evidence IterationNumber = %v, want nil", gotGoalOnly.IterationNumber)
	}
	if gotGoalOnly.RefTable != "event_log" || gotGoalOnly.RefID != "evt-123" {
		t.Errorf("goal-only evidence ref pointer mismatch: %+v", gotGoalOnly)
	}

	gotLoopScoped, err := s.GetGoalEvidence(ctx, loopScoped.ID)
	if err != nil {
		t.Fatalf("GetGoalEvidence (loop-scoped): %v", err)
	}
	if gotLoopScoped.LoopRunID != "lr_test_001" {
		t.Errorf("loop-scoped evidence LoopRunID = %q, want lr_test_001", gotLoopScoped.LoopRunID)
	}
	if gotLoopScoped.IterationNumber == nil || *gotLoopScoped.IterationNumber != 3 {
		t.Errorf("loop-scoped evidence IterationNumber = %v, want 3", gotLoopScoped.IterationNumber)
	}
	if gotLoopScoped.Result != "pass" {
		t.Errorf("loop-scoped evidence Result = %q, want pass", gotLoopScoped.Result)
	}

	// List: unfiltered returns both.
	all, err := s.ListGoalEvidence(ctx, goal.ID, GoalEvidenceFilter{})
	if err != nil {
		t.Fatalf("ListGoalEvidence (unfiltered): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListGoalEvidence (unfiltered): got %d, want 2", len(all))
	}

	// List: filter by loop_run_id.
	byLoopRun, err := s.ListGoalEvidence(ctx, goal.ID, GoalEvidenceFilter{LoopRunID: "lr_test_001"})
	if err != nil {
		t.Fatalf("ListGoalEvidence (by loop_run_id): %v", err)
	}
	if len(byLoopRun) != 1 || byLoopRun[0].ID != loopScoped.ID {
		t.Fatalf("ListGoalEvidence (by loop_run_id): got %+v", byLoopRun)
	}

	// List: filter by evidence_type.
	byType, err := s.ListGoalEvidence(ctx, goal.ID, GoalEvidenceFilter{EvidenceType: GoalEvidenceTypeHumanAcceptance})
	if err != nil {
		t.Fatalf("ListGoalEvidence (by evidence_type): %v", err)
	}
	if len(byType) != 1 || byType[0].ID != goalOnly.ID {
		t.Fatalf("ListGoalEvidence (by evidence_type): got %+v", byType)
	}

	// A different goal's evidence must not leak into this goal's list.
	otherGoal := makeTestGoal(t, s, "other goal")
	if err := s.RecordGoalEvidence(ctx, &GoalEvidence{
		GoalID: otherGoal.ID, EvidenceType: GoalEvidenceTypeArtifact,
		RefTable: "event_log", RefID: "evt-other",
	}); err != nil {
		t.Fatalf("RecordGoalEvidence (other goal): %v", err)
	}
	stillTwo, err := s.ListGoalEvidence(ctx, goal.ID, GoalEvidenceFilter{})
	if err != nil {
		t.Fatalf("ListGoalEvidence after other-goal insert: %v", err)
	}
	if len(stillTwo) != 2 {
		t.Fatalf("ListGoalEvidence leaked another goal's evidence: got %d, want 2: %+v", len(stillTwo), stillTwo)
	}

	// Delete.
	if err := s.DeleteGoalEvidence(ctx, goalOnly.ID); err != nil {
		t.Fatalf("DeleteGoalEvidence: %v", err)
	}
	afterDelete, err := s.ListGoalEvidence(ctx, goal.ID, GoalEvidenceFilter{})
	if err != nil {
		t.Fatalf("ListGoalEvidence after delete: %v", err)
	}
	if len(afterDelete) != 1 {
		t.Fatalf("ListGoalEvidence after delete: got %d, want 1", len(afterDelete))
	}
	if _, err := s.GetGoalEvidence(ctx, goalOnly.ID); !errors.Is(err, ErrGoalEvidenceNotFound) {
		t.Errorf("post-delete GetGoalEvidence: got %v, want ErrGoalEvidenceNotFound", err)
	}
	if err := s.DeleteGoalEvidence(ctx, goalOnly.ID); !errors.Is(err, ErrGoalEvidenceNotFound) {
		t.Errorf("DeleteGoalEvidence (already deleted): got %v, want ErrGoalEvidenceNotFound", err)
	}
	if err := s.DeleteGoalEvidence(ctx, "does-not-exist"); !errors.Is(err, ErrGoalEvidenceNotFound) {
		t.Errorf("DeleteGoalEvidence (unknown id): got %v, want ErrGoalEvidenceNotFound", err)
	}
}

// TestGoalEvidence_ListEmpty confirms a goal with no recorded evidence
// returns an empty, non-nil slice, not an error.
func TestGoalEvidence_ListEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "goal with no evidence")

	evidence, err := s.ListGoalEvidence(ctx, goal.ID, GoalEvidenceFilter{})
	if err != nil {
		t.Fatalf("ListGoalEvidence: %v", err)
	}
	if evidence == nil {
		t.Error("ListGoalEvidence: expected non-nil empty slice")
	}
	if len(evidence) != 0 {
		t.Errorf("ListGoalEvidence: got %d rows, want 0", len(evidence))
	}
}

// TestGoalEvidence_GoalFKEnforced confirms goal_id is a real, enforced FK.
func TestGoalEvidence_GoalFKEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.RecordGoalEvidence(ctx, &GoalEvidence{
		GoalID: "does-not-exist", EvidenceType: GoalEvidenceTypeArtifact,
		RefTable: "event_log", RefID: "evt-1",
	})
	if err == nil {
		t.Fatal("RecordGoalEvidence with unknown goal_id: expected FK violation, got nil")
	}
}

// TestGoalEvidence_Validation confirms validateGoalEvidence rejects a row
// missing goal_id, an invalid evidence_type, or a missing ref_table/ref_id
// -- this task's own explicit "Done means" requirement ("validateGoalEvidence
// rejects a row missing ref_table or ref_id").
func TestGoalEvidence_Validation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "goal for validation")

	cases := []struct {
		name     string
		evidence GoalEvidence
	}{
		{"missing goal_id", GoalEvidence{EvidenceType: GoalEvidenceTypeArtifact, RefTable: "event_log", RefID: "evt-1"}},
		{"invalid evidence_type", GoalEvidence{GoalID: goal.ID, EvidenceType: "not-a-real-type", RefTable: "event_log", RefID: "evt-1"}},
		{"missing ref_table", GoalEvidence{GoalID: goal.ID, EvidenceType: GoalEvidenceTypeArtifact, RefID: "evt-1"}},
		{"missing ref_id", GoalEvidence{GoalID: goal.ID, EvidenceType: GoalEvidenceTypeArtifact, RefTable: "event_log"}},
		{"missing both ref_table and ref_id", GoalEvidence{GoalID: goal.ID, EvidenceType: GoalEvidenceTypeArtifact}},
	}
	for _, c := range cases {
		e := c.evidence
		if err := s.RecordGoalEvidence(ctx, &e); err == nil {
			t.Errorf("RecordGoalEvidence (%s): expected error, got nil (row: %+v)", c.name, c.evidence)
		}
	}
}

// TestGoalEvidence_CheckConstraint confirms migration 137's DB-level CHECK
// constraint rejects an invalid evidence_type even bypassing Go-layer
// validation (a raw INSERT), mirroring goals_test.go's
// TestGoal_StatusCheckConstraint precedent.
func TestGoalEvidence_CheckConstraint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := makeTestGoal(t, s, "goal for check constraint")

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO goal_evidence (id, goal_id, evidence_type, ref_table, ref_id)
		 VALUES (?, ?, ?, ?, ?)`,
		"raw-bad-evidence-type", goal.ID, "not-a-real-type", "event_log", "evt-1",
	)
	if err == nil {
		t.Fatal("raw INSERT with invalid evidence_type: expected CHECK violation, got nil")
	}
}

// --- EvaluateGoalEvidence / EvidenceSatisfiesGoal -----------------------

// goalWithCriteria creates a goal with the given acceptance criteria,
// constraints, and invariants lists -- the real Goal row
// EvaluateGoalEvidence reads from, per this task's own "Done means"
// requirement ("using a real Goal row's acceptance_criteria_json/etc., not
// a hardcoded stub").
func goalWithCriteria(t *testing.T, s *Store, acceptance, constraints, invariants []string) *Goal {
	t.Helper()
	g := &Goal{Intent: "goal for evidence-walk evaluation"}
	if err := g.SetAcceptanceCriteria(acceptance); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := g.SetConstraints(constraints); err != nil {
		t.Fatalf("SetConstraints: %v", err)
	}
	if err := g.SetInvariants(invariants); err != nil {
		t.Fatalf("SetInvariants: %v", err)
	}
	if err := s.CreateGoal(context.Background(), g); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return g
}

// TestEvidenceSatisfiesGoal_ZeroEvidence confirms a goal with real
// acceptance criteria and zero recorded evidence is not satisfied.
func TestEvidenceSatisfiesGoal_ZeroEvidence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := goalWithCriteria(t, s,
		[]string{"loop_unit_tests_pass", "ralph_integration_test_passes"},
		[]string{"existing agentworkflow API remains backwards compatible"},
		[]string{"agentworkflow stays DAG-only, no cycles"},
	)

	met, evidence, err := s.EvidenceSatisfiesGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvidenceSatisfiesGoal: %v", err)
	}
	if met {
		t.Error("EvidenceSatisfiesGoal: got true, want false for zero recorded evidence")
	}
	if len(evidence) != 0 {
		t.Errorf("EvidenceSatisfiesGoal: got %d evidence rows, want 0", len(evidence))
	}

	eval, _, err := s.EvaluateGoalEvidence(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvaluateGoalEvidence: %v", err)
	}
	if eval.AcceptanceCriteriaSatisfied {
		t.Error("AcceptanceCriteriaSatisfied: got true, want false for zero evidence")
	}
	if eval.RequiredEvidencePresent {
		t.Error("RequiredEvidencePresent: got true, want false for zero evidence")
	}
	if len(eval.MissingAcceptanceCriteria) != 2 {
		t.Errorf("MissingAcceptanceCriteria: got %v, want both criteria listed", eval.MissingAcceptanceCriteria)
	}
}

// TestEvidenceSatisfiesGoal_PartialCoverage confirms a goal with evidence
// covering only some of its acceptance criteria is not satisfied -- this
// task's own explicit "Done means" scenario.
func TestEvidenceSatisfiesGoal_PartialCoverage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := goalWithCriteria(t, s,
		[]string{"loop_unit_tests_pass", "ralph_integration_test_passes"},
		nil, nil,
	)

	// Only one of the two acceptance criteria gets matching evidence.
	if err := s.RecordGoalEvidence(ctx, &GoalEvidence{
		GoalID: goal.ID, EvidenceType: GoalEvidenceTypeTestSuite,
		RefTable: "workflow_run_steps", RefID: "step-1",
		Result: "pass", Summary: "loop_unit_tests_pass",
	}); err != nil {
		t.Fatalf("RecordGoalEvidence: %v", err)
	}

	met, _, err := s.EvidenceSatisfiesGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvidenceSatisfiesGoal: %v", err)
	}
	if met {
		t.Error("EvidenceSatisfiesGoal: got true, want false for partial acceptance-criteria coverage")
	}

	eval, _, err := s.EvaluateGoalEvidence(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvaluateGoalEvidence: %v", err)
	}
	if eval.AcceptanceCriteriaSatisfied {
		t.Error("AcceptanceCriteriaSatisfied: got true, want false for partial coverage")
	}
	if len(eval.MissingAcceptanceCriteria) != 1 || eval.MissingAcceptanceCriteria[0] != "ralph_integration_test_passes" {
		t.Errorf("MissingAcceptanceCriteria: got %v, want [ralph_integration_test_passes]", eval.MissingAcceptanceCriteria)
	}
	// Constraints/invariants are both empty lists on this goal -- vacuously
	// satisfied regardless of the missing acceptance-criteria coverage.
	if !eval.ConstraintsSatisfied || !eval.InvariantsPreserved {
		t.Errorf("empty constraints/invariants should be vacuously satisfied: %+v", eval)
	}
	if eval.GoalMet {
		t.Error("GoalMet: got true, want false -- acceptance criteria clause alone must gate goal_met")
	}
}

// TestEvidenceSatisfiesGoal_FullCoverage confirms a goal with matching
// evidence for every acceptance criterion, constraint, and invariant is
// satisfied -- this task's own explicit "Done means" scenario ("full
// coverage of all four clauses (satisfied)").
func TestEvidenceSatisfiesGoal_FullCoverage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goal := goalWithCriteria(t, s,
		[]string{"loop_unit_tests_pass", "ralph_integration_test_passes"},
		[]string{"existing agentworkflow API remains backwards compatible"},
		[]string{"agentworkflow stays DAG-only, no cycles"},
	)

	items := []GoalEvidence{
		{EvidenceType: GoalEvidenceTypeTestSuite, RefTable: "workflow_run_steps", RefID: "step-1", Result: "pass", Summary: "loop_unit_tests_pass"},
		{EvidenceType: GoalEvidenceTypeTestSuite, RefTable: "workflow_run_steps", RefID: "step-2", Result: "pass", Summary: "ralph_integration_test_passes"},
		{EvidenceType: GoalEvidenceTypeVerifyResult, RefTable: "workflow_run_steps", RefID: "step-3", Result: "pass", Summary: "existing agentworkflow API remains backwards compatible"},
		{EvidenceType: GoalEvidenceTypeVerifyResult, RefTable: "workflow_run_steps", RefID: "step-4", Result: "pass", Summary: "agentworkflow stays DAG-only, no cycles"},
	}
	for i := range items {
		items[i].GoalID = goal.ID
		if err := s.RecordGoalEvidence(ctx, &items[i]); err != nil {
			t.Fatalf("RecordGoalEvidence (%d): %v", i, err)
		}
	}

	met, evidence, err := s.EvidenceSatisfiesGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvidenceSatisfiesGoal: %v", err)
	}
	if !met {
		t.Error("EvidenceSatisfiesGoal: got false, want true for full four-clause coverage")
	}
	if len(evidence) != 4 {
		t.Errorf("EvidenceSatisfiesGoal: got %d evidence rows, want 4", len(evidence))
	}

	eval, _, err := s.EvaluateGoalEvidence(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvaluateGoalEvidence: %v", err)
	}
	if !eval.AcceptanceCriteriaSatisfied || !eval.ConstraintsSatisfied || !eval.InvariantsPreserved || !eval.RequiredEvidencePresent {
		t.Errorf("expected all four clauses satisfied: %+v", eval)
	}
	if !eval.GoalMet {
		t.Error("GoalMet: got false, want true")
	}
	if len(eval.MissingAcceptanceCriteria) != 0 || len(eval.MissingConstraints) != 0 || len(eval.MissingInvariants) != 0 {
		t.Errorf("expected no missing items: %+v", eval)
	}
}

// TestEvidenceSatisfiesGoal_UnknownGoal confirms EvaluateGoalEvidence/
// EvidenceSatisfiesGoal surface ErrGoalNotFound for a goal_id that does not
// resolve to a real goals row, rather than silently reporting satisfied.
func TestEvidenceSatisfiesGoal_UnknownGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, _, err := s.EvidenceSatisfiesGoal(ctx, "does-not-exist"); !errors.Is(err, ErrGoalNotFound) {
		t.Errorf("EvidenceSatisfiesGoal (unknown goal): got %v, want ErrGoalNotFound", err)
	}
}
