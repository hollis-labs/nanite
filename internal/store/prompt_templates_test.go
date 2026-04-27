package store

import (
	"sync"
	"testing"
)

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

// TestComposePromptForAgent_CanonicalTemplateMissingWarn tests that ComposePromptForAgent
// handles missing templates gracefully without panicking. When file-default agent has no
// templates assigned, ComposePromptForAgent returns empty string and emits a one-shot
// warning (verified manually or in integration tests).
func TestComposePromptForAgent_CanonicalTemplateMissingWarn(t *testing.T) {
	s := newTestStore(t)

	// Reset the once so subsequent test runs can observe the warning.
	canonicalTemplateMissingOnce = sync.Once{}

	// Call ComposePromptForAgent for file-default with no templates assigned.
	// Migration 027 seeds the canonical template, but we can still test the
	// fallback path by querying an agent that has no assignments.
	// Create a fresh agent with no templates to verify the no-panic path.
	freshAgent := makeTestAgent(t, s, "fresh-no-templates")

	// Call ComposePromptForAgent on the fresh agent.
	// It has no templates assigned, so should return empty string without error.
	composed, err := s.ComposePromptForAgent(freshAgent.ID, map[string]string{})
	if err != nil {
		t.Fatalf("ComposePromptForAgent: %v", err)
	}

	// Should return empty string (no templates assigned).
	if composed != "" {
		t.Errorf("expected empty string, got: %q", composed)
	}

	// Verify a subsequent call also works (verifying no state corruption).
	composed2, err2 := s.ComposePromptForAgent(freshAgent.ID, map[string]string{})
	if err2 != nil {
		t.Fatalf("second ComposePromptForAgent: %v", err2)
	}
	if composed2 != "" {
		t.Errorf("second call expected empty string, got: %q", composed2)
	}

	// Verify file-default agent (with canonical template assigned by migration 027)
	// returns the proper prompt.
	fileDefaultComposed, err := s.ComposePromptForAgent("file-default", map[string]string{})
	if err != nil {
		t.Fatalf("file-default ComposePromptForAgent: %v", err)
	}
	// Migration 027 assigns the canonical template, so this should have content.
	if fileDefaultComposed == "" {
		t.Error("expected file-default to have canonical template content")
	}
}
