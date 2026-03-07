package store

import (
	"testing"
	"time"
)

func seedWorkspace(t *testing.T, s *Store, id string) {
	t.Helper()
	err := s.CreateWorkspace(&Workspace{ID: id, Name: "Test Workspace"})
	if err != nil {
		t.Fatalf("seedWorkspace: %v", err)
	}
}

func TestCreateSession(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if sess.ID == "" {
		t.Error("expected ID to be generated")
	}
	if sess.ShortCode == "" {
		t.Error("expected ShortCode to be assigned")
	}
	if sess.Status != "active" {
		t.Errorf("expected status 'active', got %q", sess.Status)
	}
	if sess.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

func TestGetSession(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1", Title: "Test Chat"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.ID != sess.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, sess.ID)
	}
	if got.ShortCode != sess.ShortCode {
		t.Errorf("ShortCode mismatch: got %q, want %q", got.ShortCode, sess.ShortCode)
	}
	if got.Title != "Test Chat" {
		t.Errorf("Title mismatch: got %q, want %q", got.Title, "Test Chat")
	}
	if got.WorkspaceID != "ws1" {
		t.Errorf("WorkspaceID mismatch: got %q, want %q", got.WorkspaceID, "ws1")
	}
	if got.Status != "active" {
		t.Errorf("Status mismatch: got %q, want %q", got.Status, "active")
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	// Create sessions with small delays to guarantee ordering.
	for i := 0; i < 3; i++ {
		sess := &Session{WorkspaceID: "ws1", Title: "Chat"}
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		// Sleep briefly so last_activity differs.
		time.Sleep(10 * time.Millisecond)
	}

	sessions, err := s.ListSessions("ws1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Verify DESC ordering by last_activity.
	for i := 1; i < len(sessions); i++ {
		if sessions[i-1].LastActivity < sessions[i].LastActivity {
			t.Errorf("sessions not in DESC order: %s < %s", sessions[i-1].LastActivity, sessions[i].LastActivity)
		}
	}
}

func TestArchiveSession(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.ArchiveSession(sess.ID); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after archive: %v", err)
	}
	if got.Status != "archived" {
		t.Errorf("expected status 'archived', got %q", got.Status)
	}
}

func TestNextShortCode(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	expected := []string{"c1", "c2", "c3"}
	for _, want := range expected {
		sess := &Session{WorkspaceID: "ws1"}
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if sess.ShortCode != want {
			t.Errorf("expected short code %q, got %q", want, sess.ShortCode)
		}
	}
}

func TestCreateMessage(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &Message{SessionID: sess.ID, Role: "user", Content: "Hello"}
	if err := s.CreateMessage(msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if msg.ID == "" {
		t.Error("expected message ID to be generated")
	}

	// Verify session message_count was incremented.
	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.MessageCount != 1 {
		t.Errorf("expected message_count=1, got %d", got.MessageCount)
	}
}

func TestListMessages(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Create 5 messages.
	for i := 0; i < 5; i++ {
		msg := &Message{SessionID: sess.ID, Role: "user", Content: "msg"}
		if err := s.CreateMessage(msg); err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond) // ensure distinct timestamps
	}

	// List with limit 3.
	messages, err := s.ListMessages(sess.ID, 3)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	// Verify ASC order (the ListMessages function reverses the DESC query).
	for i := 1; i < len(messages); i++ {
		if messages[i-1].CreatedAt > messages[i].CreatedAt {
			t.Errorf("messages not in ASC order: %s > %s", messages[i-1].CreatedAt, messages[i].CreatedAt)
		}
	}
}

func TestListSessionsReturnsEmptyArray(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sessions, err := s.ListSessions("ws1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	// Must be non-nil empty slice (so JSON encodes as [] not null).
	if sessions == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}
