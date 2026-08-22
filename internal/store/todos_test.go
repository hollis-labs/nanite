package store

import (
	"context"
	"testing"
)

// D1 (CW-20260428-0014): the workspace scope was retired; the test set
// uses session/project/turn exclusively.

func TestCreateTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{
		Scope:   TodoScopeSession,
		Title:   "Fix the bug",
		ScopeID: "sess-1",
	}
	if err := s.CreateTodo(context.Background(), todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if todo.ID == "" {
		t.Error("expected ID to be generated")
	}
	if todo.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", todo.Status)
	}
	if todo.Priority != "medium" {
		t.Errorf("expected priority 'medium', got %q", todo.Priority)
	}
	if todo.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

func TestGetTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{Scope: TodoScopeProject, ScopeID: "proj-1", ProjectID: "proj-1", Title: "Write tests"}
	if err := s.CreateTodo(context.Background(), todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	got, err := s.GetTodo(context.Background(), todo.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if got.Title != "Write tests" {
		t.Errorf("Title mismatch: got %q", got.Title)
	}
	if got.Scope != TodoScopeProject {
		t.Errorf("Scope mismatch: got %q", got.Scope)
	}
	if got.ScopeID != "proj-1" {
		t.Errorf("ScopeID mismatch: got %q", got.ScopeID)
	}
	if got.ProjectID != "proj-1" {
		t.Errorf("ProjectID mismatch: got %q", got.ProjectID)
	}
}

func TestListTodosWithFilter(t *testing.T) {
	s := newTestStore(t)

	// Create todos with different scopes/statuses/priorities.
	for _, td := range []struct {
		title     string
		status    string
		priority  string
		scope     string
		scopeID   string
		projectID string
	}{
		{"A", "pending", "high", TodoScopeSession, "sess-1", ""},
		{"B", "done", "low", TodoScopeSession, "sess-1", ""},
		{"C", "pending", "medium", TodoScopeProject, "p1", "p1"},
		{"D", "blocked", "critical", TodoScopeProject, "p1", "p1"},
	} {
		todo := &Todo{
			Title:     td.title,
			Status:    td.status,
			Priority:  td.priority,
			Scope:     td.scope,
			ScopeID:   td.scopeID,
			ProjectID: td.projectID,
		}
		if err := s.CreateTodo(context.Background(), todo); err != nil {
			t.Fatalf("CreateTodo %s: %v", td.title, err)
		}
	}

	// Filter by scope=session.
	todos, err := s.ListTodos(context.Background(), TodoFilter{Scope: TodoScopeSession})
	if err != nil {
		t.Fatalf("ListTodos session: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 session todos, got %d", len(todos))
	}

	// Filter by status.
	todos, err = s.ListTodos(context.Background(), TodoFilter{Status: "pending"})
	if err != nil {
		t.Fatalf("ListTodos pending: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 pending todos, got %d", len(todos))
	}

	// Filter by scope + scope_id.
	todos, err = s.ListTodos(context.Background(), TodoFilter{Scope: TodoScopeProject, ScopeID: "p1"})
	if err != nil {
		t.Fatalf("ListTodos project p1: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 project p1 todos, got %d", len(todos))
	}

	// Filter by project_id directly (D1 — convenience filter).
	todos, err = s.ListTodos(context.Background(), TodoFilter{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("ListTodos project_id p1: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 project_id=p1 todos, got %d", len(todos))
	}

	// Filter by priority.
	todos, err = s.ListTodos(context.Background(), TodoFilter{Priority: "critical"})
	if err != nil {
		t.Fatalf("ListTodos critical: %v", err)
	}
	if len(todos) != 1 {
		t.Errorf("expected 1 critical todo, got %d", len(todos))
	}
}

func TestUpdateTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{Scope: TodoScopeSession, ScopeID: "sess-1", Title: "Original"}
	if err := s.CreateTodo(context.Background(), todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	todo.Title = "Updated"
	todo.Status = "in_progress"
	todo.Priority = "high"
	if err := s.UpdateTodo(context.Background(), todo); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	got, err := s.GetTodo(context.Background(), todo.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("Title not updated: got %q", got.Title)
	}
	if got.Status != "in_progress" {
		t.Errorf("Status not updated: got %q", got.Status)
	}
	if got.Priority != "high" {
		t.Errorf("Priority not updated: got %q", got.Priority)
	}
}

// TestUpdateTodoScope_Promote covers the D2 promote/demote action.
func TestUpdateTodoScope_Promote(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{Scope: TodoScopeSession, ScopeID: "sess-1", Title: "promote me"}
	if err := s.CreateTodo(context.Background(), todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := s.UpdateTodoScope(context.Background(), todo.ID, TodoScopeProject, "proj-1", "proj-1"); err != nil {
		t.Fatalf("UpdateTodoScope promote: %v", err)
	}
	got, _ := s.GetTodo(context.Background(), todo.ID)
	if got.Scope != TodoScopeProject || got.ProjectID != "proj-1" {
		t.Errorf("after promote: expected scope=project + project_id=proj-1, got scope=%q project_id=%q", got.Scope, got.ProjectID)
	}

	// Demote back to session.
	if err := s.UpdateTodoScope(context.Background(), todo.ID, TodoScopeSession, "sess-1", ""); err != nil {
		t.Fatalf("UpdateTodoScope demote: %v", err)
	}
	got2, _ := s.GetTodo(context.Background(), todo.ID)
	if got2.Scope != TodoScopeSession || got2.ProjectID != "" {
		t.Errorf("after demote: expected scope=session, project_id cleared, got scope=%q project_id=%q", got2.Scope, got2.ProjectID)
	}

	// Project scope without project_id is rejected.
	if err := s.UpdateTodoScope(context.Background(), todo.ID, TodoScopeProject, "proj-1", ""); err == nil {
		t.Error("expected error promoting to project without project_id, got nil")
	}
}

func TestDeleteTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{Scope: TodoScopeSession, ScopeID: "sess-1", Title: "To delete"}
	if err := s.CreateTodo(context.Background(), todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := s.DeleteTodo(context.Background(), todo.ID); err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}

	_, err := s.GetTodo(context.Background(), todo.ID)
	if err == nil {
		t.Error("expected error getting deleted todo")
	}
}

func TestTodoParentChild(t *testing.T) {
	s := newTestStore(t)

	parent := &Todo{Scope: TodoScopeSession, ScopeID: "sess-1", Title: "Parent"}
	if err := s.CreateTodo(context.Background(), parent); err != nil {
		t.Fatalf("CreateTodo parent: %v", err)
	}

	child1 := &Todo{Scope: TodoScopeSession, ScopeID: "sess-1", Title: "Child 1", ParentID: parent.ID}
	child2 := &Todo{Scope: TodoScopeSession, ScopeID: "sess-1", Title: "Child 2", ParentID: parent.ID}
	if err := s.CreateTodo(context.Background(), child1); err != nil {
		t.Fatalf("CreateTodo child1: %v", err)
	}
	if err := s.CreateTodo(context.Background(), child2); err != nil {
		t.Fatalf("CreateTodo child2: %v", err)
	}

	children, err := s.ListTodoChildren(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("ListTodoChildren: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}

	// Cascade delete: deleting parent should remove children.
	if err := s.DeleteTodo(context.Background(), parent.ID); err != nil {
		t.Fatalf("DeleteTodo parent: %v", err)
	}
	children, err = s.ListTodoChildren(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("ListTodoChildren after delete: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("expected 0 children after cascade, got %d", len(children))
	}
}
