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
