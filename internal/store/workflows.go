package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Workflow represents a workflow record in the workflows table.
type Workflow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Trigger     string `json:"trigger"`
	Definition  string `json:"definition"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	IsEnabled   bool   `json:"is_enabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ListWorkflows returns all workflows ordered by name.
func (s *Store) ListWorkflows() ([]Workflow, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, slug, trigger, definition, COALESCE(workspace_id,''), is_enabled, created_at, updated_at
		 FROM workflows ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	out := make([]Workflow, 0)
	for rows.Next() {
		var w Workflow
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Trigger, &w.Definition,
			&w.WorkspaceID, &w.IsEnabled, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workflow: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWorkflow returns a workflow by ID.
func (s *Store) GetWorkflow(id string) (*Workflow, error) {
	var w Workflow
	err := s.DB.QueryRow(
		`SELECT id, name, slug, trigger, definition, COALESCE(workspace_id,''), is_enabled, created_at, updated_at
		 FROM workflows WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Slug, &w.Trigger, &w.Definition,
		&w.WorkspaceID, &w.IsEnabled, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow %s: %w", id, err)
	}
	return &w, nil
}

// CreateWorkflow inserts a new workflow.
func (s *Store) CreateWorkflow(w *Workflow) error {
	if w.ID == "" {
		w.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if w.Trigger == "" {
		w.Trigger = "manual"
	}
	if w.Definition == "" {
		w.Definition = "{}"
	}

	_, err := s.DB.Exec(
		`INSERT INTO workflows (id, name, slug, trigger, definition, workspace_id, is_enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Slug, w.Trigger, w.Definition, nullIfEmpty(w.WorkspaceID), w.IsEnabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("create workflow: %w", err)
	}
	w.CreatedAt = now
	w.UpdatedAt = now
	return nil
}
