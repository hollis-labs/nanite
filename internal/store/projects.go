package store

import (
	"fmt"
	"time"
)

// Project represents a project. Projects were formerly nested under the
// in-app `workspaces` table (retired — Phase 0 item 20,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md); the table
// is flat now. `projects` is also the FK target of `agent_projects` (see
// internal/store/agent_projects.go) — the live Agent Construction scope
// mechanism (docs/engineering/architecture/01-agent-construction.md) — so
// the table itself and its CRUD stay, only the workspace nesting is gone.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
	Settings    string `json:"settings"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ListProjects returns all projects, ordered by sort_order then name. No
// longer workspace-scoped — there has only ever been one workspace in
// practice (see migration 088_consolidate_personal_workspace.sql).
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, COALESCE(description,''), COALESCE(repo_path,''), settings, sort_order, created_at, updated_at FROM projects ORDER BY sort_order, name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	out := make([]Project, 0)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.Settings, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns a single project by ID.
func (s *Store) GetProject(id string) (*Project, error) {
	var p Project
	err := s.DB.QueryRow(
		`SELECT id, name, COALESCE(description,''), COALESCE(repo_path,''), settings, sort_order, created_at, updated_at FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.Settings, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get project %s: %w", id, err)
	}
	return &p, nil
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(p *Project) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if p.Settings == "" {
		p.Settings = "{}"
	}
	_, err := s.DB.Exec(
		`INSERT INTO projects (id, name, description, repo_path, settings, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.RepoPath, p.Settings, p.SortOrder, now, now,
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
