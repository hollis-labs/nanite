package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Todo scope constants (D1, CW-20260428-0014).
//
// Workspace-scoped todos were dropped in migration 043 — workspace is
// out of scope for the project/session/turn dimension exposed to users.
const (
	TodoScopeTurn    = "turn"    // lives within the current turn (still persisted)
	TodoScopeSession = "session" // default — bound to a single session
	TodoScopeProject = "project" // surfaces in any session of the same project
)

// Todo represents an internal todo item scoped to project, session, or turn.
//
// Scope semantics (D1, CW-20260428-0014):
//   - turn    — ScopeID = session_id (turn lives inside a session).
//   - session — ScopeID = session_id.
//   - project — ScopeID = project_id and ProjectID is also populated.
//
// ProjectID is the canonical project pointer; it is populated independently
// of ScopeID so cross-session lookups do not need to pivot through `scope`.
type Todo struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`    // turn, session, project
	ScopeID     string `json:"scope_id"` // session_id (turn/session) or project_id (project)
	ProjectID   string `json:"project_id,omitempty"`
	ParentID    string `json:"parent_id"` // optional parent todo for nesting
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
	Scope     string
	ScopeID   string
	ProjectID string
	Status    string
	Priority  string
	ParentID  string // use "*" to mean "has a parent" (children only)
	Labels    string // comma-separated labels to match (any match)
}

// CreateTodo inserts a new todo, auto-generating the ID if empty.
func (s *Store) CreateTodo(ctx context.Context, t *Todo) error {
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
	// For project-scoped todos populate ProjectID from ScopeID when caller
	// only set ScopeID; for session/turn-scoped todos surface ScopeID as
	// the session_id placeholder. We don't infer project_id from session
	// here — the service layer does that hop when it has a session in hand.
	if t.Scope == TodoScopeProject && t.ProjectID == "" {
		t.ProjectID = t.ScopeID
	}
	t.CreatedAt = now
	t.UpdatedAt = now

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO todos (id, scope, scope_id, project_id, parent_id, title, description, status, priority, labels, metadata, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Scope, t.ScopeID, nullIfEmpty(t.ProjectID), nullIfEmpty(t.ParentID),
		t.Title, t.Description, t.Status, t.Priority,
		t.Labels, t.Metadata, t.CreatedBy, now, now,
	)
	if err != nil {
		return fmt.Errorf("create todo: %w", err)
	}
	return nil
}

// GetTodo returns a single todo by ID.
func (s *Store) GetTodo(ctx context.Context, id string) (*Todo, error) {
	var t Todo
	var projectID sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, scope, scope_id, project_id, COALESCE(parent_id,''), title, description,
		        status, priority, labels, metadata, created_by, created_at, updated_at
		 FROM todos WHERE id = ?`, id,
	).Scan(
		&t.ID, &t.Scope, &t.ScopeID, &projectID, &t.ParentID, &t.Title, &t.Description,
		&t.Status, &t.Priority, &t.Labels, &t.Metadata, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get todo %s: %w", id, err)
	}
	if projectID.Valid {
		t.ProjectID = projectID.String
	}
	return &t, nil
}

// ListTodos returns todos matching the given filter. When the filter sets
// ProjectID without Scope, both project-scoped todos for the project AND
// session/turn-scoped todos whose originating session belongs to that
// project are returned — this powers the "This Project" surface (D2).
func (s *Store) ListTodos(ctx context.Context, f TodoFilter) ([]Todo, error) {
	query := `SELECT id, scope, scope_id, project_id, COALESCE(parent_id,''), title, description,
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
	if f.ProjectID != "" {
		query += ` AND project_id = ?`
		args = append(args, f.ProjectID)
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

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()

	out := make([]Todo, 0)
	for rows.Next() {
		var t Todo
		var projectID sql.NullString
		if err := rows.Scan(
			&t.ID, &t.Scope, &t.ScopeID, &projectID, &t.ParentID, &t.Title, &t.Description,
			&t.Status, &t.Priority, &t.Labels, &t.Metadata, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		if projectID.Valid {
			t.ProjectID = projectID.String
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTodo updates mutable fields on a todo. Only non-empty fields are changed.
// Scope and project_id may be promoted/demoted via UpdateTodoScope (D2).
func (s *Store) UpdateTodo(ctx context.Context, t *Todo) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
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

// UpdateTodoScope promotes/demotes a todo between session and project scope
// (D2, CW-20260428-0015). Validates that project scope carries a project_id.
func (s *Store) UpdateTodoScope(ctx context.Context, id, scope, scopeID, projectID string) error {
	switch scope {
	case TodoScopeTurn, TodoScopeSession, TodoScopeProject:
	default:
		return fmt.Errorf("update todo scope: invalid scope %q", scope)
	}
	if scope == TodoScopeProject && projectID == "" {
		return fmt.Errorf("update todo scope: project_id is required for scope=project")
	}
	if scope != TodoScopeProject && scopeID == "" {
		return fmt.Errorf("update todo scope: scope_id is required for scope=%q", scope)
	}
	if scope == TodoScopeProject && scopeID == "" {
		// Mirror project_id into scope_id so legacy filter paths still work.
		scopeID = projectID
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE todos SET scope = ?, scope_id = ?, project_id = ?,
		        updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		 WHERE id = ?`,
		scope, scopeID, nullIfEmpty(projectID), id,
	)
	if err != nil {
		return fmt.Errorf("update todo scope: %w", err)
	}
	return nil
}

// DeleteTodo removes a todo by ID. Children are cascade-deleted by the schema.
func (s *Store) DeleteTodo(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM todos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete todo %s: %w", id, err)
	}
	return nil
}

// ListTodoChildren returns direct children of a todo.
func (s *Store) ListTodoChildren(ctx context.Context, parentID string) ([]Todo, error) {
	return s.ListTodos(ctx, TodoFilter{ParentID: parentID})
}
