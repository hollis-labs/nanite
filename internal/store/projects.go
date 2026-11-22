package store

import (
	"context"
	"fmt"
	"time"
)

// Project represents a project. Projects were formerly nested under the
// in-app `workspaces` table (retired — Phase 0 item 20,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md); the table
// is flat now. `projects` is also the FK target of `actor_projects` (see
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
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, COALESCE(description,''), COALESCE(repo_path,''), settings, sort_order, created_at, updated_at FROM projects ORDER BY sort_order, name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer closeRows(rows)

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
func (s *Store) GetProject(ctx context.Context, id string) (*Project, error) {
	var p Project
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, COALESCE(description,''), COALESCE(repo_path,''), settings, sort_order, created_at, updated_at FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath, &p.Settings, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get project %s: %w", id, err)
	}
	return &p, nil
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(ctx context.Context, p *Project) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if p.Settings == "" {
		p.Settings = "{}"
	}
	_, err := s.DB.ExecContext(ctx,
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
func (s *Store) UpdateProject(ctx context.Context, p *Project) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE projects SET name = ?, description = ?, repo_path = ?, settings = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Description, p.RepoPath, p.Settings, p.SortOrder, now, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	p.UpdatedAt = now
	return nil
}

// ProjectSessionRef names a session that still belongs to a project.
type ProjectSessionRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ProjectInUseError is DeleteProject's refusal: sessions that are not
// archived still belong to the project. Nothing was changed.
type ProjectInUseError struct {
	ProjectID string
	Sessions  []ProjectSessionRef
}

func (e *ProjectInUseError) Error() string {
	return fmt.Sprintf("project %s still has %d session(s) that are not archived; archive them or move them to another project first",
		e.ProjectID, len(e.Sessions))
}

// DeleteProject deletes a project by ID, in one transaction
// (CW-20261001-0125):
//   - archived sessions are detached (project_id NULL); their rows and
//     messages are kept. Session and message rows are never deleted here.
//   - a session that is not archived refuses the delete with
//     *ProjectInUseError, and nothing changes;
//   - the project's agent_projects links, which name it and nothing else,
//     go with it.
//
// sessions.project_id and agent_projects.project_id are NO ACTION foreign
// keys, so before this a project that ever had a session could not be
// deleted at all. todos, reminders and pinned_content carry project_id with
// no foreign key and are left as they are.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete project %s: begin tx: %w", id, err)
	}
	defer rollbackUnlessCommitted(tx)

	// Detach first: the write takes SQLite's write lock, so the check below
	// sees sessions no other writer can attach or unarchive before commit.
	if _, err = tx.ExecContext(ctx,
		`UPDATE sessions SET project_id = NULL WHERE project_id = ? AND status = 'archived'`, id); err != nil {
		return fmt.Errorf("delete project %s: detach archived sessions: %w", id, err)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, COALESCE(NULLIF(custom_name, ''), COALESCE(title, '')), COALESCE(status, '')
		 FROM sessions WHERE project_id = ? ORDER BY last_activity DESC, id`, id)
	if err != nil {
		return fmt.Errorf("delete project %s: list sessions: %w", id, err)
	}
	var live []ProjectSessionRef
	for rows.Next() {
		var ref ProjectSessionRef
		if scanErr := rows.Scan(&ref.ID, &ref.Title, &ref.Status); scanErr != nil {
			closeRows(rows)
			return fmt.Errorf("delete project %s: scan session: %w", id, scanErr)
		}
		live = append(live, ref)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		closeRows(rows)
		return fmt.Errorf("delete project %s: list sessions: %w", id, rowsErr)
	}
	closeRows(rows)
	if len(live) > 0 {
		return &ProjectInUseError{ProjectID: id, Sessions: live}
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM actor_projects WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("delete project %s: remove agent links: %w", id, err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete project %s: %w", id, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("delete project %s: commit: %w", id, err)
	}
	return nil
}
