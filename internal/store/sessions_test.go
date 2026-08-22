package store

import (
	"context"
	"testing"
	"time"
)

func TestCreateSession(t *testing.T) {
	s := newTestStore(t)

	sess := &Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
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

	sess := &Session{Title: "Test Chat"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(context.Background(), sess.ID)
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
	if got.Status != "active" {
		t.Errorf("Status mismatch: got %q, want %q", got.Status, "active")
	}
}

func TestListSessions(t *testing.T) {
	s := newTestStore(t)

	// Create sessions with small delays to guarantee ordering.
	for i := 0; i < 3; i++ {
		sess := &Session{Title: "Chat"}
		if err := s.CreateSession(context.Background(), sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		// Sleep briefly so last_activity differs.
		time.Sleep(10 * time.Millisecond)
	}

	sessions, err := s.ListSessions(context.Background())
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

	sess := &Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.ArchiveSession(context.Background(), sess.ID); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	got, err := s.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession after archive: %v", err)
	}
	if got.Status != "archived" {
		t.Errorf("expected status 'archived', got %q", got.Status)
	}
}

func TestNextShortCode(t *testing.T) {
	s := newTestStore(t)

	expected := []string{"c1", "c2", "c3"}
	for _, want := range expected {
		sess := &Session{}
		if err := s.CreateSession(context.Background(), sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if sess.ShortCode != want {
			t.Errorf("expected short code %q, got %q", want, sess.ShortCode)
		}
	}
}

func TestCreateMessage(t *testing.T) {
	s := newTestStore(t)

	sess := &Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &Message{SessionID: sess.ID, Role: "user", Content: "Hello"}
	if err := s.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if msg.ID == "" {
		t.Error("expected message ID to be generated")
	}

	// Verify session message_count was incremented.
	got, err := s.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.MessageCount != 1 {
		t.Errorf("expected message_count=1, got %d", got.MessageCount)
	}
}

func TestListMessages(t *testing.T) {
	s := newTestStore(t)

	sess := &Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Create 5 messages.
	for i := 0; i < 5; i++ {
		msg := &Message{SessionID: sess.ID, Role: "user", Content: "msg"}
		if err := s.CreateMessage(context.Background(), msg); err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond) // ensure distinct timestamps
	}

	// List with limit 3.
	messages, err := s.ListMessages(context.Background(), sess.ID, 3)
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

// TestForkSession_AtomicMessages verifies that ForkSession copies source
// messages into the new session as a single atomic unit: on success, all
// messages are present and message_count matches.
func TestForkSession_AtomicMessages(t *testing.T) {
	s := newTestStore(t)

	src := &Session{Title: "Source"}
	if err := s.CreateSession(context.Background(), src); err != nil {
		t.Fatalf("CreateSession src: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := s.CreateMessage(context.Background(), &Message{SessionID: src.ID, Role: "user", Content: "m"}); err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
	}

	forked, err := s.ForkSession(context.Background(), src.ID, nil, true)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forked.ID == src.ID {
		t.Fatal("forked session should have a new ID")
	}
	if forked.MessageCount != 5 {
		t.Errorf("expected MessageCount=5, got %d", forked.MessageCount)
	}

	got, err := s.ListMessages(context.Background(), forked.ID, 100)
	if err != nil {
		t.Fatalf("ListMessages on fork: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("expected 5 messages in fork, got %d", len(got))
	}

	// Persisted message_count matches.
	reloaded, err := s.GetSession(context.Background(), forked.ID)
	if err != nil {
		t.Fatalf("GetSession fork: %v", err)
	}
	if reloaded.MessageCount != 5 {
		t.Errorf("persisted MessageCount=5 expected, got %d", reloaded.MessageCount)
	}
}

// TestCopyMessages_AtomicOnFailure is a regression guard for the audit
// finding that message copy was non-atomic. Attempts to copy into a
// nonexistent target session, which fails the FK on messages.session_id, and
// verifies no partial messages leaked into the messages table.
func TestCopyMessages_AtomicOnFailure(t *testing.T) {
	s := newTestStore(t)

	src := &Session{}
	if err := s.CreateSession(context.Background(), src); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.CreateMessage(context.Background(), &Message{SessionID: src.ID, Role: "user", Content: "m"}); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	bogusTarget := "no-such-session-id"
	err := s.CopyMessages(context.Background(), src.ID, bogusTarget)
	if err == nil {
		t.Fatal("expected CopyMessages to fail for nonexistent target")
	}

	// No partial messages should have been written to the bogus target.
	var n int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = ?", bogusTarget).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 messages for bogus target, got %d — CopyMessages was not atomic", n)
	}
}

func TestListSessionsReturnsEmptyArray(t *testing.T) {
	s := newTestStore(t)

	sessions, err := s.ListSessions(context.Background())
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

func TestArchiveSession_EvictsSessionObjects(t *testing.T) {
	// D5 guarantee: archiving a session must also remove its session_objects
	// rows — in the same transaction, so a failure can't leave orphan objects
	// attached to an archived session.
	s := newTestStore(t)
	sess := makeTestSession(t, s)

	// Put two objects on this session and one on a sibling session (control).
	for i := 0; i < 2; i++ {
		if _, err := s.PutSessionObject(context.Background(), SessionObjectInput{SessionID: sess.ID, Payload: `{}`}); err != nil {
			t.Fatalf("put %d on sess: %v", i, err)
		}
	}
	sibling := makeTestSession(t, s)
	siblingObj, err := s.PutSessionObject(context.Background(), SessionObjectInput{SessionID: sibling.ID, Payload: `{}`})
	if err != nil {
		t.Fatalf("put on sibling: %v", err)
	}

	if err := s.ArchiveSession(context.Background(), sess.ID); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	// Archived-session objects should be gone.
	list, err := s.ListSessionObjects(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("list after archive: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 rows for archived session, got %d", len(list))
	}

	// Sibling-session objects must be untouched.
	if _, err := s.GetSessionObject(context.Background(), sibling.ID, siblingObj.ID); err != nil {
		t.Errorf("sibling object should still be present: %v", err)
	}

	// Archive itself should still have succeeded.
	got, err := s.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("get archived session: %v", err)
	}
	if got.Status != "archived" {
		t.Errorf("expected status=archived, got %q", got.Status)
	}
}
