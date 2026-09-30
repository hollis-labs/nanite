package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestLoopViewJSONKeys pins each Loops view at a zero value (omitempty keys
// absent) and at a fully populated value (every key present), which together
// pin exactly which keys are omitempty. The key sets are what the store rows
// emitted when they were serialized directly.
func TestLoopViewJSONKeys(t *testing.T) {
	iter := int64(2)
	cases := []struct {
		name       string
		zero, full any
		always     []string
		omitempty  []string
	}{
		{
			name: "goal",
			zero: goalToView(&store.Goal{}),
			full: goalToView(&store.Goal{
				ID: "g", ParentGoalID: "p", Intent: "i", DesiredStateJSON: "[]",
				ConstraintsJSON: "[]", AcceptanceCriteriaJSON: "[]", InvariantsJSON: "[]",
				Priority: "high", Scope: "s", Status: "draft", Owner: "o", Source: "src",
				CreatedAt: "c", ActivatedAt: "a", CompletedAt: "d",
			}),
			always: []string{
				"acceptance_criteria_json", "constraints_json", "created_at",
				"desired_state_json", "id", "intent", "invariants_json", "status",
			},
			omitempty: []string{
				"activated_at", "completed_at", "owner", "parent_goal_id",
				"priority", "scope", "source",
			},
		},
		{
			name: "goal evidence",
			zero: goalEvidenceToView([]store.GoalEvidence{{}})[0],
			full: goalEvidenceToView([]store.GoalEvidence{{
				ID: "e", GoalID: "g", LoopRunID: "l", IterationNumber: &iter,
				EvidenceType: "t", RefTable: "rt", RefID: "ri", Result: "pass",
				Summary: "s", RecordedAt: "r",
			}})[0],
			always:    []string{"evidence_type", "goal_id", "id", "recorded_at", "ref_id", "ref_table"},
			omitempty: []string{"iteration_number", "loop_run_id", "result", "summary"},
		},
		{
			name: "loop run",
			zero: loopRunToView(&store.LoopRun{}),
			full: loopRunToView(&store.LoopRun{
				ID: "l", GoalID: "g", DefinitionName: "d", Status: "running",
				CurrentIteration: 1, BudgetJSON: "{}", ContinuationPolicyJSON: "{}",
				NoProgressStreak: 1, StartedAt: "s", UpdatedAt: "u", CompletedAt: "c",
			}),
			always: []string{
				"budget_json", "continuation_policy_json", "current_iteration",
				"definition_name", "goal_id", "id", "no_progress_streak", "started_at",
				"status", "updated_at",
			},
			omitempty: []string{"completed_at"},
		},
		{
			name: "loop run iteration",
			zero: loopRunIterationsToView([]store.LoopRunIteration{{}})[0],
			full: loopRunIterationsToView([]store.LoopRunIteration{{
				ID: "i", LoopRunID: "l", IterationNumber: 1, WorkflowRunID: "w",
				Decision: "continue", ProgressState: "progress", EvaluationJSON: "{}",
				StartedAt: "s", CompletedAt: "c",
			}})[0],
			always:    []string{"evaluation_json", "id", "iteration_number", "loop_run_id", "started_at"},
			omitempty: []string{"completed_at", "decision", "progress_state", "workflow_run_id"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonKeys(t, tc.zero); !reflect.DeepEqual(got, tc.always) {
				t.Fatalf("zero-value keys = %v\nwant             %v", got, tc.always)
			}
			want := append(append([]string{}, tc.always...), tc.omitempty...)
			sort.Strings(want)
			if got := jsonKeys(t, tc.full); !reflect.DeepEqual(got, want) {
				t.Fatalf("populated keys = %v\nwant           %v", got, want)
			}
		})
	}
}

func TestLoopViewEmptyListsMarshalAsArray(t *testing.T) {
	cases := map[string]any{
		"goals":      goalsToView(nil),
		"evidence":   goalEvidenceToView(nil),
		"loop runs":  loopRunsToView(nil),
		"iterations": loopRunIterationsToView(nil),
	}
	for name, v := range cases {
		raw, _ := json.Marshal(v)
		if string(raw) != "[]" {
			t.Fatalf("%s: marshaled %s, want []", name, raw)
		}
	}
}

// A missing goal is reported (404) before a malformed PATCH body is read.
func TestPatchGoalNotFoundBeforeBodyDecode(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/goals/no-such-goal", strings.NewReader(`{not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
	if msg := errorBody(t, w); msg != "goal not found" {
		t.Fatalf("error %q, want %q", msg, "goal not found")
	}
}
