package store

import "testing"

func TestCreateMode(t *testing.T) {
	s := newTestStore(t)

	m := &Mode{
		Slug:           "test-mode",
		Name:           "Test Mode",
		PromptAddendum: "You are in test mode.",
	}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}
	if m.ID == "" {
		t.Error("expected ID to be generated")
	}
	if m.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
	if m.ToolOverrides != "{}" {
		t.Errorf("expected default tool_overrides '{}', got %q", m.ToolOverrides)
	}
}

func TestGetMode(t *testing.T) {
	s := newTestStore(t)

	m := &Mode{Slug: "get-mode", Name: "Get Mode", PromptAddendum: "test"}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	got, err := s.GetMode(m.ID)
	if err != nil {
		t.Fatalf("GetMode: %v", err)
	}
	if got == nil {
		t.Fatal("expected mode, got nil")
	}
	if got.Slug != "get-mode" {
		t.Errorf("Slug mismatch: got %q, want %q", got.Slug, "get-mode")
	}

	// Test not found
	none, err := s.GetMode("nonexistent")
	if err != nil {
		t.Fatalf("GetMode nonexistent: %v", err)
	}
	if none != nil {
		t.Error("expected nil for nonexistent mode")
	}
}

func TestListModes(t *testing.T) {
	s := newTestStore(t)

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		m := &Mode{Slug: slug, Name: slug, PromptAddendum: "test"}
		if err := s.CreateMode(m); err != nil {
			t.Fatalf("CreateMode %s: %v", slug, err)
		}
	}

	modes, err := s.ListModes()
	if err != nil {
		t.Fatalf("ListModes: %v", err)
	}
	if len(modes) != 3 {
		t.Fatalf("expected 3 modes, got %d", len(modes))
	}
}

func TestUpdateMode(t *testing.T) {
	s := newTestStore(t)

	m := &Mode{Slug: "update-mode", Name: "Original", PromptAddendum: "original"}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	m.Name = "Updated"
	m.PromptAddendum = "updated addendum"
	if err := s.UpdateMode(m); err != nil {
		t.Fatalf("UpdateMode: %v", err)
	}

	got, err := s.GetMode(m.ID)
	if err != nil {
		t.Fatalf("GetMode after update: %v", err)
	}
	if got.Name != "Updated" {
		t.Errorf("Name not updated: got %q", got.Name)
	}
	if got.PromptAddendum != "updated addendum" {
		t.Errorf("PromptAddendum not updated: got %q", got.PromptAddendum)
	}

	// Test update nonexistent
	fake := &Mode{ID: "nonexistent", Slug: "x", Name: "x"}
	if err := s.UpdateMode(fake); err == nil {
		t.Error("expected error updating nonexistent mode")
	}
}

func TestDeleteMode(t *testing.T) {
	s := newTestStore(t)

	m := &Mode{Slug: "delete-mode", Name: "Delete Me", PromptAddendum: "bye"}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	if err := s.DeleteMode(m.ID); err != nil {
		t.Fatalf("DeleteMode: %v", err)
	}

	got, err := s.GetMode(m.ID)
	if err != nil {
		t.Fatalf("GetMode after delete: %v", err)
	}
	if got != nil {
		t.Error("expected mode to be deleted")
	}

	// Test delete nonexistent
	if err := s.DeleteMode("nonexistent"); err == nil {
		t.Error("expected error deleting nonexistent mode")
	}
}

func TestDeleteMode_BuiltinProtected(t *testing.T) {
	s := newTestStore(t)

	m := &Mode{Slug: "builtin-mode", Name: "Builtin", PromptAddendum: "test", IsBuiltin: true}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	if err := s.DeleteMode(m.ID); err == nil {
		t.Error("expected error deleting built-in mode")
	}

	// Verify it still exists
	got, err := s.GetMode(m.ID)
	if err != nil {
		t.Fatalf("GetMode: %v", err)
	}
	if got == nil {
		t.Error("built-in mode should not have been deleted")
	}
}

func TestAssignModeToAgent(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "assign-mode-agent")

	m := &Mode{Slug: "assign-mode", Name: "Assign", PromptAddendum: "test"}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	if err := s.AssignModeToAgent(a.ID, m.ID); err != nil {
		t.Fatalf("AssignModeToAgent: %v", err)
	}

	// Assign again should not error (INSERT OR IGNORE)
	if err := s.AssignModeToAgent(a.ID, m.ID); err != nil {
		t.Fatalf("AssignModeToAgent (idempotent): %v", err)
	}

	modes, err := s.GetAgentAssignedModes(a.ID)
	if err != nil {
		t.Fatalf("GetAgentAssignedModes: %v", err)
	}
	if len(modes) != 1 {
		t.Fatalf("expected 1 assigned mode, got %d", len(modes))
	}
	if modes[0].ID != m.ID {
		t.Errorf("mode ID mismatch: got %q, want %q", modes[0].ID, m.ID)
	}
}

func TestUnassignModeFromAgent(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "unassign-mode-agent")

	m := &Mode{Slug: "unassign-mode", Name: "Unassign", PromptAddendum: "test"}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	if err := s.AssignModeToAgent(a.ID, m.ID); err != nil {
		t.Fatalf("AssignModeToAgent: %v", err)
	}

	if err := s.UnassignModeFromAgent(a.ID, m.ID); err != nil {
		t.Fatalf("UnassignModeFromAgent: %v", err)
	}

	modes, err := s.GetAgentAssignedModes(a.ID)
	if err != nil {
		t.Fatalf("GetAgentAssignedModes: %v", err)
	}
	if len(modes) != 0 {
		t.Errorf("expected 0 assigned modes after unassign, got %d", len(modes))
	}

	// Unassign nonexistent should error
	if err := s.UnassignModeFromAgent(a.ID, m.ID); err == nil {
		t.Error("expected error unassigning already-removed mode")
	}
}

func TestDefaultModes_Seeded(t *testing.T) {
	s := newTestStore(t)

	if err := s.SeedBuiltinModes(); err != nil {
		t.Fatalf("SeedBuiltinModes: %v", err)
	}

	modes, err := s.ListModes()
	if err != nil {
		t.Fatalf("ListModes: %v", err)
	}
	if len(modes) < 4 {
		t.Fatalf("expected at least 4 seeded modes, got %d", len(modes))
	}

	slugs := make(map[string]bool)
	for _, m := range modes {
		slugs[m.Slug] = true
		if !m.IsBuiltin {
			t.Errorf("seeded mode %q should be built-in", m.Slug)
		}
	}

	for _, expected := range []string{"default", "architect", "planner", "writer"} {
		if !slugs[expected] {
			t.Errorf("expected seeded mode %q not found", expected)
		}
	}

	// Seed again — should be idempotent
	if err := s.SeedBuiltinModes(); err != nil {
		t.Fatalf("SeedBuiltinModes (idempotent): %v", err)
	}

	modes2, err := s.ListModes()
	if err != nil {
		t.Fatalf("ListModes after second seed: %v", err)
	}
	if len(modes2) != len(modes) {
		t.Errorf("expected same count after idempotent seed: got %d, want %d", len(modes2), len(modes))
	}
}

func TestGetModeBySlug(t *testing.T) {
	s := newTestStore(t)

	m := &Mode{Slug: "slug-test", Name: "Slug Test", PromptAddendum: "test"}
	if err := s.CreateMode(m); err != nil {
		t.Fatalf("CreateMode: %v", err)
	}

	got, err := s.GetModeBySlug("slug-test")
	if err != nil {
		t.Fatalf("GetModeBySlug: %v", err)
	}
	if got == nil {
		t.Fatal("expected mode, got nil")
	}
	if got.ID != m.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, m.ID)
	}
}

func TestMultipleModesPerAgent(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "multi-mode-agent")

	for _, slug := range []string{"mode-a", "mode-b", "mode-c"} {
		m := &Mode{Slug: slug, Name: slug, PromptAddendum: "test"}
		if err := s.CreateMode(m); err != nil {
			t.Fatalf("CreateMode %s: %v", slug, err)
		}
		if err := s.AssignModeToAgent(a.ID, m.ID); err != nil {
			t.Fatalf("AssignModeToAgent %s: %v", slug, err)
		}
	}

	modes, err := s.GetAgentAssignedModes(a.ID)
	if err != nil {
		t.Fatalf("GetAgentAssignedModes: %v", err)
	}
	if len(modes) != 3 {
		t.Fatalf("expected 3 assigned modes, got %d", len(modes))
	}
}
