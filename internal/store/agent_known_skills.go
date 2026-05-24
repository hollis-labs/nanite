package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrAgentKnownSkillNotFound is returned when an agent_known_skills row
// cannot be located.
var ErrAgentKnownSkillNotFound = errors.New("agent known skill not found")

// AgentKnownSkill is one row in the agent_known_skills table. It mirrors
// AgentKnownTool but tracks the per-agent skill roster instead of tools.
type AgentKnownSkill struct {
	AgentID         string `json:"agent_id"`
	SkillName       string `json:"skill_name"`
	Pinned          bool   `json:"pinned"`
	ActivationCount int64  `json:"activation_count"`
	LastUsedAt      string `json:"last_used_at"` // empty when never used
	AddedAt         string `json:"added_at"`
	TTLSeconds      int64  `json:"ttl_seconds"` // 0 when unset (column allows NULL)
	Reason          string `json:"reason"`
}

const agentKnownSkillColumns = `agent_id, skill_name, pinned, activation_count,
       COALESCE(last_used_at,''), added_at, COALESCE(ttl_seconds,0), reason`

func scanAgentKnownSkill(scanner interface{ Scan(...any) error }, t *AgentKnownSkill) error {
	return scanner.Scan(
		&t.AgentID, &t.SkillName, &t.Pinned, &t.ActivationCount,
		&t.LastUsedAt, &t.AddedAt, &t.TTLSeconds, &t.Reason,
	)
}

// InsertAgentKnownSkill upserts an agent_known_skills row. PK is
// (agent_id, skill_name).
func (s *Store) InsertAgentKnownSkill(ctx context.Context, row AgentKnownSkill) error {
	if row.AgentID == "" {
		return fmt.Errorf("insert agent_known_skills: agent_id is required")
	}
	if row.SkillName == "" {
		return fmt.Errorf("insert agent_known_skills: skill_name is required")
	}
	var ttl any
	if row.TTLSeconds > 0 {
		ttl = row.TTLSeconds
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_known_skills
		    (agent_id, skill_name, pinned, activation_count, last_used_at,
		     added_at, ttl_seconds, reason)
		 VALUES (?, ?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?)`,
		row.AgentID, row.SkillName, row.Pinned, row.ActivationCount,
		nullIfEmpty(row.LastUsedAt),
		row.AddedAt,
		ttl, row.Reason,
	)
	if err != nil {
		return fmt.Errorf("insert agent_known_skills: %w", err)
	}
	return nil
}

// ListAgentKnownSkills returns every known-skill row for an agent, ordered
// by pinned DESC, last_used_at DESC, skill_name ASC.
func (s *Store) ListAgentKnownSkills(ctx context.Context, agentID string) ([]AgentKnownSkill, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentKnownSkillColumns+`
		 FROM agent_known_skills
		 WHERE agent_id = ?
		 ORDER BY pinned DESC, last_used_at DESC, skill_name ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_known_skills: %w", err)
	}
	defer rows.Close()

	out := make([]AgentKnownSkill, 0)
	for rows.Next() {
		var t AgentKnownSkill
		if err := scanAgentKnownSkill(rows, &t); err != nil {
			return nil, fmt.Errorf("scan agent_known_skills: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAgentKnownSkill returns a single known-skill row by (agent_id, skill_name).
// Returns ErrAgentKnownSkillNotFound when the row does not exist.
func (s *Store) GetAgentKnownSkill(ctx context.Context, agentID, skillName string) (*AgentKnownSkill, error) {
	var t AgentKnownSkill
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentKnownSkillColumns+`
		 FROM agent_known_skills
		 WHERE agent_id = ? AND skill_name = ?`,
		agentID, skillName,
	)
	if err := scanAgentKnownSkill(row, &t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentKnownSkillNotFound
		}
		return nil, fmt.Errorf("get agent_known_skills: %w", err)
	}
	return &t, nil
}

// DeleteAgentKnownSkill removes a single known-skill row. Returns
// ErrAgentKnownSkillNotFound if no row matched.
func (s *Store) DeleteAgentKnownSkill(ctx context.Context, agentID, skillName string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_known_skills WHERE agent_id = ? AND skill_name = ?`,
		agentID, skillName,
	)
	if err != nil {
		return fmt.Errorf("delete agent_known_skills: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_known_skills rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentKnownSkillNotFound
	}
	return nil
}
