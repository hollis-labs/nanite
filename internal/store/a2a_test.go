package store

import (
	"testing"
)

// TestSendA2AMessage_NewSchema verifies that SendA2AMessage persists a message
// with the new session-scoped addressing and returns the inserted row with
// defaults populated.
func TestSendA2AMessage_NewSchema(t *testing.T) {
	s := newTestStore(t)

	in := &A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   "file-a",
		ToSessionID:   "sess-1",
		ToAgentID:     "file-b",
		Body:          "hello",
	}

	out, err := s.SendA2AMessage(in)
	if err != nil {
		t.Fatalf("SendA2AMessage: %v", err)
	}
	if out == nil {
		t.Fatal("SendA2AMessage returned nil message")
	}
	if out.ID == "" {
		t.Error("expected non-empty ID")
	}
	if out.Status != "unread" {
		t.Errorf("status = %q, want %q", out.Status, "unread")
	}
	if out.Type != "message" {
		t.Errorf("type = %q, want %q", out.Type, "message")
	}
	if out.Priority != 2 {
		t.Errorf("priority = %d, want 2", out.Priority)
	}
	if out.FromSessionID != "sess-1" {
		t.Errorf("from_session_id = %q, want %q", out.FromSessionID, "sess-1")
	}
	if out.FromAgentID != "file-a" {
		t.Errorf("from_agent_id = %q, want %q", out.FromAgentID, "file-a")
	}
	if out.ToSessionID != "sess-1" {
		t.Errorf("to_session_id = %q, want %q", out.ToSessionID, "sess-1")
	}
	if out.ToAgentID != "file-b" {
		t.Errorf("to_agent_id = %q, want %q", out.ToAgentID, "file-b")
	}
	if out.Body != "hello" {
		t.Errorf("body = %q, want %q", out.Body, "hello")
	}
	if out.ThreadID == "" {
		t.Error("expected ThreadID to default to message ID")
	}
	if out.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

// TestGetA2AInbox_FiltersBySessionAndAgent verifies that the inbox query
// correctly scopes by (to_session_id, to_agent_id) and ignores messages
// addressed to other agents or sessions.
func TestGetA2AInbox_FiltersBySessionAndAgent(t *testing.T) {
	s := newTestStore(t)

	// Two messages for (sess-1, file-a).
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-b",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "hi 1",
	}); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-c",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "hi 2",
	}); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	// One message for (sess-1, file-b).
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-a",
		ToSessionID: "sess-1", ToAgentID: "file-b",
		Body: "hi 3",
	}); err != nil {
		t.Fatalf("seed 3: %v", err)
	}

	msgs, err := s.GetA2AInbox("sess-1", "file-a", "")
	if err != nil {
		t.Fatalf("GetA2AInbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	for _, m := range msgs {
		if m.ToSessionID != "sess-1" || m.ToAgentID != "file-a" {
			t.Errorf("unexpected recipient: session=%q agent=%q", m.ToSessionID, m.ToAgentID)
		}
	}
}

// TestGetA2AInbox_FiltersByStatus verifies that the status filter narrows
// results to unread or read messages.
func TestGetA2AInbox_FiltersByStatus(t *testing.T) {
	s := newTestStore(t)

	m1, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-b",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "m1",
	})
	if err != nil {
		t.Fatalf("seed m1: %v", err)
	}
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-b",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "m2",
	}); err != nil {
		t.Fatalf("seed m2: %v", err)
	}

	if err := s.AckA2AMessage(m1.ID); err != nil {
		t.Fatalf("AckA2AMessage: %v", err)
	}

	unread, err := s.GetA2AInbox("sess-1", "file-a", "unread")
	if err != nil {
		t.Fatalf("GetA2AInbox unread: %v", err)
	}
	if len(unread) != 1 {
		t.Errorf("unread count = %d, want 1", len(unread))
	}

	read, err := s.GetA2AInbox("sess-1", "file-a", "read")
	if err != nil {
		t.Fatalf("GetA2AInbox read: %v", err)
	}
	if len(read) != 1 {
		t.Errorf("read count = %d, want 1", len(read))
	}
}

// TestGetA2ARecent verifies that recent messages are returned in chronological
// order (oldest first) after the DESC-then-reverse transformation.
func TestGetA2ARecent(t *testing.T) {
	s := newTestStore(t)

	bodies := []string{"one", "two", "three", "four", "five"}
	for _, b := range bodies {
		if _, err := s.SendA2AMessage(&A2AMessage{
			FromSessionID: "sess-1", FromAgentID: "file-a",
			ToSessionID: "sess-1", ToAgentID: "file-b",
			Body: b,
		}); err != nil {
			t.Fatalf("seed %q: %v", b, err)
		}
	}

	got, err := s.GetA2ARecent("sess-1", 3)
	if err != nil {
		t.Fatalf("GetA2ARecent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}

	// Expect the 3 most recent in chronological order: three, four, five.
	want := []string{"three", "four", "five"}
	for i, m := range got {
		if m.Body != want[i] {
			t.Errorf("msg[%d].body = %q, want %q", i, m.Body, want[i])
		}
	}
}

// TestA2AUnreadCount verifies that the count is scoped to (to_session_id,
// to_agent_id) and only counts unread messages, ignoring messages the agent
// sent.
func TestA2AUnreadCount(t *testing.T) {
	s := newTestStore(t)

	// file-a receives 2 messages in sess-1.
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-b",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "to a 1",
	}); err != nil {
		t.Fatalf("seed to-a 1: %v", err)
	}
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-c",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "to a 2",
	}); err != nil {
		t.Fatalf("seed to-a 2: %v", err)
	}
	// file-a sends a message (should not count).
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "file-a",
		ToSessionID: "sess-1", ToAgentID: "file-b",
		Body: "from a",
	}); err != nil {
		t.Fatalf("seed from-a: %v", err)
	}
	// A message in a different session (should not count).
	if _, err := s.SendA2AMessage(&A2AMessage{
		FromSessionID: "sess-2", FromAgentID: "file-b",
		ToSessionID: "sess-2", ToAgentID: "file-a",
		Body: "other sess",
	}); err != nil {
		t.Fatalf("seed other-sess: %v", err)
	}

	n, err := s.A2AUnreadCount("sess-1", "file-a")
	if err != nil {
		t.Fatalf("A2AUnreadCount: %v", err)
	}
	if n != 2 {
		t.Errorf("unread count = %d, want 2", n)
	}
}
