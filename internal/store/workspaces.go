package store

import (
	"fmt"
	"time"
)

// Workspace represents a workspace record.
type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	SortOrder   int    `json:"sort_order"`
	Settings    string `json:"settings"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Project represents a project within a workspace.
type Project struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
	Settings    string `json:"settings"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ListWorkspaces returns all workspaces ordered by sort_order.
func (s *Store) ListWorkspaces() ([]Workspace, error) {
	rows, err := s.DB.Query(`SELECT id, name, COALESCE(description,''), COALESCE(icon,''), sort_order, settings, created_at, updated_at FROM workspaces ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()

	out := make([]Workspace, 0)
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Icon, &w.SortOrder, &w.Settings, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWorkspace returns a single workspace by ID.
func (s *Store) GetWorkspace(id string) (*Workspace, error) {
	var w Workspace
	err := s.DB.QueryRow(
		`SELECT id, name, COALESCE(description,''), COALESCE(icon,''), sort_order, settings, created_at, updated_at FROM workspaces WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Description, &w.Icon, &w.SortOrder, &w.Settings, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get workspace %s: %w", id, err)
	}
	return &w, nil
}

// CreateWorkspace inserts a new workspace.
func (s *Store) CreateWorkspace(w *Workspace) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if w.Settings == "" {
		w.Settings = "{}"
	}
	_, err := s.DB.Exec(
		`INSERT INTO workspaces (id, name, description, icon, sort_order, settings, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Description, w.Icon, w.SortOrder, w.Settings, now, now,
	)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	w.CreatedAt = now
	w.UpdatedAt = now
	return nil
}

// UpdateWorkspace updates an existing workspace.
func (s *Store) UpdateWorkspace(w *Workspace) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE workspaces SET name = ?, description = ?, icon = ?, sort_order = ?, settings = ?, updated_at = ? WHERE id = ?`,
		w.Name, w.Description, w.Icon, w.SortOrder, w.Settings, now, w.ID,
	)
	if err != nil {
		return fmt.Errorf("update workspace: %w", err)
	}
	w.UpdatedAt = now
	return nil
}

// DeleteWorkspace deletes a workspace by ID.
func (s *Store) DeleteWorkspace(id string) error {
	_, err := s.DB.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete workspace %s: %w", id, err)
	}
	return nil
}

// ListProjects returns all projects for a given workspace.
func (s *Store) ListProjects(workspaceID string) ([]Project, error) {
	rows, err := s.DB.Query(
		`SELECT id, workspace_id, name, COALESCE(description,''), COALESCE(repo_path,''), settings, sort_order, created_at, updated_at FROM projects WHERE workspace_id = ? ORDER BY sort_order, name`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	out := make([]Project, 0)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Description, &p.RepoPath, &p.Settings, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(p *Project) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if p.Settings == "" {
		p.Settings = "{}"
	}
	_, err := s.DB.Exec(
		`INSERT INTO projects (id, workspace_id, name, description, repo_path, settings, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.WorkspaceID, p.Name, p.Description, p.RepoPath, p.Settings, p.SortOrder, now, now,
	)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

// UpdateProject updates an existing project.
func (s *Store) UpdateProject(p *Project) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE projects SET name = ?, description = ?, repo_path = ?, settings = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Description, p.RepoPath, p.Settings, p.SortOrder, now, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	p.UpdatedAt = now
	return nil
}

// DeleteProject deletes a project by ID.
func (s *Store) DeleteProject(id string) error {
	_, err := s.DB.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project %s: %w", id, err)
	}
	return nil
}
