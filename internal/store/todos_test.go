package store

import (
	"testing"
)

func TestCreateTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{
		Scope:   "workspace",
		Title:   "Fix the bug",
		ScopeID: "",
	}
	if err := s.CreateTodo(todo); err != nil {
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

	todo := &Todo{Scope: "project", ScopeID: "proj-1", Title: "Write tests"}
	if err := s.CreateTodo(todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	got, err := s.GetTodo(todo.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if got.Title != "Write tests" {
		t.Errorf("Title mismatch: got %q", got.Title)
	}
	if got.Scope != "project" {
		t.Errorf("Scope mismatch: got %q", got.Scope)
	}
	if got.ScopeID != "proj-1" {
		t.Errorf("ScopeID mismatch: got %q", got.ScopeID)
	}
}

func TestListTodosWithFilter(t *testing.T) {
	s := newTestStore(t)

	// Create todos with different statuses and priorities.
	for _, td := range []struct {
		title    string
		status   string
		priority string
		scope    string
		scopeID  string
	}{
		{"A", "pending", "high", "workspace", ""},
		{"B", "done", "low", "workspace", ""},
		{"C", "pending", "medium", "project", "p1"},
		{"D", "blocked", "critical", "project", "p1"},
	} {
		todo := &Todo{
			Title:    td.title,
			Status:   td.status,
			Priority: td.priority,
			Scope:    td.scope,
			ScopeID:  td.scopeID,
		}
		if err := s.CreateTodo(todo); err != nil {
			t.Fatalf("CreateTodo %s: %v", td.title, err)
		}
	}

	// Filter by scope.
	todos, err := s.ListTodos(TodoFilter{Scope: "workspace"})
	if err != nil {
		t.Fatalf("ListTodos workspace: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 workspace todos, got %d", len(todos))
	}

	// Filter by status.
	todos, err = s.ListTodos(TodoFilter{Status: "pending"})
	if err != nil {
		t.Fatalf("ListTodos pending: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 pending todos, got %d", len(todos))
	}

	// Filter by scope + scope_id.
	todos, err = s.ListTodos(TodoFilter{Scope: "project", ScopeID: "p1"})
	if err != nil {
		t.Fatalf("ListTodos project p1: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("expected 2 project p1 todos, got %d", len(todos))
	}

	// Filter by priority.
	todos, err = s.ListTodos(TodoFilter{Priority: "critical"})
	if err != nil {
		t.Fatalf("ListTodos critical: %v", err)
	}
	if len(todos) != 1 {
		t.Errorf("expected 1 critical todo, got %d", len(todos))
	}
}

func TestUpdateTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{Scope: "workspace", Title: "Original"}
	if err := s.CreateTodo(todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	todo.Title = "Updated"
	todo.Status = "in_progress"
	todo.Priority = "high"
	if err := s.UpdateTodo(todo); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	got, err := s.GetTodo(todo.ID)
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

func TestDeleteTodo(t *testing.T) {
	s := newTestStore(t)

	todo := &Todo{Scope: "workspace", Title: "To delete"}
	if err := s.CreateTodo(todo); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := s.DeleteTodo(todo.ID); err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}

	_, err := s.GetTodo(todo.ID)
	if err == nil {
		t.Error("expected error getting deleted todo")
	}
}

func TestTodoParentChild(t *testing.T) {
	s := newTestStore(t)

	parent := &Todo{Scope: "workspace", Title: "Parent"}
	if err := s.CreateTodo(parent); err != nil {
		t.Fatalf("CreateTodo parent: %v", err)
	}

	child1 := &Todo{Scope: "workspace", Title: "Child 1", ParentID: parent.ID}
	child2 := &Todo{Scope: "workspace", Title: "Child 2", ParentID: parent.ID}
	if err := s.CreateTodo(child1); err != nil {
		t.Fatalf("CreateTodo child1: %v", err)
	}
	if err := s.CreateTodo(child2); err != nil {
		t.Fatalf("CreateTodo child2: %v", err)
	}

	children, err := s.ListTodoChildren(parent.ID)
	if err != nil {
		t.Fatalf("ListTodoChildren: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}

	// Cascade delete: deleting parent should remove children.
	if err := s.DeleteTodo(parent.ID); err != nil {
		t.Fatalf("DeleteTodo parent: %v", err)
	}
	children, err = s.ListTodoChildren(parent.ID)
	if err != nil {
		t.Fatalf("ListTodoChildren after delete: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("expected 0 children after cascade, got %d", len(children))
	}
}
