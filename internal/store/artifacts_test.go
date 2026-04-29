package store

import "testing"

// TestListArtifactsByProject exercises the project-inheritance lookup added
// for F4 (CW-20260429-0004). The query joins artifacts to sessions on
// session_id and filters by sessions.project_id, with an optional exclusion
// for the active session. We seed two sessions in the same project plus one
// in another project, attach artifacts to each, and verify all three
// scenarios:
//
//	1. project filter alone returns artifacts from both same-project sessions
//	   (and nothing from the other project).
//	2. exclude_session_id filter removes the active session's artifacts.
//	3. an empty project_id returns an empty slice (no SQL fired).
func TestListArtifactsByProject(t *testing.T) {
	s := newTestStore(t)

	// Seed workspace + projects + sessions. We bypass the higher-level
	// service layer and hit the DB directly so the test stays focused on
	// the query under test.
	if _, err := s.DB.Exec(`INSERT INTO workspaces (id, name) VALUES (?, ?)`, "ws-f4", "f4"); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO projects (id, workspace_id, name) VALUES (?, ?, ?)`,
		"proj-A", "ws-f4", "Project A",
	); err != nil {
		t.Fatalf("seed proj-A: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO projects (id, workspace_id, name) VALUES (?, ?, ?)`,
		"proj-B", "ws-f4", "Project B",
	); err != nil {
		t.Fatalf("seed proj-B: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (id, short_code, workspace_id, project_id, title)
		 VALUES (?, ?, ?, ?, ?)`,
		"sess-active", "sc-1", "ws-f4", "proj-A", "active",
	); err != nil {
		t.Fatalf("seed active session: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (id, short_code, workspace_id, project_id, title)
		 VALUES (?, ?, ?, ?, ?)`,
		"sess-sibling", "sc-2", "ws-f4", "proj-A", "sibling",
	); err != nil {
		t.Fatalf("seed sibling session: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (id, short_code, workspace_id, project_id, title)
		 VALUES (?, ?, ?, ?, ?)`,
		"sess-other-project", "sc-3", "ws-f4", "proj-B", "other",
	); err != nil {
		t.Fatalf("seed other-project session: %v", err)
	}

	// Three artifacts: one per session.
	for _, a := range []*Artifact{
		{SessionID: "sess-active", Name: "active.txt", MimeType: "text/plain", StoragePath: "/tmp/a"},
		{SessionID: "sess-sibling", Name: "sibling.txt", MimeType: "text/plain", StoragePath: "/tmp/b"},
		{SessionID: "sess-other-project", Name: "other.txt", MimeType: "text/plain", StoragePath: "/tmp/c"},
	} {
		if err := s.CreateArtifact(a); err != nil {
			t.Fatalf("create artifact %s: %v", a.Name, err)
		}
	}

	// 1. proj-A returns both same-project sessions' artifacts.
	got, err := s.ListArtifactsByProject("proj-A", "")
	if err != nil {
		t.Fatalf("ListArtifactsByProject (no exclude): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 artifacts for proj-A, got %d", len(got))
	}
	for _, a := range got {
		if a.SessionID == "sess-other-project" {
			t.Fatalf("artifact from other project leaked into proj-A result: %+v", a)
		}
	}

	// 2. excluding sess-active leaves only the sibling's artifact.
	got, err = s.ListArtifactsByProject("proj-A", "sess-active")
	if err != nil {
		t.Fatalf("ListArtifactsByProject (with exclude): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 artifact after excluding active session, got %d", len(got))
	}
	if got[0].SessionID != "sess-sibling" {
		t.Fatalf("expected sibling artifact, got SessionID=%q", got[0].SessionID)
	}

	// 3. empty project_id returns an empty slice (and never fires SQL).
	got, err = s.ListArtifactsByProject("", "")
	if err != nil {
		t.Fatalf("ListArtifactsByProject (empty project): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 artifacts for empty project_id, got %d", len(got))
	}
}
