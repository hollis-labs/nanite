package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrAgentProjectNotFound denotes an absent agent-project membership.
var ErrAgentProjectNotFound = errors.New("agent-project link not found")

// AgentProject represents an agent-to-project assignment.
type AgentProject struct {
	AgentID   string `json:"agent_id"`
	ProjectID string `json:"project_id"`
	CreatedAt string `json:"created_at"`
}

// ListAgentProjects returns all projects linked to an agent.
func (s *Store) ListAgentProjects(ctx context.Context, agentID string) ([]Project, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT p.id, p.name, COALESCE(p.description,''), COALESCE(p.repo_path,''),
		        p.settings, p.sort_order, p.created_at, p.updated_at
		 FROM projects p
		 JOIN agent_projects ap ON p.id = ap.project_id
		 WHERE ap.agent_id = ?
		 ORDER BY p.name`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent projects: %w", err)
	}
	defer closeRows(rows)

	out := make([]Project, 0)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepoPath,
			&p.Settings, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListProjectAgents returns all agents linked to a project.
func (s *Store) ListProjectAgents(ctx context.Context, projectID string) ([]AgentProfile, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentColumns+`
		 FROM agent_profiles
		 JOIN agent_projects ap ON agent_profiles.id = ap.agent_id
		 WHERE ap.project_id = ?
		 ORDER BY agent_profiles.name`, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project agents: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentProfile, 0)
	for rows.Next() {
		var a AgentProfile
		if err := scanAgent(rows, &a); err != nil {
			return nil, fmt.Errorf("scan project agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAgentProject links an agent to a project.
func (s *Store) AddAgentProject(ctx context.Context, agentID, projectID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO agent_projects (agent_id, project_id, created_at)
		 VALUES (?, ?, ?)`,
		agentID, projectID, now,
	)
	if err != nil {
		return fmt.Errorf("add agent project: %w", err)
	}
	return nil
}

// RemoveAgentProject unlinks an agent from a project.
func (s *Store) RemoveAgentProject(ctx context.Context, agentID, projectID string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_projects WHERE agent_id = ? AND project_id = ?`,
		agentID, projectID,
	)
	if err != nil {
		return fmt.Errorf("remove agent project: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove agent project rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentProjectNotFound
	}
	return nil
}
