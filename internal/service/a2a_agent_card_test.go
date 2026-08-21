package service

// TASKS/teams/11-team-run-launch-api.md's required registry-growth/
// agent-card-pollution regression coverage: AgentCardGenerator.Generate
// must never surface a per-launch compiled TeamRun definition
// (agentworkflow.TeamRunDefinitionNamePrefix) as a public A2A skill, even
// when it is still registered (the common case -- see
// agentworkflow.Registry.Unregister's own doc comment for why a
// still-waiting TeamRun stays registered).

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

func TestAgentCardGenerator_Generate_ExcludesTeamRunDefinitions(t *testing.T) {
	reg := agentworkflow.NewRegistry(nil)

	plainWF := agentworkflow.WorkflowDefinition{
		Name:  "workflow-a",
		Steps: []agentworkflow.StepDefinition{{ID: "only", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}}},
	}
	if err := reg.Register(plainWF); err != nil {
		t.Fatalf("Register(plainWF): %v", err)
	}

	// The exact literal shape team_run_launcher.go's LaunchTeamRun
	// produces: agentworkflow.TeamRunDefinitionNamePrefix + team name +
	// ":" + a fresh ulid.
	teamRunWF := agentworkflow.WorkflowDefinition{
		Name: agentworkflow.TeamRunDefinitionNamePrefix + "Feature Development:01K5ZQ2VXH8P8QZJY7N3F6R2ST",
		Steps: []agentworkflow.StepDefinition{
			{ID: "scope_work", Kind: agentworkflow.StepKindFlex, Config: map[string]any{
				"active_slots": []string{"orchestrator"},
				"exit_trigger": map[string]any{"event": "done"},
			}},
		},
	}
	if err := reg.Register(teamRunWF); err != nil {
		t.Fatalf("Register(teamRunWF): %v", err)
	}

	gen := NewAgentCardGenerator(reg, "http://example.test", "test")
	card, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(card.Skills) != 1 {
		t.Fatalf("Skills = %+v, want exactly 1 (the compiled TeamRun definition must be excluded)", card.Skills)
	}
	if card.Skills[0].ID != "workflow-a" {
		t.Fatalf("Skills[0].ID = %q, want %q", card.Skills[0].ID, "workflow-a")
	}
	for _, sk := range card.Skills {
		if agentworkflow.IsTeamRunDefinitionName(sk.ID) {
			t.Fatalf("a team-run: prefixed definition leaked into the public skill list: %+v", sk)
		}
	}
}

// TestAgentCardGenerator_Generate_StillListsPlainWorkflowsWhenNoTeamRuns
// guards the boundary case: with zero team-run: definitions registered,
// behavior is unchanged from before this task (every named workflow is
// still a skill).
func TestAgentCardGenerator_Generate_StillListsPlainWorkflowsWhenNoTeamRuns(t *testing.T) {
	reg := agentworkflow.NewRegistry(nil)
	for _, name := range []string{"workflow-a", "workflow-b"} {
		wf := agentworkflow.WorkflowDefinition{
			Name:  name,
			Steps: []agentworkflow.StepDefinition{{ID: "only", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop"}}},
		}
		if err := reg.Register(wf); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}

	gen := NewAgentCardGenerator(reg, "http://example.test", "test")
	card, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(card.Skills) != 2 {
		t.Fatalf("Skills = %+v, want 2", card.Skills)
	}
}
