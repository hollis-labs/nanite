package store

import "testing"

func TestCreateWorkspace(t *testing.T) {
	s := newTestStore(t)

	w := &Workspace{ID: "ws-test", Name: "Test WS", Description: "A test workspace"}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if w.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
	if w.Settings != "{}" {
		t.Errorf("expected default settings '{}', got %q", w.Settings)
	}
}

func TestGetWorkspace(t *testing.T) {
	s := newTestStore(t)

	w := &Workspace{ID: "ws-get", Name: "Get WS", Description: "desc", Icon: "star"}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	got, err := s.GetWorkspace("ws-get")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.Name != "Get WS" {
		t.Errorf("Name mismatch: got %q, want %q", got.Name, "Get WS")
	}
	if got.Description != "desc" {
		t.Errorf("Description mismatch: got %q, want %q", got.Description, "desc")
	}
	if got.Icon != "star" {
		t.Errorf("Icon mismatch: got %q, want %q", got.Icon, "star")
	}
}

func TestListWorkspaces(t *testing.T) {
	s := newTestStore(t)

	for _, id := range []string{"ws-a", "ws-b"} {
		if err := s.CreateWorkspace(&Workspace{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateWorkspace %s: %v", id, err)
		}
	}

	ws, err := s.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(ws))
	}
}

// TestResolveWorkspaceID_SingleWorkspaceDefaultsOnEmpty is the
// CW-20260815-0010 case: with exactly one real workspace, an empty
// requested id resolves to it instead of forcing the caller to be
// explicit.
func TestResolveWorkspaceID_SingleWorkspaceDefaultsOnEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateWorkspace(&Workspace{ID: "default", Name: "Default"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	got, err := s.ResolveWorkspaceID("")
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if got != "default" {
		t.Errorf("expected 'default', got %q", got)
	}
}

// TestResolveWorkspaceID_SingleWorkspaceDefaultsOnStaleID covers a
// requested id that names a workspace that no longer exists (e.g. a
// browser's persisted id for a workspace removed by consolidation) — it
// must resolve to the sole remaining workspace, not error, the same as
// an empty request.
func TestResolveWorkspaceID_SingleWorkspaceDefaultsOnStaleID(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateWorkspace(&Workspace{ID: "default", Name: "Default"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	got, err := s.ResolveWorkspaceID("personal")
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if got != "default" {
		t.Errorf("expected fallback to 'default' for a stale/unknown id, got %q", got)
	}
}

// TestResolveWorkspaceID_ValidIDPassesThrough proves a real, existing id
// is returned as-is (not silently overridden), even when other workspaces
// exist too.
func TestResolveWorkspaceID_ValidIDPassesThrough(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"ws-a", "ws-b"} {
		if err := s.CreateWorkspace(&Workspace{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateWorkspace %s: %v", id, err)
		}
	}

	got, err := s.ResolveWorkspaceID("ws-b")
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if got != "ws-b" {
		t.Errorf("expected 'ws-b' to pass through unchanged, got %q", got)
	}
}

// TestResolveWorkspaceID_AmbiguousMultipleWorkspacesRequiresExplicit
// proves the fallback is scoped to the single-workspace case only: with
// more than one real workspace, an empty or unrecognized request must
// still come back empty so the caller surfaces a real "be explicit"
// error rather than silently guessing among several.
func TestResolveWorkspaceID_AmbiguousMultipleWorkspacesRequiresExplicit(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"ws-a", "ws-b"} {
		if err := s.CreateWorkspace(&Workspace{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateWorkspace %s: %v", id, err)
		}
	}

	got, err := s.ResolveWorkspaceID("")
	if err != nil {
		t.Fatalf("ResolveWorkspaceID: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty result (ambiguous — 2 workspaces exist), got %q", got)
	}
}

// TestResolveWorkspaceID_RealDBErrorNotMasked is the PR #242 review fix:
// a genuine DB/query failure while checking whether `requested` exists
// must propagate to the caller, not be silently treated the same as
// "unknown id, fall back to the single workspace." Simulated by closing
// the underlying DB connection before calling — every query fails, and
// none of those failures is sql.ErrNoRows.
func TestResolveWorkspaceID_RealDBErrorNotMasked(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateWorkspace(&Workspace{ID: "default", Name: "Default"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatalf("close DB: %v", err)
	}

	if _, err := s.ResolveWorkspaceID("some-id"); err == nil {
		t.Fatal("expected a real DB error to propagate from ResolveWorkspaceID, got nil")
	}
}

func TestUpdateWorkspace(t *testing.T) {
	s := newTestStore(t)

	w := &Workspace{ID: "ws-upd", Name: "Original"}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	w.Name = "Updated"
	w.Description = "new desc"
	if err := s.UpdateWorkspace(w); err != nil {
		t.Fatalf("UpdateWorkspace: %v", err)
	}

	got, err := s.GetWorkspace("ws-upd")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.Name != "Updated" {
		t.Errorf("Name not updated: got %q", got.Name)
	}
	if got.Description != "new desc" {
		t.Errorf("Description not updated: got %q", got.Description)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	s := newTestStore(t)

	w := &Workspace{ID: "ws-del", Name: "Delete Me"}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := s.DeleteWorkspace("ws-del"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	_, err := s.GetWorkspace("ws-del")
	if err == nil {
		t.Fatal("expected error getting deleted workspace, got nil")
	}
}

func TestListWorkspacesReturnsEmptyArray(t *testing.T) {
	s := newTestStore(t)

	ws, err := s.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if ws == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(ws) != 0 {
		t.Fatalf("expected 0 workspaces, got %d", len(ws))
	}
}
