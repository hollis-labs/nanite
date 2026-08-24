package service

// TASKS/loops/09-stepkindloop-executor-and-waiting-status.md's own
// regression coverage:
//
//  1. The three-way gate > flex > loop precedence (waitingRunStatus,
//     formerly flexOrGateWaitingStatus) — 7 cases minimum, per that task's
//     own "Done means": gate-only, flex-only, loop-only, gate+flex,
//     gate+loop, flex+loop, all three.
//  2. parseLoopStepConfig's config-schema parsing, including the
//     documented default for outer-workflow-params forwarding.
//
// The end-to-end "reaches waiting_on_loop, then genuinely resumes via the
// real push mechanism" integration test lives in internal/loop (package
// loop already imports internal/service for task 08's own wiring, so it's
// the one package that can construct both a real BuiltinWorkflowEngine and
// a real LoopEngine together without an import cycle) — see
// internal/loop/stepkindloop_integration_test.go.

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// --- waitingRunStatus: gate > flex > loop precedence ---

func TestWaitingRunStatus_Precedence(t *testing.T) {
	byID := map[string]agentworkflow.StepDefinition{
		"g1": {ID: "g1", Kind: agentworkflow.StepKindGate},
		"g2": {ID: "g2", Kind: agentworkflow.StepKindGate},
		"f1": {ID: "f1", Kind: agentworkflow.StepKindFlex},
		"f2": {ID: "f2", Kind: agentworkflow.StepKindFlex},
		"l1": {ID: "l1", Kind: agentworkflow.StepKindLoop},
		"l2": {ID: "l2", Kind: agentworkflow.StepKindLoop},
	}

	tests := []struct {
		name    string
		waiting []string
		want    agentworkflow.RunStatus
	}{
		{"gate-only", []string{"g1"}, agentworkflow.RunStatusWaiting},
		{"gate-only-multi", []string{"g1", "g2"}, agentworkflow.RunStatusWaiting},
		{"flex-only", []string{"f1"}, agentworkflow.RunStatusWaitingOnFlex},
		{"flex-only-multi", []string{"f1", "f2"}, agentworkflow.RunStatusWaitingOnFlex},
		{"loop-only", []string{"l1"}, agentworkflow.RunStatusWaitingOnLoop},
		{"loop-only-multi", []string{"l1", "l2"}, agentworkflow.RunStatusWaitingOnLoop},
		{"gate+flex", []string{"g1", "f1"}, agentworkflow.RunStatusWaiting},
		{"gate+loop", []string{"g1", "l1"}, agentworkflow.RunStatusWaiting},
		{"flex+loop", []string{"f1", "l1"}, agentworkflow.RunStatusWaitingOnFlex},
		{"all-three", []string{"g1", "f1", "l1"}, agentworkflow.RunStatusWaiting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waiting := make(map[string]bool, len(tt.waiting))
			for _, id := range tt.waiting {
				waiting[id] = true
			}
			got := waitingRunStatus(byID, waiting)
			if got != tt.want {
				t.Errorf("waitingRunStatus(%v) = %q, want %q", tt.waiting, got, tt.want)
			}
		})
	}
}

// --- parseLoopStepConfig ---

func TestParseLoopStepConfig(t *testing.T) {
	outerParams := map[string]any{"task": "outer task description"}

	t.Run("minimal valid config with goal_id defaults workflow_params to outer input.Params", func(t *testing.T) {
		cfg := map[string]any{
			"workflow_name":    "iteration-def",
			"agent_profile_id": "agent-1",
			"goal_id":          "goal-123",
		}
		req, err := parseLoopStepConfig(cfg, outerParams)
		if err != nil {
			t.Fatalf("parseLoopStepConfig: %v", err)
		}
		if req.WorkflowName != "iteration-def" {
			t.Errorf("WorkflowName = %q, want %q", req.WorkflowName, "iteration-def")
		}
		if req.AgentProfileID != "agent-1" {
			t.Errorf("AgentProfileID = %q, want %q", req.AgentProfileID, "agent-1")
		}
		if req.GoalID != "goal-123" {
			t.Errorf("GoalID = %q, want %q", req.GoalID, "goal-123")
		}
		if req.WorkflowParams["task"] != "outer task description" {
			t.Errorf("WorkflowParams = %v, want it to default to the outer run's own params", req.WorkflowParams)
		}
	})

	t.Run("explicit workflow_params overrides the outer-params default", func(t *testing.T) {
		cfg := map[string]any{
			"workflow_name":    "iteration-def",
			"agent_profile_id": "agent-1",
			"goal_id":          "goal-123",
			"workflow_params":  map[string]any{"task": "inner override"},
		}
		req, err := parseLoopStepConfig(cfg, outerParams)
		if err != nil {
			t.Fatalf("parseLoopStepConfig: %v", err)
		}
		if req.WorkflowParams["task"] != "inner override" {
			t.Errorf("WorkflowParams = %v, want explicit override to win", req.WorkflowParams)
		}
	})

	t.Run("inline goal spec is parsed when goal_id is absent", func(t *testing.T) {
		cfg := map[string]any{
			"workflow_name":    "iteration-def",
			"agent_profile_id": "agent-1",
			"goal": map[string]any{
				"intent":              "ship the feature",
				"desired_state":       []any{"tests pass"},
				"acceptance_criteria": []any{"ci green"},
			},
		}
		req, err := parseLoopStepConfig(cfg, nil)
		if err != nil {
			t.Fatalf("parseLoopStepConfig: %v", err)
		}
		if req.GoalID != "" {
			t.Errorf("GoalID = %q, want empty (inline goal spec)", req.GoalID)
		}
		if req.GoalIntent != "ship the feature" {
			t.Errorf("GoalIntent = %q, want %q", req.GoalIntent, "ship the feature")
		}
		if len(req.GoalDesiredState) != 1 || req.GoalDesiredState[0] != "tests pass" {
			t.Errorf("GoalDesiredState = %v, want [\"tests pass\"]", req.GoalDesiredState)
		}
	})

	t.Run("budget and continuation_policy sub-objects parsed", func(t *testing.T) {
		cfg := map[string]any{
			"workflow_name":    "iteration-def",
			"agent_profile_id": "agent-1",
			"goal_id":          "goal-123",
			"budget": map[string]any{
				"max_iterations": 5,
				"on_exhausted":   "fail",
			},
			"continuation_policy": map[string]any{
				"provider": "anthropic",
				"model":    "claude-x",
			},
		}
		req, err := parseLoopStepConfig(cfg, nil)
		if err != nil {
			t.Fatalf("parseLoopStepConfig: %v", err)
		}
		if req.Budget.MaxIterations != 5 {
			t.Errorf("Budget.MaxIterations = %d, want 5", req.Budget.MaxIterations)
		}
		if req.Budget.OnExhausted != "fail" {
			t.Errorf("Budget.OnExhausted = %q, want %q", req.Budget.OnExhausted, "fail")
		}
		if req.ContinuationPolicy.Provider != "anthropic" || req.ContinuationPolicy.Model != "claude-x" {
			t.Errorf("ContinuationPolicy = %+v, want provider=anthropic model=claude-x", req.ContinuationPolicy)
		}
	})

	t.Run("missing workflow_name is rejected", func(t *testing.T) {
		cfg := map[string]any{"agent_profile_id": "agent-1", "goal_id": "goal-123"}
		if _, err := parseLoopStepConfig(cfg, nil); err == nil {
			t.Fatal("parseLoopStepConfig succeeded, want error for missing workflow_name")
		}
	})

	t.Run("missing agent_profile_id is rejected", func(t *testing.T) {
		cfg := map[string]any{"workflow_name": "iteration-def", "goal_id": "goal-123"}
		if _, err := parseLoopStepConfig(cfg, nil); err == nil {
			t.Fatal("parseLoopStepConfig succeeded, want error for missing agent_profile_id")
		}
	})

	t.Run("both goal_id and goal set is rejected", func(t *testing.T) {
		cfg := map[string]any{
			"workflow_name":    "iteration-def",
			"agent_profile_id": "agent-1",
			"goal_id":          "goal-123",
			"goal":             map[string]any{"intent": "x"},
		}
		if _, err := parseLoopStepConfig(cfg, nil); err == nil {
			t.Fatal("parseLoopStepConfig succeeded, want error for goal_id + goal both set")
		}
	})

	t.Run("neither goal_id nor goal set is rejected", func(t *testing.T) {
		cfg := map[string]any{"workflow_name": "iteration-def", "agent_profile_id": "agent-1"}
		if _, err := parseLoopStepConfig(cfg, nil); err == nil {
			t.Fatal("parseLoopStepConfig succeeded, want error for neither goal_id nor goal set")
		}
	})
}

// --- isTerminalLoopStatus ---

func TestIsTerminalLoopStatus(t *testing.T) {
	terminal := []string{"completed", "failed", "canceled"}
	for _, s := range terminal {
		if !isTerminalLoopStatus(s) {
			t.Errorf("isTerminalLoopStatus(%q) = false, want true", s)
		}
	}
	notTerminal := []string{"running", "waiting_on_gate", "waiting_on_escalation", ""}
	for _, s := range notTerminal {
		if isTerminalLoopStatus(s) {
			t.Errorf("isTerminalLoopStatus(%q) = true, want false", s)
		}
	}
}
