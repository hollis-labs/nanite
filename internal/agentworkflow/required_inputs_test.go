package agentworkflow

import (
	"reflect"
	"testing"
)

// TestRequiredInputs_WorkerReviewerGateShape mirrors the real shipped
// worker-reviewer-gate.yaml definition's worker step (CW-20260815-0022):
// a single {{input.task}} reference inside config.prompt.
func TestRequiredInputs_WorkerReviewerGateShape(t *testing.T) {
	def := WorkflowDefinition{
		Steps: []StepDefinition{
			{
				ID:   "worker",
				Kind: StepKindLLM,
				Config: map[string]any{
					"provider": "anthropic",
					"prompt":   "{{ input.task }}",
				},
			},
			{
				ID:        "approve",
				Kind:      StepKindGate,
				DependsOn: []string{"worker"},
				Config: map[string]any{
					"description": "Final human approval",
				},
			},
		},
	}
	got := RequiredInputs(def)
	want := []string{"task"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredInputs = %v, want %v", got, want)
	}
}

// TestRequiredInputs_DedupsAcrossSteps_FirstSeenOrder proves a key
// referenced by more than one step appears once, in the order its first
// occurrence was scanned (steps in definition order, keys sorted within a
// step's config for determinism).
func TestRequiredInputs_DedupsAcrossSteps_FirstSeenOrder(t *testing.T) {
	def := WorkflowDefinition{
		Steps: []StepDefinition{
			{ID: "a", Config: map[string]any{"x": "{{input.repo}}"}},
			{ID: "b", Config: map[string]any{"y": "{{input.branch}}", "z": "{{input.repo}}"}},
		},
	}
	got := RequiredInputs(def)
	want := []string{"repo", "branch"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredInputs = %v, want %v", got, want)
	}
}

// TestRequiredInputs_ScansNestedMapsAndSlices covers a tool step's shape
// (args nested under config, possibly inside a slice) — not just a flat
// prompt string.
func TestRequiredInputs_ScansNestedMapsAndSlices(t *testing.T) {
	def := WorkflowDefinition{
		Steps: []StepDefinition{
			{
				ID:   "t",
				Kind: StepKindTool,
				Config: map[string]any{
					"tool": "some_tool",
					"args": map[string]any{
						"paths": []any{"{{input.file_a}}", "{{input.file_b}}"},
					},
				},
			},
		},
	}
	got := RequiredInputs(def)
	want := []string{"file_a", "file_b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredInputs = %v, want %v", got, want)
	}
}

// TestRequiredInputs_IgnoresStepReferences proves a {{steps.<id>.output}}
// reference (a dependency-result reference, not a caller-supplied param) is
// not reported as a required input.
func TestRequiredInputs_IgnoresStepReferences(t *testing.T) {
	def := WorkflowDefinition{
		Steps: []StepDefinition{
			{ID: "a", Config: map[string]any{"prompt": "{{input.task}}"}},
			{ID: "b", DependsOn: []string{"a"}, Config: map[string]any{"prompt": "{{steps.a.output}}"}},
		},
	}
	got := RequiredInputs(def)
	want := []string{"task"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredInputs = %v, want %v", got, want)
	}
}

// TestRequiredInputs_NoReferences_ReturnsEmpty proves a workflow with no
// {{input.*}} references anywhere reports no required inputs, not nil vs.
// empty-slice ambiguity that trips up a naive caller.
func TestRequiredInputs_NoReferences_ReturnsEmpty(t *testing.T) {
	def := WorkflowDefinition{
		Steps: []StepDefinition{
			{ID: "a", Config: map[string]any{"prompt": "static, no templating"}},
		},
	}
	if got := RequiredInputs(def); len(got) != 0 {
		t.Errorf("RequiredInputs = %v, want empty", got)
	}
}
