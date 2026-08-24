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
//
// TASKS/skills/02: ApprovedContentHash/GrantedAt/GrantedBy/CapabilitiesGranted
// are additive grant-state columns (migration 137) folding this table into
// docs/engineering/architecture/20-skills.md's per-agent skill
// attachment/grant table — see that doc's "Security, sandboxing, and trust"
// section for ApprovedContentHash's role (approval is granted against a
// specific vendored-store content hash; a changed source requires a new
// explicit install, which carries a new hash requiring its own approval).
// All four are nullable/defaulted and read back empty/zero on any
// pre-existing row created via the original six-column shape — the live
// Agent Builder Wizard / Agent Capabilities Panel frontend keeps reading and
// writing the original columns unmodified.
type AgentKnownSkill struct {
	AgentID         string `json:"agent_id"`
	SkillName       string `json:"skill_name"`
	Pinned          bool   `json:"pinned"`
	ActivationCount int64  `json:"activation_count"`
	LastUsedAt      string `json:"last_used_at"` // empty when never used
	AddedAt         string `json:"added_at"`
	TTLSeconds      int64  `json:"ttl_seconds"` // 0 when unset (column allows NULL)
	Reason          string `json:"reason"`

	// ApprovedContentHash is the internal/skillvendor content-address the
	// grant was approved against. Empty when the skill has never been
	// through an explicit approval step (e.g. a bare Wizard/Panel
	// assignment with no grant workflow attached yet).
	ApprovedContentHash string `json:"approved_content_hash"`
	GrantedAt           string `json:"granted_at"`
	GrantedBy           string `json:"granted_by"`
	// CapabilitiesGranted is a JSON blob describing what the granted
	// skill's script/materializer is authorized for. Shape is owned by
	// task 09 — kept loose/untyped here.
	CapabilitiesGranted string `json:"capabilities_granted"`
}

// IsBareAssignment reports whether this row carries no known-skill-specific
// data beyond bare (agent_id, skill_name) existence — i.e. it was created
// only by skills.go's AssignSkillToAgent (the "Assigned Skills" step
// underlying POST/DELETE /api/agents/{id}/skills) and has never been touched
// by the known-skills REST handlers (internal/api/agent_capabilities.go's
// POST/PUT/DELETE /api/agents/{id}/known-skills) or a grant workflow.
//
// TASKS/skills/02's fix-required section (2026-08-21 review): AssignSkillToAgent
// and the known-skills handlers now read/write the identical
// (agent_id, skill_name) row space, so both handleCreateAgentKnownSkill's
// pre-existence check and RemoveSkillFromAgent's delete-vs-preserve decision
// key off this exact test to keep the two REST surfaces safely independent.
// AddedAt is deliberately excluded — InsertAgentKnownSkill always defaults it
// to the current timestamp on first insert, so it is never empty regardless
// of which path created the row and is not evidence of a real grant.
func (t AgentKnownSkill) IsBareAssignment() bool {
	return !t.Pinned &&
		t.ActivationCount == 0 &&
		t.LastUsedAt == "" &&
		t.TTLSeconds == 0 &&
		t.Reason == "" &&
		t.ApprovedContentHash == "" &&
		t.GrantedAt == "" &&
		t.GrantedBy == "" &&
		t.CapabilitiesGranted == ""
}

const agentKnownSkillColumns = `agent_id, skill_name, pinned, activation_count,
       COALESCE(last_used_at,''), added_at, COALESCE(ttl_seconds,0), reason,
       COALESCE(approved_content_hash,''), COALESCE(granted_at,''),
       COALESCE(granted_by,''), COALESCE(capabilities_granted,'')`

func scanAgentKnownSkill(scanner interface{ Scan(...any) error }, t *AgentKnownSkill) error {
	return scanner.Scan(
		&t.AgentID, &t.SkillName, &t.Pinned, &t.ActivationCount,
		&t.LastUsedAt, &t.AddedAt, &t.TTLSeconds, &t.Reason,
		&t.ApprovedContentHash, &t.GrantedAt, &t.GrantedBy, &t.CapabilitiesGranted,
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
		     added_at, ttl_seconds, reason,
		     approved_content_hash, granted_at, granted_by, capabilities_granted)
		 VALUES (?, ?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?, ?, ?, ?, ?)`,
		row.AgentID, row.SkillName, row.Pinned, row.ActivationCount,
		nullIfEmpty(row.LastUsedAt),
		row.AddedAt,
		ttl, row.Reason,
		nullIfEmpty(row.ApprovedContentHash), nullIfEmpty(row.GrantedAt),
		nullIfEmpty(row.GrantedBy), nullIfEmpty(row.CapabilitiesGranted),
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
	defer closeRows(rows)

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
