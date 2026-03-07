package store

import "fmt"

// AgentProfile represents an agent profile record.
type AgentProfile struct {
	ID              string
	Name            string
	Slug            string
	Avatar          string
	SystemPrompt    string
	Description     string
	Modes           string
	DefaultMode     string
	DefaultModel    string
	MCPServers      string
	ToolPermissions string
	CanExecute      bool
	Settings        string
	CreatedAt       string
	UpdatedAt       string
}

// AgentMode represents a mode configuration for an agent.
type AgentMode struct {
	ID             string
	AgentID        string
	Slug           string
	Name           string
	PromptAddendum string
	ToolOverrides  string
	Settings       string
}

// GetAgentBySlug returns an agent profile by its slug.
func (s *Store) GetAgentBySlug(slug string) (*AgentProfile, error) {
	var a AgentProfile
	err := s.DB.QueryRow(
		`SELECT id, name, slug, COALESCE(avatar,''), system_prompt, COALESCE(description,''),
		        modes, default_mode, COALESCE(default_model,''),
		        mcp_servers, tool_permissions, can_execute, settings, created_at, updated_at
		 FROM agent_profiles WHERE slug = ?`, slug,
	).Scan(
		&a.ID, &a.Name, &a.Slug, &a.Avatar, &a.SystemPrompt, &a.Description,
		&a.Modes, &a.DefaultMode, &a.DefaultModel,
		&a.MCPServers, &a.ToolPermissions, &a.CanExecute, &a.Settings, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get agent by slug %s: %w", slug, err)
	}
	return &a, nil
}

// GetAgentMode returns a specific mode for an agent.
func (s *Store) GetAgentMode(agentID, modeSlug string) (*AgentMode, error) {
	var m AgentMode
	err := s.DB.QueryRow(
		`SELECT id, agent_id, slug, name, prompt_addendum, tool_overrides, settings
		 FROM agent_modes WHERE agent_id = ? AND slug = ?`, agentID, modeSlug,
	).Scan(&m.ID, &m.AgentID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings)
	if err != nil {
		return nil, fmt.Errorf("get agent mode %s/%s: %w", agentID, modeSlug, err)
	}
	return &m, nil
}

// ListAgents returns all agent profiles.
func (s *Store) ListAgents() ([]AgentProfile, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, slug, COALESCE(avatar,''), system_prompt, COALESCE(description,''),
		        modes, default_mode, COALESCE(default_model,''),
		        mcp_servers, tool_permissions, can_execute, settings, created_at, updated_at
		 FROM agent_profiles ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var out []AgentProfile
	for rows.Next() {
		var a AgentProfile
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Slug, &a.Avatar, &a.SystemPrompt, &a.Description,
			&a.Modes, &a.DefaultMode, &a.DefaultModel,
			&a.MCPServers, &a.ToolPermissions, &a.CanExecute, &a.Settings, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
