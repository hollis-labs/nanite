package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Mode represents a first-class, reusable mode that can be assigned to agents.
type Mode struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	PromptAddendum string `json:"prompt_addendum"`
	ToolOverrides  string `json:"tool_overrides"`
	Settings       string `json:"settings"`
	IsBuiltin      bool   `json:"is_builtin"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// AgentModeAssignment represents a many-to-many link between agents and modes.
type AgentModeAssignment struct {
	AgentID string `json:"agent_id"`
	ModeID  string `json:"mode_id"`
}

// CreateMode inserts a new mode.
func (s *Store) CreateMode(m *Mode) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if m.ToolOverrides == "" {
		m.ToolOverrides = "{}"
	}
	if m.Settings == "" {
		m.Settings = "{}"
	}

	_, err := s.DB.Exec(
		`INSERT INTO modes (id, slug, name, prompt_addendum, tool_overrides, settings, is_builtin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Slug, m.Name, m.PromptAddendum, m.ToolOverrides, m.Settings, m.IsBuiltin,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("create mode: %w", err)
	}
	m.CreatedAt = now
	m.UpdatedAt = now
	return nil
}

// GetMode returns a mode by ID.
func (s *Store) GetMode(id string) (*Mode, error) {
	var m Mode
	err := s.DB.QueryRow(
		`SELECT id, slug, name, prompt_addendum, tool_overrides, settings, is_builtin, created_at, updated_at
		 FROM modes WHERE id = ?`, id,
	).Scan(&m.ID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings, &m.IsBuiltin,
		&m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mode %s: %w", id, err)
	}
	return &m, nil
}

// GetModeBySlug returns a mode by slug.
func (s *Store) GetModeBySlug(slug string) (*Mode, error) {
	var m Mode
	err := s.DB.QueryRow(
		`SELECT id, slug, name, prompt_addendum, tool_overrides, settings, is_builtin, created_at, updated_at
		 FROM modes WHERE slug = ?`, slug,
	).Scan(&m.ID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings, &m.IsBuiltin,
		&m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mode by slug %s: %w", slug, err)
	}
	return &m, nil
}

// ListModes returns all modes ordered by name.
func (s *Store) ListModes() ([]Mode, error) {
	rows, err := s.DB.Query(
		`SELECT id, slug, name, prompt_addendum, tool_overrides, settings, is_builtin, created_at, updated_at
		 FROM modes ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list modes: %w", err)
	}
	defer rows.Close()

	out := make([]Mode, 0)
	for rows.Next() {
		var m Mode
		if err := rows.Scan(&m.ID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings, &m.IsBuiltin,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mode: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMode updates a mode's mutable fields.
func (s *Store) UpdateMode(m *Mode) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE modes SET slug = ?, name = ?, prompt_addendum = ?, tool_overrides = ?, settings = ?, updated_at = ?
		 WHERE id = ?`,
		m.Slug, m.Name, m.PromptAddendum, m.ToolOverrides, m.Settings,
		now, m.ID,
	)
	if err != nil {
		return fmt.Errorf("update mode: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mode %q not found", m.ID)
	}
	m.UpdatedAt = now
	return nil
}

// DeleteMode removes a mode by ID (only non-builtin).
func (s *Store) DeleteMode(id string) error {
	res, err := s.DB.Exec(
		`DELETE FROM modes WHERE id = ? AND is_builtin = 0`, id,
	)
	if err != nil {
		return fmt.Errorf("delete mode %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mode %q not found or is built-in", id)
	}
	return nil
}

// AssignModeToAgent links a mode to an agent.
func (s *Store) AssignModeToAgent(agentID, modeID string) error {
	_, err := s.DB.Exec(
		`INSERT OR IGNORE INTO agent_mode_assignments (agent_id, mode_id)
		 VALUES (?, ?)`,
		agentID, modeID,
	)
	if err != nil {
		return fmt.Errorf("assign mode to agent: %w", err)
	}
	return nil
}

// UnassignModeFromAgent removes a mode assignment from an agent.
func (s *Store) UnassignModeFromAgent(agentID, modeID string) error {
	res, err := s.DB.Exec(
		`DELETE FROM agent_mode_assignments WHERE agent_id = ? AND mode_id = ?`,
		agentID, modeID,
	)
	if err != nil {
		return fmt.Errorf("unassign mode from agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mode assignment not found")
	}
	return nil
}

// GetAgentAssignedModes returns all modes assigned to an agent via the junction table.
func (s *Store) GetAgentAssignedModes(agentID string) ([]Mode, error) {
	rows, err := s.DB.Query(
		`SELECT m.id, m.slug, m.name, m.prompt_addendum, m.tool_overrides, m.settings, m.is_builtin, m.created_at, m.updated_at
		 FROM modes m
		 JOIN agent_mode_assignments ama ON m.id = ama.mode_id
		 WHERE ama.agent_id = ?
		 ORDER BY m.name`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("get agent assigned modes: %w", err)
	}
	defer rows.Close()

	out := make([]Mode, 0)
	for rows.Next() {
		var m Mode
		if err := rows.Scan(&m.ID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings, &m.IsBuiltin,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent assigned mode: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// BuiltinModes defines the default modes seeded on first run.
var BuiltinModes = []Mode{
	{
		Slug:           "default",
		Name:           "Default",
		PromptAddendum: "You are in your default conversational mode. Be helpful, thoughtful, and proactive about managing context and suggesting next steps.",
	},
	{
		Slug:           "architect",
		Name:           "Architect",
		PromptAddendum: "You are in architect mode. Focus on system design, technical trade-offs, and architectural decisions. Ask probing questions about requirements, constraints, and failure modes. Suggest patterns and evaluate alternatives.",
	},
	{
		Slug:           "planner",
		Name:           "Planner",
		PromptAddendum: "You are in planner mode. Focus on breaking work into actionable tasks, estimating scope, identifying dependencies, and creating structured plans. Use numbered lists and clear acceptance criteria.",
	},
	{
		Slug:           "writer",
		Name:           "Writer",
		PromptAddendum: "You are in writer mode. Focus on prose quality, narrative structure, clarity, and voice. Help with drafting, editing, and refining written content. Be direct about what works and what doesn't.",
	},
}

// SeedBuiltinModes inserts built-in modes if they don't exist.
func (s *Store) SeedBuiltinModes() error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range BuiltinModes {
		_, err := s.DB.Exec(
			`INSERT OR IGNORE INTO modes (id, slug, name, prompt_addendum, tool_overrides, settings, is_builtin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, '{}', '{}', 1, ?, ?)`,
			uuid.New().String(), m.Slug, m.Name, m.PromptAddendum, now, now,
		)
		if err != nil {
			return fmt.Errorf("seed mode %s: %w", m.Slug, err)
		}
	}
	slog.Info("seed: ensured built-in modes exist")
	return nil
}
