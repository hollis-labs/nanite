package store

import (
	"testing"
)

// makeProjectAndSession is a small helper that creates the project + session
// rows the post-D1 ListPinnedContent / ListUnfiredReminders queries need in
// order to surface project-scoped rows. Returns sessionID and projectID.
func makeProjectAndSession(t *testing.T, s *Store, sessionID, projectID string) {
	t.Helper()
	if err := s.CreateProject(&Project{ID: projectID, Name: "test-project", RepoPath: "/tmp/" + projectID}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.CreateSession(&Session{
		ID:        sessionID,
		ShortCode: sessionID,
		ProjectID: projectID,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

func TestPinnedContent_CRUD(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-pin-test"
	projectID := "prj-pin-test"
	agentID := "agent-test"
	makeProjectAndSession(t, s, sessionID, projectID)

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

	// Create project-scoped pin (D1 — replaces former cross_session).
	err = s.CreatePinnedContent(PinnedContent{
		ID:        "pin-2",
		SessionID: &sid,
		Scope:     PinScopeProject,
		ProjectID: projectID,
		Content:   "Project note: prefer composition over inheritance.",
		AgentID:   agentID,
	})
	if err != nil {
		t.Fatalf("CreatePinnedContent project: %v", err)
	}

	// List should return both (session pin + project pin via session->project resolution).
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
	projectID := "prj-clear-test"
	agentID := "agent-test"
	makeProjectAndSession(t, s, sessionID, projectID)
	sid := sessionID

	// Session-scoped pin.
	if err := s.CreatePinnedContent(PinnedContent{
		ID: "p-sess", SessionID: &sid,
		Scope: PinScopeSession, Content: "session pin", AgentID: agentID,
	}); err != nil {
		t.Fatal(err)
	}
	// Project-scoped pin — should survive ClearSessionPins.
	if err := s.CreatePinnedContent(PinnedContent{
		ID:        "p-proj",
		SessionID: &sid,
		Scope:     PinScopeProject,
		ProjectID: projectID,
		Content:   "project pin",
		AgentID:   agentID,
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
	// Only project pin should remain.
	if len(pins) != 1 {
		t.Errorf("expected 1 pin after clear, got %d", len(pins))
	}
	if pins[0].Scope != PinScopeProject {
		t.Errorf("remaining pin should be project, got %q", pins[0].Scope)
	}
}

// TestPinnedContent_ProjectScope_CrossSession covers the D1 acceptance criterion
// that a project-scoped pin set in one session surfaces in any other session of
// the same project.
func TestPinnedContent_ProjectScope_CrossSession(t *testing.T) {
	s := newTestStore(t)
	projectID := "prj-cross-test"
	sessA := "sess-A"
	sessB := "sess-B"
	makeProjectAndSession(t, s, sessA, projectID)
	// Add a second session in the same project (we already have the project + workspace).
	if err := s.CreateSession(&Session{
		ID: sessB, ShortCode: sessB, ProjectID: projectID,
	}); err != nil {
		t.Fatalf("CreateSession B: %v", err)
	}

	sidA := sessA
	if err := s.CreatePinnedContent(PinnedContent{
		ID: "p-proj", SessionID: &sidA,
		Scope: PinScopeProject, ProjectID: projectID,
		Content: "project decision", AgentID: "agent-test",
	}); err != nil {
		t.Fatalf("CreatePinnedContent project: %v", err)
	}
	sidB := sessB
	if err := s.CreatePinnedContent(PinnedContent{
		ID: "p-sess-B", SessionID: &sidB,
		Scope: PinScopeSession, Content: "B-only", AgentID: "agent-test",
	}); err != nil {
		t.Fatalf("CreatePinnedContent session: %v", err)
	}

	// Session A sees its project pin only (no session-A pin was created).
	pinsA, _ := s.ListPinnedContent(sessA)
	if len(pinsA) != 1 || pinsA[0].ID != "p-proj" {
		t.Errorf("session A pins: expected [p-proj], got %+v", pinsA)
	}

	// Session B sees both: its session-pin AND the project pin.
	pinsB, _ := s.ListPinnedContent(sessB)
	if len(pinsB) != 2 {
		t.Errorf("session B pins: expected 2, got %d (%+v)", len(pinsB), pinsB)
	}

	// Promote a session pin to project — UpdatePinScope.
	if err := s.UpdatePinScope("p-sess-B", PinScopeProject, projectID); err != nil {
		t.Fatalf("UpdatePinScope: %v", err)
	}
	pinsAAfter, _ := s.ListPinnedContent(sessA)
	if len(pinsAAfter) != 2 {
		t.Errorf("after promote: session A should see both pins, got %d", len(pinsAAfter))
	}
}

func TestReminders_CRUD(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-reminder-test"
	projectID := "prj-reminder-test"
	makeProjectAndSession(t, s, sessionID, projectID)

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
	if r.Scope != ReminderScopeSession {
		t.Errorf("expected default scope=session, got %q", r.Scope)
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

// TestReminders_ProjectScope_CrossSession covers the D1 acceptance criterion
// that a project-scoped reminder set in one session surfaces in any other
// session of the same project (per the scope-resolution path).
func TestReminders_ProjectScope_CrossSession(t *testing.T) {
	s := newTestStore(t)
	projectID := "prj-rem-cross"
	sessA := "sess-rem-A"
	sessB := "sess-rem-B"
	makeProjectAndSession(t, s, sessA, projectID)
	if err := s.CreateSession(&Session{
		ID: sessB, ShortCode: sessB, ProjectID: projectID,
	}); err != nil {
		t.Fatalf("CreateSession B: %v", err)
	}

	// Project-scoped reminder created from session A.
	if err := s.CreateReminder(Reminder{
		ID:          "rem-proj",
		SessionID:   sessA,
		Scope:       ReminderScopeProject,
		ProjectID:   projectID,
		Text:        "project-wide note",
		TriggerJSON: `{"type":"turn_count","n":1}`,
	}); err != nil {
		t.Fatalf("CreateReminder project: %v", err)
	}
	// Session-scoped reminder created from session A — should NOT surface in B.
	if err := s.CreateReminder(Reminder{
		ID:          "rem-sess-A",
		SessionID:   sessA,
		Scope:       ReminderScopeSession,
		Text:        "A-only",
		TriggerJSON: `{"type":"turn_count","n":1}`,
	}); err != nil {
		t.Fatalf("CreateReminder session: %v", err)
	}

	listA, err := s.ListUnfiredReminders(sessA)
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 2 {
		t.Errorf("session A: expected 2 reminders, got %d", len(listA))
	}

	listB, err := s.ListUnfiredReminders(sessB)
	if err != nil {
		t.Fatal(err)
	}
	if len(listB) != 1 || listB[0].ID != "rem-proj" {
		t.Errorf("session B: expected [rem-proj], got %+v", listB)
	}

	// Promote the session-A reminder to project — should now surface in B.
	if err := s.UpdateReminderScope("rem-sess-A", ReminderScopeProject, projectID); err != nil {
		t.Fatalf("UpdateReminderScope: %v", err)
	}
	listBAfter, _ := s.ListUnfiredReminders(sessB)
	if len(listBAfter) != 2 {
		t.Errorf("after promote: session B should see both reminders, got %d", len(listBAfter))
	}
}

// TestReminders_TurnScope_DoesNotLeak verifies turn-scoped reminders behave like
// session-scoped reminders for the purposes of cross-session listing — they do
// NOT leak into other sessions.
func TestReminders_TurnScope_DoesNotLeak(t *testing.T) {
	s := newTestStore(t)
	projectID := "prj-turn-test"
	sessA := "sess-turn-A"
	sessB := "sess-turn-B"
	makeProjectAndSession(t, s, sessA, projectID)
	if err := s.CreateSession(&Session{
		ID: sessB, ShortCode: sessB, ProjectID: projectID,
	}); err != nil {
		t.Fatalf("CreateSession B: %v", err)
	}

	if err := s.CreateReminder(Reminder{
		ID:          "rem-turn",
		SessionID:   sessA,
		Scope:       ReminderScopeTurn,
		Text:        "this turn only",
		TriggerJSON: `{"type":"turn_count","n":1}`,
	}); err != nil {
		t.Fatalf("CreateReminder turn: %v", err)
	}

	listA, _ := s.ListUnfiredReminders(sessA)
	if len(listA) != 1 {
		t.Errorf("session A turn-scoped: expected 1 reminder, got %d", len(listA))
	}
	listB, _ := s.ListUnfiredReminders(sessB)
	if len(listB) != 0 {
		t.Errorf("session B should not see session A turn-scoped reminders, got %d", len(listB))
	}
}
