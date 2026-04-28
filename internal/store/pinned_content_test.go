package store

import (
	"testing"
)

func TestPinnedContent_CRUD(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-pin-test"
	agentID := "agent-test"

	// Create session-scoped pin.
	sid := sessionID
	err := s.CreatePinnedContent(PinnedContent{
		ID:        "pin-1",
		SessionID: &sid,
		Scope:     PinScopeSession,
		Content:   "Important context: use Go idiomatic error wrapping.",
		AgentID:   agentID,
	})
	if err != nil {
		t.Fatalf("CreatePinnedContent: %v", err)
	}

	// Create cross-session pin (nil session_id).
	err = s.CreatePinnedContent(PinnedContent{
		ID:      "pin-2",
		Scope:   PinScopeCrossSession,
		Content: "Cross-session note: prefer composition over inheritance.",
		AgentID: agentID,
	})
	if err != nil {
		t.Fatalf("CreatePinnedContent cross-session: %v", err)
	}

	// List should return both (session pins + cross_session).
	pins, err := s.ListPinnedContent(sessionID)
	if err != nil {
		t.Fatalf("ListPinnedContent: %v", err)
	}
	if len(pins) != 2 {
		t.Errorf("expected 2 pins, got %d", len(pins))
	}

	// Delete session pin.
	if err := s.DeletePinnedContent("pin-1"); err != nil {
		t.Fatalf("DeletePinnedContent: %v", err)
	}
	pins, err = s.ListPinnedContent(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 {
		t.Errorf("expected 1 pin after delete, got %d", len(pins))
	}
}

func TestPinnedContent_ClearSessionPins(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-clear-test"
	agentID := "agent-test"
	sid := sessionID

	// Session-scoped pin.
	if err := s.CreatePinnedContent(PinnedContent{
		ID: "p-sess", SessionID: &sid,
		Scope: PinScopeSession, Content: "session pin", AgentID: agentID,
	}); err != nil {
		t.Fatal(err)
	}
	// Cross-session pin — should survive clear.
	if err := s.CreatePinnedContent(PinnedContent{
		ID:      "p-cross",
		Scope:   PinScopeCrossSession,
		Content: "cross-session pin",
		AgentID: agentID,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearSessionPins(sessionID); err != nil {
		t.Fatal(err)
	}

	pins, err := s.ListPinnedContent(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	// Only cross-session pin should remain.
	if len(pins) != 1 {
		t.Errorf("expected 1 pin after clear, got %d", len(pins))
	}
	if pins[0].Scope != PinScopeCrossSession {
		t.Errorf("remaining pin should be cross_session, got %q", pins[0].Scope)
	}
}

func TestReminders_CRUD(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-reminder-test"

	// Create reminder.
	if err := s.CreateReminder(Reminder{
		ID:          "rem-1",
		SessionID:   sessionID,
		Text:        "Review the architectural decision",
		TriggerJSON: `{"type":"turn_count","n":5}`,
	}); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	// List unfired.
	unfired, err := s.ListUnfiredReminders(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfired) != 1 {
		t.Errorf("expected 1 unfired reminder, got %d", len(unfired))
	}

	// Get by ID.
	r, err := s.GetReminder("rem-1")
	if err != nil {
		t.Fatalf("GetReminder: %v", err)
	}
	if r.Text != "Review the architectural decision" {
		t.Errorf("unexpected text: %q", r.Text)
	}
	if r.FiredAt != nil {
		t.Errorf("expected FiredAt to be nil, got %v", r.FiredAt)
	}

	// Mark fired.
	if err := s.MarkReminderFired("rem-1"); err != nil {
		t.Fatalf("MarkReminderFired: %v", err)
	}
	unfired2, err := s.ListUnfiredReminders(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfired2) != 0 {
		t.Errorf("expected 0 unfired after marking fired, got %d", len(unfired2))
	}

	// Verify FiredAt is now set.
	r2, err := s.GetReminder("rem-1")
	if err != nil {
		t.Fatal(err)
	}
	if r2.FiredAt == nil {
		t.Error("expected FiredAt to be set after MarkReminderFired")
	}

	// Delete.
	if err := s.DeleteReminder("rem-1"); err != nil {
		t.Fatalf("DeleteReminder: %v", err)
	}
	_, err = s.GetReminder("rem-1")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}
