package reflex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/reflex"
)

// TestLoader_ValidYAML_WorkflowName is the CW-20260814-0002 companion to
// TestLoader_ValidYAML: a user-authored reflex can set resolves_to.workflow_name
// and have it round-trip into Resolution.WorkflowName.
func TestLoader_ValidYAML_WorkflowName(t *testing.T) {
	dir := t.TempDir()
	content := `
id: onboard-workflow
triggers:
  user_phrase_any_of:
    - "onboard the new hire"
resolves_to:
  workflow_name: onboard-user
priority: 60
`
	if err := os.WriteFile(filepath.Join(dir, "onboard.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("write test yaml: %v", err)
	}

	reflexes, err := reflex.LoadUserReflexes(dir)
	if err != nil {
		t.Fatalf("LoadUserReflexes: %v", err)
	}
	if len(reflexes) != 1 {
		t.Fatalf("expected 1 reflex, got %d", len(reflexes))
	}
	if got := reflexes[0].ResolvesTo.WorkflowName; got != "onboard-user" {
		t.Errorf("ResolvesTo.WorkflowName = %q, want onboard-user", got)
	}
}

// TestMatcher_WorkflowReflex_ResolvesToWorkflowName confirms Match surfaces
// a matched reflex's WorkflowName unchanged — the matcher itself needs no
// workflow-specific logic, it just returns the winning Reflex.
func TestMatcher_WorkflowReflex_ResolvesToWorkflowName(t *testing.T) {
	workflowReflex := reflex.Reflex{
		ID: "onboard-workflow",
		Triggers: reflex.Triggers{
			UserPhraseAnyOf: []string{"onboard the new hire"},
		},
		ResolvesTo: reflex.Resolution{WorkflowName: "onboard-user"},
		Priority:   50,
	}
	merged := reflex.MergeReflexes(reflex.BuiltinReflexes(), []reflex.Reflex{workflowReflex})

	got, ok := reflex.Match("please onboard the new hire today", classify.TierSmall, classify.PatternInline, merged)
	if !ok {
		t.Fatal("expected a match, got none")
	}
	if got.Reflex.ID != "onboard-workflow" {
		t.Fatalf("expected onboard-workflow to win, got %q", got.Reflex.ID)
	}
	if got.Reflex.ResolvesTo.WorkflowName != "onboard-user" {
		t.Errorf("ResolvesTo.WorkflowName = %q, want onboard-user", got.Reflex.ResolvesTo.WorkflowName)
	}
}

// TestAssignRoleWithReflex_WorkflowName_ReturnsRoleWorkflow guards the
// dispatcher.go integration seam: a workflow-routed reflex match must
// produce a RoleAssignment with Role=RoleWorkflow and WorkflowName set,
// mirroring dispatch.ExecuteTask's own precedence (a non-empty WorkflowName
// bypasses the ordinary Role/Profile mapping).
func TestAssignRoleWithReflex_WorkflowName_ReturnsRoleWorkflow(t *testing.T) {
	workflowReflex := reflex.Reflex{
		ID: "onboard-workflow",
		Triggers: reflex.Triggers{
			UserPhraseAnyOf: []string{"onboard the new hire"},
		},
		// Role/Profile deliberately left set to prove WorkflowName wins.
		ResolvesTo: reflex.Resolution{Role: "worker", Profile: "worker", WorkflowName: "onboard-user"},
		Priority:   50,
	}
	merged := reflex.MergeReflexes(reflex.BuiltinReflexes(), []reflex.Reflex{workflowReflex})

	assignment := reflex.AssignRoleWithReflex(
		"please onboard the new hire today",
		classify.TierSmall,
		classify.PatternInline,
		merged,
		"sess-001",
		"",
		nil,
	)
	if assignment.Role != dispatch.RoleWorkflow {
		t.Errorf("Role = %v, want RoleWorkflow", assignment.Role)
	}
	if assignment.WorkflowName != "onboard-user" {
		t.Errorf("WorkflowName = %q, want onboard-user", assignment.WorkflowName)
	}
}

// TestAssignRoleWithReflex_NonWorkflowReflex_LeavesWorkflowNameEmpty is the
// non-goal guard: an ordinary reflex match (no workflow_name) must not
// populate WorkflowName or force RoleWorkflow.
func TestAssignRoleWithReflex_NonWorkflowReflex_LeavesWorkflowNameEmpty(t *testing.T) {
	merged := reflex.MergeReflexes(reflex.BuiltinReflexes(), nil)
	assignment := reflex.AssignRoleWithReflex(
		"Implement the reflex matcher module",
		classify.TierSmall,
		classify.PatternInline,
		merged,
		"sess-002",
		"",
		nil,
	)
	if assignment.Role == dispatch.RoleWorkflow {
		t.Error("ordinary worker-execute reflex should not resolve to RoleWorkflow")
	}
	if assignment.WorkflowName != "" {
		t.Errorf("WorkflowName = %q, want empty", assignment.WorkflowName)
	}
}
