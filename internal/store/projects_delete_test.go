package store

import (
	"context"
	"errors"
	"testing"
)

// CW-20261001-0125: deleting a project that ever had a session failed with a
// FOREIGN KEY error. Archived sessions are now detached and kept, live ones
// refuse the delete, and no session or message row is ever deleted.

func seedProjectSession(t *testing.T, s *Store, projectID, sessionID string, archived bool) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateSession(ctx, &Session{ID: sessionID, Title: "title " + sessionID, ProjectID: projectID}); err != nil {
		t.Fatalf("CreateSession(%s): %v", sessionID, err)
	}
	if err := s.CreateMessage(ctx, &Message{ID: "msg-" + sessionID, SessionID: sessionID, Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("CreateMessage(%s): %v", sessionID, err)
	}
	if archived {
		if err := s.ArchiveSession(ctx, sessionID); err != nil {
			t.Fatalf("ArchiveSession(%s): %v", sessionID, err)
		}
	}
}

func countProjectDeleteRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestDeleteProject_DetachesArchivedSessionsAndKeepsThem(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "project-delete-agent")
	if err := s.CreateProject(ctx, &Project{ID: "proj-archived", Name: "Archived only"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddAgentProject(ctx, agent.ID, "proj-archived"); err != nil {
		t.Fatalf("AddAgentProject: %v", err)
	}
	seedProjectSession(t, s, "proj-archived", "sess-archived-1", true)
	seedProjectSession(t, s, "proj-archived", "sess-archived-2", true)

	if err := s.DeleteProject(ctx, "proj-archived"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM projects WHERE id = ?`, "proj-archived"); n != 0 {
		t.Fatalf("project rows = %d, want 0", n)
	}
	if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM actor_projects WHERE project_id = ?`, "proj-archived"); n != 0 {
		t.Fatalf("agent_projects rows = %d, want 0", n)
	}
	for _, id := range []string{"sess-archived-1", "sess-archived-2"} {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			t.Fatalf("archived session %s was not kept: %v", id, err)
		}
		if sess.ProjectID != "" || sess.Status != "archived" {
			t.Fatalf("session %s = project %q status %q, want detached and still archived", id, sess.ProjectID, sess.Status)
		}
		if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, id); n != 1 {
			t.Fatalf("session %s messages = %d, want 1 kept", id, n)
		}
	}
	if _, err := s.GetAgentForActor(ctx, agent.ID); err != nil {
		t.Fatalf("agent linked to the project was affected: %v", err)
	}
}

func TestDeleteProject_LiveSessionRefusesAndChangesNothing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "project-in-use-agent")
	if err := s.CreateProject(ctx, &Project{ID: "proj-live", Name: "Live"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddAgentProject(ctx, agent.ID, "proj-live"); err != nil {
		t.Fatalf("AddAgentProject: %v", err)
	}
	seedProjectSession(t, s, "proj-live", "sess-live", false)
	seedProjectSession(t, s, "proj-live", "sess-old", true)

	err := s.DeleteProject(ctx, "proj-live")
	var inUse *ProjectInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteProject = %v, want *ProjectInUseError", err)
	}
	if len(inUse.Sessions) != 1 || inUse.Sessions[0].ID != "sess-live" || inUse.Sessions[0].Status != "active" || inUse.Sessions[0].Title != "title sess-live" {
		t.Fatalf("in-use sessions = %+v, want only the live one", inUse.Sessions)
	}

	// The refusal rolled back the archived session's detach too.
	if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM projects WHERE id = ?`, "proj-live"); n != 1 {
		t.Fatalf("project rows = %d, want 1", n)
	}
	if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM actor_projects WHERE project_id = ?`, "proj-live"); n != 1 {
		t.Fatalf("agent_projects rows = %d, want 1", n)
	}
	for _, id := range []string{"sess-live", "sess-old"} {
		sess, err := s.GetSession(ctx, id)
		if err != nil || sess.ProjectID != "proj-live" {
			t.Fatalf("session %s = %+v, %v; want still attached to proj-live", id, sess, err)
		}
	}

	// Once the live session is archived, the delete goes through.
	if err := s.ArchiveSession(ctx, "sess-live"); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := s.DeleteProject(ctx, "proj-live"); err != nil {
		t.Fatalf("DeleteProject after archiving: %v", err)
	}
	if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM sessions WHERE id IN ('sess-live', 'sess-old') AND project_id IS NULL`); n != 2 {
		t.Fatalf("detached sessions = %d, want 2", n)
	}
}

func TestDeleteProject_NoSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, &Project{ID: "proj-empty", Name: "Empty"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.DeleteProject(ctx, "proj-empty"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if n := countProjectDeleteRows(t, s, `SELECT COUNT(*) FROM projects WHERE id = ?`, "proj-empty"); n != 0 {
		t.Fatalf("project rows = %d, want 0", n)
	}
}
