package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Todo represents an internal todo item scoped to workspace, project, or session.
type Todo struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`       // workspace, project, session
	ScopeID     string `json:"scope_id"`    // empty for workspace, project_id or session_id
	ParentID    string `json:"parent_id"`   // optional parent todo for nesting
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`   // pending, in_progress, done, blocked
	Priority    string `json:"priority"` // low, medium, high, critical
	Labels      string `json:"labels"`   // JSON array of strings
	Metadata    string `json:"metadata"` // JSON object
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// TodoFilter controls which todos to retrieve.
type TodoFilter struct {
	Scope    string
	ScopeID  string
	Status   string
	Priority string
	ParentID string // use "*" to mean "has a parent" (children only)
	Labels   string // comma-separated labels to match (any match)
}

// CreateTodo inserts a new todo, auto-generating the ID if empty.
func (s *Store) CreateTodo(t *Todo) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if t.Status == "" {
		t.Status = "pending"
	}
	if t.Priority == "" {
		t.Priority = "medium"
	}
	if t.Labels == "" {
		t.Labels = "[]"
	}
	if t.Metadata == "" {
		t.Metadata = "{}"
	}
	if t.CreatedBy == "" {
		t.CreatedBy = "user"
	}
	t.CreatedAt = now
	t.UpdatedAt = now

	_, err := s.DB.Exec(
		`INSERT INTO todos (id, scope, scope_id, parent_id, title, description, status, priority, labels, metadata, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Scope, t.ScopeID, nullIfEmpty(t.ParentID),
		t.Title, t.Description, t.Status, t.Priority,
		t.Labels, t.Metadata, t.CreatedBy, now, now,
	)
	if err != nil {
		return fmt.Errorf("create todo: %w", err)
	}
	return nil
}

// GetTodo returns a single todo by ID.
func (s *Store) GetTodo(id string) (*Todo, error) {
	var t Todo
	err := s.DB.QueryRow(
		`SELECT id, scope, scope_id, COALESCE(parent_id,''), title, description,
		        status, priority, labels, metadata, created_by, created_at, updated_at
		 FROM todos WHERE id = ?`, id,
	).Scan(
		&t.ID, &t.Scope, &t.ScopeID, &t.ParentID, &t.Title, &t.Description,
		&t.Status, &t.Priority, &t.Labels, &t.Metadata, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get todo %s: %w", id, err)
	}
	return &t, nil
}

// ListTodos returns todos matching the given filter.
func (s *Store) ListTodos(f TodoFilter) ([]Todo, error) {
	query := `SELECT id, scope, scope_id, COALESCE(parent_id,''), title, description,
	                 status, priority, labels, metadata, created_by, created_at, updated_at
	          FROM todos WHERE 1=1`
	var args []any

	if f.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, f.Scope)
	}
	if f.ScopeID != "" {
		query += ` AND scope_id = ?`
		args = append(args, f.ScopeID)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Priority != "" {
		query += ` AND priority = ?`
		args = append(args, f.Priority)
	}
	if f.ParentID != "" {
		if f.ParentID == "*" {
			query += ` AND parent_id IS NOT NULL`
		} else {
			query += ` AND parent_id = ?`
			args = append(args, f.ParentID)
		}
	}
	if f.Labels != "" {
		// Match any label in the comma-separated list using JSON LIKE.
		for _, label := range strings.Split(f.Labels, ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				query += ` AND labels LIKE ?`
				args = append(args, "%"+label+"%")
			}
		}
	}

	query += ` ORDER BY created_at DESC`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()

	out := make([]Todo, 0)
	for rows.Next() {
		var t Todo
		if err := rows.Scan(
			&t.ID, &t.Scope, &t.ScopeID, &t.ParentID, &t.Title, &t.Description,
			&t.Status, &t.Priority, &t.Labels, &t.Metadata, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTodo updates mutable fields on a todo. Only non-empty fields are changed.
func (s *Store) UpdateTodo(t *Todo) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE todos SET title = ?, description = ?, status = ?, priority = ?,
		        labels = ?, metadata = ?, updated_at = ?
		 WHERE id = ?`,
		t.Title, t.Description, t.Status, t.Priority,
		t.Labels, t.Metadata, now, t.ID,
	)
	if err != nil {
		return fmt.Errorf("update todo %s: %w", t.ID, err)
	}
	t.UpdatedAt = now
	return nil
}

// DeleteTodo removes a todo by ID. Children are cascade-deleted by the schema.
func (s *Store) DeleteTodo(id string) error {
	_, err := s.DB.Exec(`DELETE FROM todos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete todo %s: %w", id, err)
	}
	return nil
}

// ListTodoChildren returns direct children of a todo.
func (s *Store) ListTodoChildren(parentID string) ([]Todo, error) {
	return s.ListTodos(TodoFilter{ParentID: parentID})
}
