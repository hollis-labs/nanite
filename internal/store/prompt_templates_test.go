package store

import "testing"

// TestListPromptTemplatesForAgent_ColumnAlignment is a regression guard for
// the audit finding where SELECT was missing `icon`, causing Scan to misalign
// CreatedAt/UpdatedAt. A known Icon value is round-tripped to verify the
// field mapping is correct.
func TestListPromptTemplatesForAgent_ColumnAlignment(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "pt-align")

	pt := &PromptTemplate{
		Name:     "Custom Template",
		Slug:     "custom-pt-align",
		Scope:    "system",
		Template: "hello {{name}}",
		Priority: 15,
		Icon:     "book",
	}
	if err := s.CreatePromptTemplate(pt); err != nil {
		t.Fatalf("CreatePromptTemplate: %v", err)
	}

	if err := s.AssignPromptTemplateToAgent(agent.ID, pt.ID); err != nil {
		t.Fatalf("AssignPromptTemplateToAgent: %v", err)
	}

	list, err := s.ListPromptTemplatesForAgent(agent.ID)
	if err != nil {
		t.Fatalf("ListPromptTemplatesForAgent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 template, got %d", len(list))
	}
	got := list[0]

	if got.ID != pt.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, pt.ID)
	}
	if got.Slug != "custom-pt-align" {
		t.Errorf("Slug misaligned: got %q", got.Slug)
	}
	if got.Icon != "book" {
		t.Errorf("Icon misaligned: got %q, want %q", got.Icon, "book")
	}
	if got.Priority != 15 {
		t.Errorf("Priority misaligned: got %d", got.Priority)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be populated")
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should be populated")
	}
}
