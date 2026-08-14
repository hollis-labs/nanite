package agentworkflow

import "testing"

func stepIDs(level []StepDefinition) map[string]bool {
	out := make(map[string]bool, len(level))
	for _, s := range level {
		out[s.ID] = true
	}
	return out
}

func TestLevels_Diamond(t *testing.T) {
	steps := []StepDefinition{
		{ID: "a", Kind: StepKindTool},
		{ID: "b", Kind: StepKindTool, DependsOn: []string{"a"}},
		{ID: "c", Kind: StepKindTool, DependsOn: []string{"a"}},
		{ID: "d", Kind: StepKindTool, DependsOn: []string{"b", "c"}},
	}
	levels, err := Levels(steps)
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}
	if len(levels) != 3 {
		t.Fatalf("len(levels) = %d, want 3: %v", len(levels), levels)
	}
	if got := stepIDs(levels[0]); len(got) != 1 || !got["a"] {
		t.Fatalf("level 0 = %v, want {a}", got)
	}
	if got := stepIDs(levels[1]); len(got) != 2 || !got["b"] || !got["c"] {
		t.Fatalf("level 1 = %v, want {b,c}", got)
	}
	if got := stepIDs(levels[2]); len(got) != 1 || !got["d"] {
		t.Fatalf("level 2 = %v, want {d}", got)
	}
}

func TestLevels_NoDependencies(t *testing.T) {
	steps := []StepDefinition{
		{ID: "a", Kind: StepKindTool},
		{ID: "b", Kind: StepKindTool},
	}
	levels, err := Levels(steps)
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}
	if len(levels) != 1 || len(levels[0]) != 2 {
		t.Fatalf("levels = %v, want a single level of 2", levels)
	}
}

func TestLevels_Cycle(t *testing.T) {
	steps := []StepDefinition{
		{ID: "a", Kind: StepKindTool, DependsOn: []string{"b"}},
		{ID: "b", Kind: StepKindTool, DependsOn: []string{"a"}},
	}
	if _, err := Levels(steps); err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestLevels_SelfCycle(t *testing.T) {
	steps := []StepDefinition{
		{ID: "a", Kind: StepKindTool, DependsOn: []string{"a"}},
	}
	if _, err := Levels(steps); err == nil {
		t.Fatal("expected self-cycle error, got nil")
	}
}

func TestLevels_UnknownDependency(t *testing.T) {
	steps := []StepDefinition{
		{ID: "a", Kind: StepKindTool, DependsOn: []string{"ghost"}},
	}
	if _, err := Levels(steps); err == nil {
		t.Fatal("expected unknown-dependency error, got nil")
	}
}
