package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close(); os.Remove(dbPath) })
	return s
}

func newSelfTools(t *testing.T) *SelfToolsTransport {
	t.Helper()
	s := newTestStore(t)
	return NewSelfToolsTransport(s)
}

// TestSelfToolsTransport_ListTools verifies all expected tools are returned.
func TestSelfToolsTransport_ListTools(t *testing.T) {
	st := newSelfTools(t)
	tools, err := st.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]bool{
		"mentat_create_skill":    false,
		"mentat_list_skills":     false,
		"mentat_update_skill":    false,
		"mentat_delete_skill":    false,
		"mentat_create_agent":    false,
		"mentat_list_agents":     false,
		"mentat_update_agent":    false,
		"mentat_list_workflows":  false,
		"mentat_create_workflow": false,
	}

	for _, tool := range tools {
		if _, ok := expected[tool.Name]; ok {
			expected[tool.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}

	if len(tools) != len(expected) {
		t.Errorf("expected %d tools, got %d", len(expected), len(tools))
	}
}

// TestSelfToolsTransport_CreateSkill tests the round-trip create and verify.
func TestSelfToolsTransport_CreateSkill(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Create a skill.
	result, err := st.CallTool(ctx, "mentat_create_skill", map[string]any{
		"name":        "Test Skill",
		"slug":        "test-skill",
		"description": "A test skill for unit testing",
		"category":    "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Created skill") {
		t.Errorf("expected creation confirmation, got: %s", result.Content[0].Text)
	}

	// List skills and verify it appears.
	listResult, err := st.CallTool(ctx, "mentat_list_skills", map[string]any{
		"category": "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if listResult.IsError {
		t.Fatalf("unexpected error: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "Test Skill") {
		t.Errorf("expected skill in list, got: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "test-skill") {
		t.Errorf("expected slug in list, got: %s", listResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_ListSkills verifies filtering by category.
func TestSelfToolsTransport_ListSkills(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Create two skills in different categories.
	st.CallTool(ctx, "mentat_create_skill", map[string]any{
		"name": "Skill A", "slug": "skill-a", "description": "cat-x skill", "category": "cat-x",
	})
	st.CallTool(ctx, "mentat_create_skill", map[string]any{
		"name": "Skill B", "slug": "skill-b", "description": "cat-y skill", "category": "cat-y",
	})

	// List all.
	allResult, _ := st.CallTool(ctx, "mentat_list_skills", map[string]any{})
	if !strings.Contains(allResult.Content[0].Text, "Skill A") || !strings.Contains(allResult.Content[0].Text, "Skill B") {
		t.Errorf("expected both skills, got: %s", allResult.Content[0].Text)
	}

	// Filter by cat-x.
	filteredResult, _ := st.CallTool(ctx, "mentat_list_skills", map[string]any{"category": "cat-x"})
	if !strings.Contains(filteredResult.Content[0].Text, "Skill A") {
		t.Errorf("expected Skill A, got: %s", filteredResult.Content[0].Text)
	}
	if strings.Contains(filteredResult.Content[0].Text, "Skill B") {
		t.Errorf("did not expect Skill B, got: %s", filteredResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_CreateAgent tests agent creation round-trip.
func TestSelfToolsTransport_CreateAgent(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	result, err := st.CallTool(ctx, "mentat_create_agent", map[string]any{
		"name":          "Test Agent",
		"slug":          "test-agent",
		"system_prompt": "You are a helpful test agent.",
		"description":   "For testing purposes",
		"default_model": "claude-3-haiku",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Created agent") {
		t.Errorf("expected creation confirmation, got: %s", result.Content[0].Text)
	}

	// List agents and verify.
	listResult, err := st.CallTool(ctx, "mentat_list_agents", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if listResult.IsError {
		t.Fatalf("unexpected error: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "Test Agent") {
		t.Errorf("expected agent in list, got: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "test-agent") {
		t.Errorf("expected slug in list, got: %s", listResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_CreateSkill_MissingFields verifies validation.
func TestSelfToolsTransport_CreateSkill_MissingFields(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	result, _ := st.CallTool(ctx, "mentat_create_skill", map[string]any{
		"name": "Only Name",
	})
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

// TestSelfToolsTransport_DeleteSkill tests skill deletion.
func TestSelfToolsTransport_DeleteSkill(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	// Create a skill first.
	createResult, _ := st.CallTool(ctx, "mentat_create_skill", map[string]any{
		"name": "To Delete", "slug": "to-delete", "description": "Will be deleted",
	})
	if createResult.IsError {
		t.Fatalf("create failed: %s", createResult.Content[0].Text)
	}

	// Extract the ID from the list.
	skills, _ := st.Store.ListSkills()
	var skillID string
	for _, sk := range skills {
		if sk.Slug == "to-delete" {
			skillID = sk.ID
			break
		}
	}
	if skillID == "" {
		t.Fatal("could not find created skill")
	}

	// Delete it.
	delResult, _ := st.CallTool(ctx, "mentat_delete_skill", map[string]any{"id": skillID})
	if delResult.IsError {
		t.Fatalf("delete failed: %s", delResult.Content[0].Text)
	}

	// Verify it's gone.
	sk, _ := st.Store.GetSkill(skillID)
	if sk != nil {
		t.Error("skill should have been deleted")
	}
}

// TestSelfToolsTransport_CreateWorkflow tests workflow creation round-trip.
func TestSelfToolsTransport_CreateWorkflow(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	result, err := st.CallTool(ctx, "mentat_create_workflow", map[string]any{
		"name":       "Test Workflow",
		"slug":       "test-workflow",
		"definition": `{"steps":[{"name":"step1","action":"llm_call"}]}`,
		"trigger":    "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Created workflow") {
		t.Errorf("expected creation confirmation, got: %s", result.Content[0].Text)
	}

	// List workflows and verify.
	listResult, _ := st.CallTool(ctx, "mentat_list_workflows", map[string]any{})
	if listResult.IsError {
		t.Fatalf("unexpected error: %s", listResult.Content[0].Text)
	}
	if !strings.Contains(listResult.Content[0].Text, "Test Workflow") {
		t.Errorf("expected workflow in list, got: %s", listResult.Content[0].Text)
	}
}

// TestSelfToolsTransport_UnknownTool verifies unknown tool returns error.
func TestSelfToolsTransport_UnknownTool(t *testing.T) {
	st := newSelfTools(t)
	result, _ := st.CallTool(context.Background(), "nonexistent_tool", map[string]any{})
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
}
