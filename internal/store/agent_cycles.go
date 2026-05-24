package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/oklog/ulid/v2"
)

var ErrAgentCycleNotFound = errors.New("agent cycle not found")

const (
	AgentCycleKindRequest  = "request"
	AgentCycleKindTick     = "tick"
	AgentCycleKindWake     = "wake"
	AgentCycleKindReboot   = "reboot"
	AgentCycleKindRecovery = "recovery"

	AgentCycleStatusRunning  = "running"
	AgentCycleStatusComplete = "complete"
	AgentCycleStatusFailed   = "failed"
	AgentCycleStatusRebooted = "rebooted"
	AgentCycleStatusHalted   = "halted"
)

// AgentCycle is the durable unit of live-context continuity for durable
// agents. Full transcripts stay in messages/session_events/tool caches;
// this row stores the compact state future boots should consult first.
type AgentCycle struct {
	ID                   string `json:"id"`
	AgentID              string `json:"agent_id"`
	SessionID            string `json:"session_id"`
	CycleKind            string `json:"cycle_kind"`
	Status               string `json:"status"`
	InputPointerJSON     string `json:"input_pointer_json"`
	OutputSummary        string `json:"output_summary"`
	DecisionsJSON        string `json:"decisions_json"`
	OpenItemsJSON        string `json:"open_items_json"`
	ArtifactPointersJSON string `json:"artifact_pointers_json"`
	ToolCacheRefsJSON    string `json:"tool_cache_refs_json"`
	RebootReason         string `json:"reboot_reason"`
	StartedAt            string `json:"started_at"`
	EndedAt              string `json:"ended_at"`
}

const agentCycleColumns = `id, agent_id, COALESCE(session_id,''), cycle_kind,
       status, input_pointer_json, output_summary, decisions_json,
       open_items_json, artifact_pointers_json, tool_cache_refs_json,
       reboot_reason, started_at, COALESCE(ended_at,'')`

func scanAgentCycle(scanner interface{ Scan(...any) error }, c *AgentCycle) error {
	return scanner.Scan(
		&c.ID, &c.AgentID, &c.SessionID, &c.CycleKind,
		&c.Status, &c.InputPointerJSON, &c.OutputSummary, &c.DecisionsJSON,
		&c.OpenItemsJSON, &c.ArtifactPointersJSON, &c.ToolCacheRefsJSON,
		&c.RebootReason, &c.StartedAt, &c.EndedAt,
	)
}

// InsertAgentCycle inserts a new cycle row. Empty JSON fields are normalized
// to their default empty object/array shapes.
func (s *Store) InsertAgentCycle(ctx context.Context, row AgentCycle) (string, error) {
	if row.AgentID == "" {
		return "", fmt.Errorf("insert agent_cycles: agent_id is required")
	}
	if row.ID == "" {
		row.ID = "cycle-" + ulid.Make().String()
	}
	if row.CycleKind == "" {
		row.CycleKind = AgentCycleKindRequest
	}
	if row.Status == "" {
		row.Status = AgentCycleStatusRunning
	}
	normalizeCycleJSON(&row)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_cycles
		    (id, agent_id, session_id, cycle_kind, status,
		     input_pointer_json, output_summary, decisions_json,
		     open_items_json, artifact_pointers_json, tool_cache_refs_json,
		     reboot_reason, started_at, ended_at)
		 VALUES (?, ?, ?, ?, ?,
		         ?, ?, ?,
		         ?, ?, ?,
		         ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?)`,
		row.ID, row.AgentID, row.SessionID, row.CycleKind, row.Status,
		row.InputPointerJSON, row.OutputSummary, row.DecisionsJSON,
		row.OpenItemsJSON, row.ArtifactPointersJSON, row.ToolCacheRefsJSON,
		row.RebootReason,
		row.StartedAt, nullIfEmpty(row.EndedAt),
	)
	if err != nil {
		return "", fmt.Errorf("insert agent_cycles: %w", err)
	}
	return row.ID, nil
}

// CompleteAgentCycle updates the durable summary fields and stamps ended_at.
func (s *Store) CompleteAgentCycle(ctx context.Context, row AgentCycle) error {
	if row.ID == "" {
		return fmt.Errorf("complete agent_cycles: id is required")
	}
	if row.Status == "" {
		row.Status = AgentCycleStatusComplete
	}
	normalizeCycleJSON(&row)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_cycles
		    SET status = ?,
		        output_summary = ?,
		        decisions_json = ?,
		        open_items_json = ?,
		        artifact_pointers_json = ?,
		        tool_cache_refs_json = ?,
		        reboot_reason = ?,
		        ended_at = COALESCE(NULLIF(?, ''), datetime('now'))
		  WHERE id = ?`,
		row.Status, row.OutputSummary, row.DecisionsJSON, row.OpenItemsJSON,
		row.ArtifactPointersJSON, row.ToolCacheRefsJSON, row.RebootReason,
		row.EndedAt, row.ID,
	)
	if err != nil {
		return fmt.Errorf("complete agent_cycles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete agent_cycles rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentCycleNotFound
	}
	return nil
}

func (s *Store) GetAgentCycle(ctx context.Context, id string) (*AgentCycle, error) {
	var out AgentCycle
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentCycleColumns+` FROM agent_cycles WHERE id = ?`, id,
	)
	if err := scanAgentCycle(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentCycleNotFound
		}
		return nil, fmt.Errorf("get agent_cycles: %w", err)
	}
	return &out, nil
}

func (s *Store) ListAgentCycles(ctx context.Context, agentID string, limit int) ([]AgentCycle, error) {
	if agentID == "" {
		return nil, fmt.Errorf("list agent_cycles: agent_id is required")
	}
	query := `SELECT ` + agentCycleColumns + `
		 FROM agent_cycles
		 WHERE agent_id = ?
		 ORDER BY started_at DESC, id DESC`
	args := []any{agentID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent_cycles: %w", err)
	}
	defer rows.Close()
	out := make([]AgentCycle, 0)
	for rows.Next() {
		var c AgentCycle
		if err := scanAgentCycle(rows, &c); err != nil {
			return nil, fmt.Errorf("scan agent_cycles: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func normalizeCycleJSON(row *AgentCycle) {
	if row.InputPointerJSON == "" {
		row.InputPointerJSON = "{}"
	}
	if row.DecisionsJSON == "" {
		row.DecisionsJSON = "[]"
	}
	if row.OpenItemsJSON == "" {
		row.OpenItemsJSON = "[]"
	}
	if row.ArtifactPointersJSON == "" {
		row.ArtifactPointersJSON = "[]"
	}
	if row.ToolCacheRefsJSON == "" {
		row.ToolCacheRefsJSON = "[]"
	}
}
