package store

import (
	"context"
	"testing"
)

// makeDocumentTestSession creates a minimal session for document tests.
func makeDocumentTestSession(t *testing.T, s *Store) string {
	t.Helper()
	sess := makeTestSession(t, s)
	return sess.ID
}

// TestDocumentsCRUD exercises Create, Get, List, UpdateToggles, and Delete.
func TestDocumentsCRUD(t *testing.T) {
	s := newTestStore(t)
	sessionID := makeDocumentTestSession(t, s)

	// Create
	doc := &Document{
		SessionID: sessionID,
		Name:      "test.md",
		Content:   "# Hello",
		MimeType:  "text/markdown",
	}
	if err := s.CreateDocument(context.Background(), doc); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if doc.ID == "" {
		t.Fatal("expected non-empty ID after create")
	}

	// Get
	got, err := s.GetDocument(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got.Name != "test.md" {
		t.Errorf("Name: got %q want %q", got.Name, "test.md")
	}
	if got.Included {
		t.Error("expected Included=false by default")
	}
	if got.FullContent {
		t.Error("expected FullContent=false by default")
	}

	// List
	docs, err := s.ListDocuments(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ListDocuments: got %d want 1", len(docs))
	}

	// UpdateToggles
	if err := s.UpdateDocumentToggles(context.Background(), doc.ID, true, false, "summary text"); err != nil {
		t.Fatalf("UpdateDocumentToggles: %v", err)
	}
	got, _ = s.GetDocument(context.Background(), doc.ID)
	if !got.Included {
		t.Error("expected Included=true after update")
	}
	if got.Summary != "summary text" {
		t.Errorf("Summary: got %q want %q", got.Summary, "summary text")
	}

	// GetIncludedDocuments — returns only included docs.
	included, err := s.GetIncludedDocuments(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetIncludedDocuments: %v", err)
	}
	if len(included) != 1 {
		t.Fatalf("GetIncludedDocuments: got %d want 1", len(included))
	}

	// Exclude again.
	_ = s.UpdateDocumentToggles(context.Background(), doc.ID, false, false, "")
	included, _ = s.GetIncludedDocuments(context.Background(), sessionID)
	if len(included) != 0 {
		t.Errorf("GetIncludedDocuments after exclude: got %d want 0", len(included))
	}

	// Delete
	if err := s.DeleteDocument(context.Background(), doc.ID); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	docs, _ = s.ListDocuments(context.Background(), sessionID)
	if len(docs) != 0 {
		t.Errorf("ListDocuments after delete: got %d want 0", len(docs))
	}
}

// TestSessionContextPrompt exercises Get/Set round-trip and default empty value.
func TestSessionContextPrompt(t *testing.T) {
	s := newTestStore(t)
	sessionID := makeDocumentTestSession(t, s)

	// Default is empty.
	prompt, err := s.GetSessionContextPrompt(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSessionContextPrompt (default): %v", err)
	}
	if prompt != "" {
		t.Errorf("expected empty default prompt, got %q", prompt)
	}

	// Set.
	want := "## Skills\nUse /capture-decision for design decisions."
	if err := s.SetSessionContextPrompt(context.Background(), sessionID, want); err != nil {
		t.Fatalf("SetSessionContextPrompt: %v", err)
	}

	// Get after set.
	got, err := s.GetSessionContextPrompt(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSessionContextPrompt (after set): %v", err)
	}
	if got != want {
		t.Errorf("prompt mismatch:\ngot  %q\nwant %q", got, want)
	}

	// Overwrite.
	if err := s.SetSessionContextPrompt(context.Background(), sessionID, ""); err != nil {
		t.Fatalf("SetSessionContextPrompt (clear): %v", err)
	}
	got, _ = s.GetSessionContextPrompt(context.Background(), sessionID)
	if got != "" {
		t.Errorf("expected empty after clear, got %q", got)
	}
}

// TestDocumentsExcludedByDefault ensures newly-created documents are not
// injected into agent context until explicitly included.
func TestDocumentsExcludedByDefault(t *testing.T) {
	s := newTestStore(t)
	sessionID := makeDocumentTestSession(t, s)

	for i := 0; i < 3; i++ {
		doc := &Document{
			SessionID: sessionID,
			Name:      "doc",
			Content:   "content",
		}
		if err := s.CreateDocument(context.Background(), doc); err != nil {
			t.Fatalf("CreateDocument %d: %v", i, err)
		}
	}

	included, err := s.GetIncludedDocuments(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetIncludedDocuments: %v", err)
	}
	if len(included) != 0 {
		t.Errorf("expected 0 included docs by default, got %d", len(included))
	}
}
