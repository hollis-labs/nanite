package loop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeStepExecutor is a scriptable agentworkflow.StepExecutor test double --
// mirrors internal/service/workflow_engine_test.go's own fakeStepExecutor
// shape (package-private there, so re-declared here rather than imported).
// ExecuteToolStep/Verify are never expected to be called by Decide (it only
// ever calls ExecuteLLMStep, per decideByReasoning) and error loudly if they
// are, to catch an accidental future call into either.
type fakeStepExecutor struct {
	calls   int
	lastReq agentworkflow.LLMStepRequest
	llmFunc func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error)
}

func (f *fakeStepExecutor) ExecuteLLMStep(_ context.Context, req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	f.calls++
	f.lastReq = req
	if f.llmFunc != nil {
		return f.llmFunc(req)
	}
	return agentworkflow.LLMStepResult{}, nil
}

func (f *fakeStepExecutor) ExecuteToolStep(_ context.Context, _ agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	return agentworkflow.ToolStepResult{}, fmt.Errorf("fakeStepExecutor: unexpected ExecuteToolStep call")
}

func (f *fakeStepExecutor) Verify(_ context.Context, _ agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{}, fmt.Errorf("fakeStepExecutor: unexpected Verify call")
}

var _ agentworkflow.StepExecutor = (*fakeStepExecutor)(nil)

func newTestGoal(t *testing.T, acceptance, constraints, invariants []string) Goal {
	t.Helper()
	g := Goal{ID: "g1", Intent: "test goal"}
	if err := g.SetAcceptanceCriteria(acceptance); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := g.SetConstraints(constraints); err != nil {
		t.Fatalf("SetConstraints: %v", err)
	}
	if err := g.SetInvariants(invariants); err != nil {
		t.Fatalf("SetInvariants: %v", err)
	}
	return g
}

// --- Branch 1: goal_met -> COMPLETE ---

func TestDecide_GoalMet_ReturnsComplete(t *testing.T) {
	goal := newTestGoal(t, []string{"tests pass"}, nil, nil)
	evidence := []GoalEvidence{
		{GoalID: "g1", EvidenceType: store.GoalEvidenceTypeTestSuite, RefTable: "workflow_run_steps", RefID: "s1", Summary: "tests pass"},
	}
	exec := &fakeStepExecutor{}

	d, err := Decide(context.Background(), exec, goal, evidence, Evaluation{}, nil, Budget{MaxIterations: 10}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionComplete {
		t.Fatalf("expected DecisionComplete, got %+v", d)
	}
	if exec.calls != 0 {
		t.Fatalf("expected zero LLM calls for the goal-met branch, got %d", exec.calls)
	}
}

func TestDecide_GoalMet_PartialEvidence_DoesNotComplete(t *testing.T) {
	goal := newTestGoal(t, []string{"tests pass", "docs updated"}, nil, nil)
	// Only one of two acceptance criteria has matching evidence.
	evidence := []GoalEvidence{
		{GoalID: "g1", EvidenceType: store.GoalEvidenceTypeTestSuite, RefTable: "workflow_run_steps", RefID: "s1", Summary: "tests pass"},
	}
	exec := &fakeStepExecutor{}

	d, err := Decide(context.Background(), exec, goal, evidence, Evaluation{}, nil, Budget{MaxIterations: 10, MaxNoProgressIterations: 10}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind == DecisionComplete {
		t.Fatalf("expected partial evidence to NOT satisfy the goal, got %+v", d)
	}
}

func TestDecide_GoalMet_NoEvidenceAtAll_DoesNotComplete(t *testing.T) {
	// Vacuously-empty acceptance/constraints/invariants lists, but zero
	// evidence rows recorded -- required_evidence_present must still fail.
	goal := newTestGoal(t, nil, nil, nil)
	exec := &fakeStepExecutor{}

	d, err := Decide(context.Background(), exec, goal, nil, Evaluation{}, nil, Budget{MaxIterations: 10, MaxNoProgressIterations: 10}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind == DecisionComplete {
		t.Fatalf("expected zero evidence rows to NOT satisfy the goal, got %+v", d)
	}
}

// --- Branch 2: budget exhausted -> ESCALATE | FAIL ---

func unmetGoal(t *testing.T) Goal {
	t.Helper()
	return newTestGoal(t, []string{"unmet criterion"}, nil, nil)
}

func TestDecide_BudgetExhausted_MaxIterations_Escalate(t *testing.T) {
	history := make([]IterationResult, 5)
	for i := range history {
		history[i] = IterationResult{IterationNumber: i + 1, ProgressState: store.LoopRunIterationProgressProgress}
	}
	exec := &fakeStepExecutor{}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxIterations: 5, OnExhausted: store.LoopRunOnExhaustedEscalate}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionEscalate {
		t.Fatalf("expected DecisionEscalate, got %+v", d)
	}
	if exec.calls != 0 {
		t.Fatalf("expected zero LLM calls for the budget-exhausted branch, got %d", exec.calls)
	}
}

func TestDecide_BudgetExhausted_MaxIterations_Fail(t *testing.T) {
	history := make([]IterationResult, 5)
	for i := range history {
		history[i] = IterationResult{IterationNumber: i + 1, ProgressState: store.LoopRunIterationProgressProgress}
	}

	d, err := Decide(context.Background(), &fakeStepExecutor{}, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxIterations: 5, OnExhausted: store.LoopRunOnExhaustedFail}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionFail {
		t.Fatalf("expected DecisionFail, got %+v", d)
	}
}

func TestDecide_BudgetExhausted_DefaultsOnExhaustedToEscalate(t *testing.T) {
	history := []IterationResult{{IterationNumber: 1, ProgressState: store.LoopRunIterationProgressProgress}}

	// OnExhausted left unset ("") -- SetBudget's own "default escalate"
	// convention (internal/store/loop_runs.go) applies here too.
	d, err := Decide(context.Background(), &fakeStepExecutor{}, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxIterations: 1}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionEscalate {
		t.Fatalf("expected an unset OnExhausted to default to DecisionEscalate, got %+v", d)
	}
}

func TestDecide_BudgetNotExhausted_ZeroMaxIterationsMeansUnbounded(t *testing.T) {
	history := make([]IterationResult, 100)
	for i := range history {
		history[i] = IterationResult{IterationNumber: i + 1, ProgressState: store.LoopRunIterationProgressProgress}
	}

	// Budget{} zero value: MaxIterations == 0 must mean "no cap," not
	// "already exhausted at iteration 1."
	d, err := Decide(context.Background(), &fakeStepExecutor{}, unmetGoal(t), nil, Evaluation{}, history, Budget{}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionContinue {
		t.Fatalf("expected DecisionContinue with an unset Budget, got %+v", d)
	}
}

func TestDecide_BudgetExhausted_MaxFailures(t *testing.T) {
	history := []IterationResult{
		{IterationNumber: 1, ProgressState: store.LoopRunIterationProgressRegression},
		{IterationNumber: 2, ProgressState: store.LoopRunIterationProgressRegression},
	}

	d, err := Decide(context.Background(), &fakeStepExecutor{}, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxFailures: 2, OnExhausted: store.LoopRunOnExhaustedEscalate}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionEscalate {
		t.Fatalf("expected DecisionEscalate once regression count reaches max_failures, got %+v", d)
	}
}

func TestDecide_BudgetExhausted_MaxRuntimeSeconds(t *testing.T) {
	start := time.Now().UTC().Add(-2 * time.Hour)
	history := []IterationResult{
		{
			IterationNumber: 1,
			ProgressState:   store.LoopRunIterationProgressProgress,
			StartedAt:       start.Format(time.RFC3339),
			CompletedAt:     start.Add(90 * time.Minute).Format(time.RFC3339),
		},
	}

	d, err := Decide(context.Background(), &fakeStepExecutor{}, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxRuntimeSeconds: 60 * 60, OnExhausted: store.LoopRunOnExhaustedFail}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionFail {
		t.Fatalf("expected DecisionFail once elapsed runtime reaches max_runtime_seconds, got %+v", d)
	}
}

// --- Branch 4: otherwise -> CONTINUE ---

func TestDecide_NoTerminalCondition_ReturnsContinue(t *testing.T) {
	history := []IterationResult{
		{IterationNumber: 1, ProgressState: store.LoopRunIterationProgressProgress},
	}
	exec := &fakeStepExecutor{}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxIterations: 20, MaxNoProgressIterations: 3}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionContinue {
		t.Fatalf("expected DecisionContinue, got %+v", d)
	}
	if exec.calls != 0 {
		t.Fatalf("expected zero LLM calls for the continue branch, got %d", exec.calls)
	}
}

// --- Branch 3: no_progress_streak -> reasoning fallback ---

func noProgressHistory(streak int) []IterationResult {
	history := make([]IterationResult, streak)
	for i := range history {
		history[i] = IterationResult{IterationNumber: i + 1, ProgressState: store.LoopRunIterationProgressNoProgress}
	}
	return history
}

func TestDecide_NoProgressStreak_CallsReasoningFallback(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{Text: `{"decision": "retry", "reason": "try the same approach once more"}`}, nil
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{RemainingDelta: "unchanged"}, history,
		Budget{MaxIterations: 20, MaxNoProgressIterations: 2}, policy)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionRetry {
		t.Fatalf("expected DecisionRetry, got %+v", d)
	}
	if d.Reason != "try the same approach once more" {
		t.Fatalf("expected the reasoning fallback's reason to be carried through, got %q", d.Reason)
	}
	if exec.calls != 1 {
		t.Fatalf("expected exactly one LLM call, got %d", exec.calls)
	}
	if exec.lastReq.Provider != "anthropic" || exec.lastReq.Model != "claude" {
		t.Fatalf("expected the ContinuationPolicy's Provider/Model to reach the LLMStepRequest, got %+v", exec.lastReq)
	}
}

func TestDecide_NoProgressStreak_BelowThreshold_DoesNotCallReasoningFallback(t *testing.T) {
	history := noProgressHistory(1)
	exec := &fakeStepExecutor{}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxIterations: 20, MaxNoProgressIterations: 2}, ContinuationPolicy{})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionContinue {
		t.Fatalf("expected DecisionContinue below the no-progress threshold, got %+v", d)
	}
	if exec.calls != 0 {
		t.Fatalf("expected zero LLM calls below the no-progress threshold, got %d", exec.calls)
	}
}

func TestDecide_NoProgressStreak_AllSixEligibleDecisions(t *testing.T) {
	cases := []struct {
		response string
		want     DecisionKind
	}{
		{`{"decision":"retry"}`, DecisionRetry},
		{`{"decision":"REPLAN"}`, DecisionReplan},
		{`{"decision":"Rearchitect"}`, DecisionRearchitect},
		{`{"decision":"wait"}`, DecisionWait},
		{`{"decision":"escalate"}`, DecisionEscalate},
		{`{"decision":"fail"}`, DecisionFail},
	}
	for _, c := range cases {
		t.Run(string(c.want), func(t *testing.T) {
			history := noProgressHistory(2)
			exec := &fakeStepExecutor{
				llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
					return agentworkflow.LLMStepResult{Text: c.response}, nil
				},
			}
			policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

			d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
				Budget{MaxNoProgressIterations: 2}, policy)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			if d.Kind != c.want {
				t.Fatalf("expected %q, got %+v", c.want, d)
			}
		})
	}
}

func TestDecide_NoProgressStreak_ExcludesContinueAndComplete(t *testing.T) {
	for _, raw := range []string{`{"decision":"continue"}`, `{"decision":"complete"}`} {
		history := noProgressHistory(2)
		exec := &fakeStepExecutor{
			llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
				return agentworkflow.LLMStepResult{Text: raw}, nil
			},
		}
		policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

		_, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
			Budget{MaxNoProgressIterations: 2}, policy)
		if err == nil {
			t.Fatalf("expected an error for a reasoning-ineligible decision %q, got none", raw)
		}
	}
}

func TestDecide_NoProgressStreak_Rearchitect_CarriesRevision(t *testing.T) {
	history := noProgressHistory(2)
	raw := `{"decision":"rearchitect","reason":"the original target state is unreachable as written",` +
		`"revised_desired_state":["a narrower target"],"revised_acceptance_criteria":["narrower criterion"]}`
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{Text: raw}, nil
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Kind != DecisionRearchitect {
		t.Fatalf("expected DecisionRearchitect, got %+v", d)
	}
	if d.Revision == nil {
		t.Fatal("expected a non-nil Revision for a REARCHITECT decision")
	}
	if len(d.Revision.DesiredState) != 1 || d.Revision.DesiredState[0] != "a narrower target" {
		t.Fatalf("unexpected Revision.DesiredState: %+v", d.Revision.DesiredState)
	}
	if len(d.Revision.AcceptanceCriteria) != 1 || d.Revision.AcceptanceCriteria[0] != "narrower criterion" {
		t.Fatalf("unexpected Revision.AcceptanceCriteria: %+v", d.Revision.AcceptanceCriteria)
	}
}

func TestDecide_NoProgressStreak_NonRearchitectDecision_HasNoRevision(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{Text: `{"decision":"wait","reason":"waiting on an external condition"}`}, nil
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if d.Revision != nil {
		t.Fatalf("expected a nil Revision for a non-REARCHITECT decision, got %+v", d.Revision)
	}
}

// --- Fail-safe: a malformed/unparseable reasoning response is an error ---

func TestDecide_NoProgressStreak_MalformedResponse_ReturnsError_NotContinue(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{Text: "I think we should keep going, seems fine."}, nil
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	d, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err == nil {
		t.Fatalf("expected an error for an unparseable reasoning response, got Decision %+v", d)
	}
	if d.Kind == DecisionContinue {
		t.Fatal("a malformed reasoning response must never silently resolve to DecisionContinue")
	}
}

func TestDecide_NoProgressStreak_InvalidJSON_ReturnsError(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{Text: `{"decision": "retry"`}, nil
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	_, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err == nil {
		t.Fatal("expected an error for invalid JSON in the reasoning response")
	}
}

func TestDecide_NoProgressStreak_UnknownDecision_ReturnsError(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{Text: `{"decision": "shrug"}`}, nil
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	_, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err == nil {
		t.Fatal("expected an error for a decision value outside the reasoning-eligible set")
	}
}

func TestDecide_NoProgressStreak_ExecuteLLMStepError_Propagates(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			return agentworkflow.LLMStepResult{}, fmt.Errorf("provider unavailable")
		},
	}
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	_, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err == nil {
		t.Fatal("expected ExecuteLLMStep's own error to propagate")
	}
}

func TestDecide_NoProgressStreak_NilExecutor_ReturnsError(t *testing.T) {
	history := noProgressHistory(2)
	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}

	_, err := Decide(context.Background(), nil, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, policy)
	if err == nil {
		t.Fatal("expected an error when no StepExecutor is given but the reasoning fallback is reached")
	}
}

func TestDecide_NoProgressStreak_MissingPolicyProviderOrModel_ReturnsError(t *testing.T) {
	history := noProgressHistory(2)
	exec := &fakeStepExecutor{}

	_, err := Decide(context.Background(), exec, unmetGoal(t), nil, Evaluation{}, history,
		Budget{MaxNoProgressIterations: 2}, ContinuationPolicy{})
	if err == nil {
		t.Fatal("expected an error when ContinuationPolicy has no Provider/Model")
	}
	if exec.calls != 0 {
		t.Fatalf("expected no LLM call when the policy is incomplete, got %d", exec.calls)
	}
}

// --- noProgressStreak / countProgressState helpers ---

func TestNoProgressStreak_BreaksOnRegressionOrBlocked(t *testing.T) {
	history := []IterationResult{
		{IterationNumber: 1, ProgressState: store.LoopRunIterationProgressNoProgress},
		{IterationNumber: 2, ProgressState: store.LoopRunIterationProgressRegression},
		{IterationNumber: 3, ProgressState: store.LoopRunIterationProgressNoProgress},
		{IterationNumber: 4, ProgressState: store.LoopRunIterationProgressNoProgress},
	}
	if got := noProgressStreak(history); got != 2 {
		t.Fatalf("expected a trailing streak of 2 (regression at index 1 breaks it), got %d", got)
	}
}

func TestNoProgressStreak_EmptyHistory(t *testing.T) {
	if got := noProgressStreak(nil); got != 0 {
		t.Fatalf("expected 0 for empty history, got %d", got)
	}
}
