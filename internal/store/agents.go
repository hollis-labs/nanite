package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

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

// GetAgent returns an agent profile by ID.
func (s *Store) GetAgent(id string) (*AgentProfile, error) {
	var a AgentProfile
	err := s.DB.QueryRow(
		`SELECT id, name, slug, COALESCE(avatar,''), system_prompt, COALESCE(description,''),
		        modes, default_mode, COALESCE(default_model,''),
		        mcp_servers, tool_permissions, can_execute, settings, created_at, updated_at
		 FROM agent_profiles WHERE id = ?`, id,
	).Scan(
		&a.ID, &a.Name, &a.Slug, &a.Avatar, &a.SystemPrompt, &a.Description,
		&a.Modes, &a.DefaultMode, &a.DefaultModel,
		&a.MCPServers, &a.ToolPermissions, &a.CanExecute, &a.Settings, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	return &a, nil
}

// CreateAgent inserts a new agent profile.
func (s *Store) CreateAgent(a *AgentProfile) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if a.Modes == "" {
		a.Modes = "[]"
	}
	if a.DefaultMode == "" {
		a.DefaultMode = "default"
	}
	if a.MCPServers == "" {
		a.MCPServers = "[]"
	}
	if a.ToolPermissions == "" {
		a.ToolPermissions = "{}"
	}
	if a.Settings == "" {
		a.Settings = "{}"
	}

	_, err := s.DB.Exec(
		`INSERT INTO agent_profiles (id, name, slug, avatar, system_prompt, description,
		                              modes, default_mode, default_model,
		                              mcp_servers, tool_permissions, can_execute, settings,
		                              created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Slug, nullIfEmpty(a.Avatar), a.SystemPrompt, nullIfEmpty(a.Description),
		a.Modes, a.DefaultMode, nullIfEmpty(a.DefaultModel),
		a.MCPServers, a.ToolPermissions, a.CanExecute, a.Settings,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	a.CreatedAt = now
	a.UpdatedAt = now
	return nil
}

// UpdateAgent updates mutable fields on an agent profile.
func (s *Store) UpdateAgent(a *AgentProfile) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE agent_profiles SET name = ?, slug = ?, avatar = ?, system_prompt = ?, description = ?,
		        modes = ?, default_mode = ?, default_model = ?,
		        mcp_servers = ?, tool_permissions = ?, can_execute = ?, settings = ?,
		        updated_at = ?
		 WHERE id = ?`,
		a.Name, a.Slug, nullIfEmpty(a.Avatar), a.SystemPrompt, nullIfEmpty(a.Description),
		a.Modes, a.DefaultMode, nullIfEmpty(a.DefaultModel),
		a.MCPServers, a.ToolPermissions, a.CanExecute, a.Settings,
		now, a.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	a.UpdatedAt = now
	return nil
}

// ListAgentModes returns all modes for a given agent.
func (s *Store) ListAgentModes(agentID string) ([]AgentMode, error) {
	rows, err := s.DB.Query(
		`SELECT id, agent_id, slug, name, prompt_addendum, tool_overrides, settings
		 FROM agent_modes WHERE agent_id = ? ORDER BY slug`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent modes: %w", err)
	}
	defer rows.Close()

	var out []AgentMode
	for rows.Next() {
		var m AgentMode
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings); err != nil {
			return nil, fmt.Errorf("scan agent mode: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateAgentMode inserts a new mode for an agent.
func (s *Store) CreateAgentMode(m *AgentMode) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.ToolOverrides == "" {
		m.ToolOverrides = "{}"
	}
	if m.Settings == "" {
		m.Settings = "{}"
	}

	_, err := s.DB.Exec(
		`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum, tool_overrides, settings)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.AgentID, m.Slug, m.Name, m.PromptAddendum, m.ToolOverrides, m.Settings,
	)
	if err != nil {
		return fmt.Errorf("create agent mode: %w", err)
	}
	return nil
}

// SessionAgent represents a record in the session_agents table.
type SessionAgent struct {
	SessionID string
	AgentID   string
	Mode      string
	JoinedAt  string
	IsPrimary bool
}

// GetSessionPrimaryAgent returns the primary agent for a session.
func (s *Store) GetSessionPrimaryAgent(sessionID string) (*SessionAgent, error) {
	var sa SessionAgent
	err := s.DB.QueryRow(
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_agents WHERE session_id = ? AND is_primary = TRUE`, sessionID,
	).Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary)
	if err != nil {
		return nil, fmt.Errorf("get session primary agent for %s: %w", sessionID, err)
	}
	return &sa, nil
}

// SetSessionAgentMode updates the mode for a specific agent in a session.
func (s *Store) SetSessionAgentMode(sessionID, agentID, mode string) error {
	_, err := s.DB.Exec(
		`UPDATE session_agents SET mode = ? WHERE session_id = ? AND agent_id = ?`,
		mode, sessionID, agentID,
	)
	if err != nil {
		return fmt.Errorf("set session agent mode: %w", err)
	}
	return nil
}

// EnsureSessionAgent upserts a session_agents record.
func (s *Store) EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`INSERT INTO session_agents (session_id, agent_id, mode, joined_at, is_primary)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, agent_id) DO UPDATE SET mode = excluded.mode, is_primary = excluded.is_primary`,
		sessionID, agentID, mode, now, isPrimary,
	)
	if err != nil {
		return fmt.Errorf("ensure session agent: %w", err)
	}
	return nil
}

// ListSessionAgents returns all agents in a given session.
func (s *Store) ListSessionAgents(sessionID string) ([]SessionAgent, error) {
	rows, err := s.DB.Query(
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_agents WHERE session_id = ?
		 ORDER BY joined_at`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list session agents: %w", err)
	}
	defer rows.Close()

	var out []SessionAgent
	for rows.Next() {
		var sa SessionAgent
		if err := rows.Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary); err != nil {
			return nil, fmt.Errorf("scan session agent: %w", err)
		}
		out = append(out, sa)
	}
	return out, rows.Err()
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
