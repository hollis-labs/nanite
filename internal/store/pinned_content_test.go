package store

import (
	"context"
	"testing"
)

// makeProjectAndSession is a small helper that creates the project + session
// rows the post-D1 ListPinnedContent queries need in
// order to surface project-scoped rows. Returns sessionID and projectID.
func makeProjectAndSession(t *testing.T, s *Store, sessionID, projectID string) {
	t.Helper()
	if err := s.CreateProject(context.Background(), &Project{ID: projectID, Name: "test-project", RepoPath: "/tmp/" + projectID}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.CreateSession(context.Background(), &Session{
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
	err := s.CreatePinnedContent(context.Background(), PinnedContent{
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
	err = s.CreatePinnedContent(context.Background(), PinnedContent{
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
	pins, err := s.ListPinnedContent(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListPinnedContent: %v", err)
	}
	if len(pins) != 2 {
		t.Errorf("expected 2 pins, got %d", len(pins))
	}

	// Delete session pin.
	if err := s.DeletePinnedContent(context.Background(), "pin-1"); err != nil {
		t.Fatalf("DeletePinnedContent: %v", err)
	}
	pins, err = s.ListPinnedContent(context.Background(), sessionID)
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
	if err := s.CreatePinnedContent(context.Background(), PinnedContent{
		ID: "p-sess", SessionID: &sid,
		Scope: PinScopeSession, Content: "session pin", AgentID: agentID,
	}); err != nil {
		t.Fatal(err)
	}
	// Project-scoped pin — should survive ClearSessionPins.
	if err := s.CreatePinnedContent(context.Background(), PinnedContent{
		ID:        "p-proj",
		SessionID: &sid,
		Scope:     PinScopeProject,
		ProjectID: projectID,
		Content:   "project pin",
		AgentID:   agentID,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearSessionPins(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}

	pins, err := s.ListPinnedContent(context.Background(), sessionID)
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
	if err := s.CreateSession(context.Background(), &Session{
		ID: sessB, ShortCode: sessB, ProjectID: projectID,
	}); err != nil {
		t.Fatalf("CreateSession B: %v", err)
	}

	sidA := sessA
	if err := s.CreatePinnedContent(context.Background(), PinnedContent{
		ID: "p-proj", SessionID: &sidA,
		Scope: PinScopeProject, ProjectID: projectID,
		Content: "project decision", AgentID: "agent-test",
	}); err != nil {
		t.Fatalf("CreatePinnedContent project: %v", err)
	}
	sidB := sessB
	if err := s.CreatePinnedContent(context.Background(), PinnedContent{
		ID: "p-sess-B", SessionID: &sidB,
		Scope: PinScopeSession, Content: "B-only", AgentID: "agent-test",
	}); err != nil {
		t.Fatalf("CreatePinnedContent session: %v", err)
	}

	// Session A sees its project pin only (no session-A pin was created).
	pinsA, _ := s.ListPinnedContent(context.Background(), sessA)
	if len(pinsA) != 1 || pinsA[0].ID != "p-proj" {
		t.Errorf("session A pins: expected [p-proj], got %+v", pinsA)
	}

	// Session B sees both: its session-pin AND the project pin.
	pinsB, _ := s.ListPinnedContent(context.Background(), sessB)
	if len(pinsB) != 2 {
		t.Errorf("session B pins: expected 2, got %d (%+v)", len(pinsB), pinsB)
	}

	// Promote a session pin to project — UpdatePinScope.
	if err := s.UpdatePinScope(context.Background(), "p-sess-B", PinScopeProject, projectID); err != nil {
		t.Fatalf("UpdatePinScope: %v", err)
	}
	pinsAAfter, _ := s.ListPinnedContent(context.Background(), sessA)
	if len(pinsAAfter) != 2 {
		t.Errorf("after promote: session A should see both pins, got %d", len(pinsAAfter))
	}
}
