package service

// TASKS/loops/11-loop-event-predicate-trigger.md -- unit coverage for
// EvaluateLoopRunResumeReflexes: the trigger-eval-then-fire contract, the
// waiting_on_escalation guard, and resumer-error propagation. The genuine
// end-to-end test (a real *loop.LoopEngine as the LoopRunResumer, driving
// a real LoopRun out of waiting_on_escalation) lives in
// internal/loop/reflex_resume_test.go -- package internal/loop already
// imports internal/service, so it can build a real LoopEngine and pass it
// here as the resumer; this package cannot import internal/loop back
// (that's the whole reason LoopRunResumer is an interface).

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeLoopRunResumer records every ResumeLoopRun call it receives —
// standing in for a real *loop.LoopEngine in this package's own unit
// tests (which cannot import internal/loop without cycling).
type fakeLoopRunResumer struct {
	calls []string
	err   error
}

func (f *fakeLoopRunResumer) ResumeLoopRun(_ context.Context, loopRunID string) error {
	f.calls = append(f.calls, loopRunID)
	return f.err
}

func newTestReflexEngineForLoopResume(t *testing.T, st *store.Store) *reflexes.Engine {
	t.Helper()
	return reflexes.NewEngine(st, slog.Default())
}

func createTestWaitingLoopRun(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	goal := store.Goal{Intent: "an external condition must become true"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	lr := &store.LoopRun{GoalID: goal.ID, DefinitionName: "resume-loop-run-test"}
	if err := lr.SetBudget(store.Budget{}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := st.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	if err := st.UpdateLoopRunStatus(ctx, lr.ID, store.LoopRunStatusWaitingOnEscalation, nil); err != nil {
		t.Fatalf("UpdateLoopRunStatus(waiting_on_escalation): %v", err)
	}
	return lr.ID
}

func insertResumeLoopRunReflex(t *testing.T, st *store.Store, loopRunID string) string {
	t.Helper()
	id, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		Name:        "resume-" + loopRunID,
		ClassTag:    "process",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"external_check_passed"}`,
		ActionKind:  store.ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"` + loopRunID + `"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run): %v", err)
	}
	return id
}

// TestEvaluateLoopRunResumeReflexes_TriggerFalse_DoesNotResume confirms
// the trigger-spec AST is genuinely evaluated (EvaluateTrigger, unchanged)
// -- a candidate whose event has not occurred yet must not call Resume.
func TestEvaluateLoopRunResumeReflexes_TriggerFalse_DoesNotResume(t *testing.T) {
	st := newTestStore(t)
	loopRunID := createTestWaitingLoopRun(t, st)
	insertResumeLoopRunReflex(t, st, loopRunID)
	engine := newTestReflexEngineForLoopResume(t, st)
	resumer := &fakeLoopRunResumer{}

	fired, hadCandidates, err := EvaluateLoopRunResumeReflexes(context.Background(), engine, resumer, loopRunID, reflexes.State{
		SessionID: "", // no live chat session -- the whole point of this trigger kind
	})
	if err != nil {
		t.Fatalf("EvaluateLoopRunResumeReflexes: %v", err)
	}
	if fired {
		t.Error("fired = true, want false (event has not occurred)")
	}
	if !hadCandidates {
		t.Error("hadCandidates = false, want true (a resume_loop_run reflex is attached, it just hasn't fired)")
	}
	if len(resumer.calls) != 0 {
		t.Errorf("resumer.calls = %v, want none", resumer.calls)
	}
}

// TestEvaluateLoopRunResumeReflexes_TriggerTrue_CallsResume is the
// positive case: once the event appears in State.Events, Resolve() fires
// the candidate and EvaluateLoopRunResumeReflexes calls
// resumer.ResumeLoopRun with the loop_run_id parsed from the fired
// reflex's own action_spec.
func TestEvaluateLoopRunResumeReflexes_TriggerTrue_CallsResume(t *testing.T) {
	st := newTestStore(t)
	loopRunID := createTestWaitingLoopRun(t, st)
	reflexID := insertResumeLoopRunReflex(t, st, loopRunID)
	engine := newTestReflexEngineForLoopResume(t, st)
	resumer := &fakeLoopRunResumer{}

	fired, hadCandidates, err := EvaluateLoopRunResumeReflexes(context.Background(), engine, resumer, loopRunID, reflexes.State{
		Events: []reflexes.EventSignal{{EventType: "external_check_passed", Category: "test"}},
	})
	if err != nil {
		t.Fatalf("EvaluateLoopRunResumeReflexes: %v", err)
	}
	if !fired {
		t.Fatal("fired = false, want true (event occurred)")
	}
	if !hadCandidates {
		t.Error("hadCandidates = false, want true")
	}
	if len(resumer.calls) != 1 || resumer.calls[0] != loopRunID {
		t.Fatalf("resumer.calls = %v, want exactly [%s]", resumer.calls, loopRunID)
	}

	// Unified telemetry (EmitFirings) must have bumped fired_count -- the
	// same contract every other real Resolve() caller upholds.
	row, err := st.GetAgentReflex(context.Background(), reflexID)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if row.FiredCount != 1 {
		t.Errorf("row.FiredCount = %d, want 1", row.FiredCount)
	}
}

// TestEvaluateLoopRunResumeReflexes_NotWaitingOnEscalation_IsANoOp
// confirms the status guard: a LoopRun that is not currently
// waiting_on_escalation (e.g. still running, or already resumed past it)
// must not have its resume_loop_run reflexes evaluated at all, even if a
// candidate's trigger would otherwise fire.
func TestEvaluateLoopRunResumeReflexes_NotWaitingOnEscalation_IsANoOp(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	goal := store.Goal{Intent: "still running"}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	lr := &store.LoopRun{GoalID: goal.ID, DefinitionName: "resume-loop-run-not-waiting-test"}
	if err := lr.SetBudget(store.Budget{}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := st.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	// lr.Status is "running" (CreateLoopRun's own default) -- deliberately
	// NOT transitioned to waiting_on_escalation.
	insertResumeLoopRunReflex(t, st, lr.ID)
	engine := newTestReflexEngineForLoopResume(t, st)
	resumer := &fakeLoopRunResumer{}

	fired, hadCandidates, err := EvaluateLoopRunResumeReflexes(ctx, engine, resumer, lr.ID, reflexes.State{
		Events: []reflexes.EventSignal{{EventType: "external_check_passed"}},
	})
	if err != nil {
		t.Fatalf("EvaluateLoopRunResumeReflexes: %v", err)
	}
	if fired {
		t.Error("fired = true, want false (loop run is not waiting_on_escalation)")
	}
	if hadCandidates {
		t.Error("hadCandidates = true, want false (the status guard returns before candidates are even listed)")
	}
	if len(resumer.calls) != 0 {
		t.Errorf("resumer.calls = %v, want none", resumer.calls)
	}
}

// TestEvaluateLoopRunResumeReflexes_ResumerError_IsSurfaced confirms a
// LoopEngine.Resume failure (surfaced through the LoopRunResumer
// interface) is returned as an error, not silently swallowed --
// distinct from a per-candidate Executor.Apply failure (which Resolve()
// itself already logs-and-continues on).
func TestEvaluateLoopRunResumeReflexes_ResumerError_IsSurfaced(t *testing.T) {
	st := newTestStore(t)
	loopRunID := createTestWaitingLoopRun(t, st)
	insertResumeLoopRunReflex(t, st, loopRunID)
	engine := newTestReflexEngineForLoopResume(t, st)
	wantErr := errors.New("boom")
	resumer := &fakeLoopRunResumer{err: wantErr}

	fired, hadCandidates, err := EvaluateLoopRunResumeReflexes(context.Background(), engine, resumer, loopRunID, reflexes.State{
		Events: []reflexes.EventSignal{{EventType: "external_check_passed"}},
	})
	if !fired {
		t.Error("fired = false, want true (the candidate did fire; the resumer call is what failed)")
	}
	if !hadCandidates {
		t.Error("hadCandidates = false, want true")
	}
	if err == nil {
		t.Fatal("err = nil, want the resumer's error to be surfaced")
	}
	if len(resumer.calls) != 1 {
		t.Fatalf("resumer.calls = %v, want exactly one attempt", resumer.calls)
	}
}

// TestEvaluateLoopRunResumeReflexes_NoCandidates_IsANoOp confirms a
// waiting_on_escalation LoopRun with zero resume_loop_run reflexes
// attached is a clean no-op, not an error.
func TestEvaluateLoopRunResumeReflexes_NoCandidates_IsANoOp(t *testing.T) {
	st := newTestStore(t)
	loopRunID := createTestWaitingLoopRun(t, st)
	engine := newTestReflexEngineForLoopResume(t, st)
	resumer := &fakeLoopRunResumer{}

	fired, hadCandidates, err := EvaluateLoopRunResumeReflexes(context.Background(), engine, resumer, loopRunID, reflexes.State{})
	if err != nil {
		t.Fatalf("EvaluateLoopRunResumeReflexes: %v", err)
	}
	if fired {
		t.Error("fired = true, want false (no candidates)")
	}
	if hadCandidates {
		t.Error("hadCandidates = true, want false (zero resume_loop_run reflexes attached)")
	}
}

// TestEvaluateLoopRunResumeReflexes_ArgumentGuards covers the defensive
// nil/empty-string checks.
func TestEvaluateLoopRunResumeReflexes_ArgumentGuards(t *testing.T) {
	st := newTestStore(t)
	engine := newTestReflexEngineForLoopResume(t, st)
	resumer := &fakeLoopRunResumer{}
	ctx := context.Background()

	if _, _, err := EvaluateLoopRunResumeReflexes(ctx, nil, resumer, "lr-1", reflexes.State{}); err == nil {
		t.Error("nil reflexEngine: want error, got nil")
	}
	if _, _, err := EvaluateLoopRunResumeReflexes(ctx, engine, nil, "lr-1", reflexes.State{}); err == nil {
		t.Error("nil resumer: want error, got nil")
	}
	if _, _, err := EvaluateLoopRunResumeReflexes(ctx, engine, resumer, "", reflexes.State{}); err == nil {
		t.Error("empty loop_run_id: want error, got nil")
	}
}
