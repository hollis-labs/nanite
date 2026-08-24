package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrAgentKnownToolNotFound is returned when an agent_known_tools row
// cannot be located.
var ErrAgentKnownToolNotFound = errors.New("agent known tool not found")

// AgentKnownTool is one row in the agent_known_tools table. It represents
// a single tool that is currently part of an agent's "known set", together
// with activation telemetry the FU-7 classifier uses to rank candidates.
type AgentKnownTool struct {
	AgentID         string `json:"agent_id"`
	ToolName        string `json:"tool_name"`
	Pinned          bool   `json:"pinned"`
	SortOrder       int64  `json:"sort_order"`
	ActivationCount int64  `json:"activation_count"`
	LastUsedAt      string `json:"last_used_at"` // empty when never used
	AddedAt         string `json:"added_at"`
	TTLSeconds      int64  `json:"ttl_seconds"` // 0 when unset (column allows NULL)
	Reason          string `json:"reason"`
}

const agentKnownToolColumns = `agent_id, tool_name, pinned, COALESCE(sort_order,0), activation_count,
       COALESCE(last_used_at,''), added_at, COALESCE(ttl_seconds,0), reason`

func scanAgentKnownTool(scanner interface{ Scan(...any) error }, t *AgentKnownTool) error {
	return scanner.Scan(
		&t.AgentID, &t.ToolName, &t.Pinned, &t.SortOrder, &t.ActivationCount,
		&t.LastUsedAt, &t.AddedAt, &t.TTLSeconds, &t.Reason,
	)
}

// InsertAgentKnownTool upserts an agent_known_tools row. The PK is
// (agent_id, tool_name) so re-inserting the same pair replaces the row.
// Empty AddedAt is filled by the column default via DEFAULT VALUES coalescing.
func (s *Store) InsertAgentKnownTool(ctx context.Context, row AgentKnownTool) error {
	if row.AgentID == "" {
		return fmt.Errorf("insert agent_known_tools: agent_id is required")
	}
	if row.ToolName == "" {
		return fmt.Errorf("insert agent_known_tools: tool_name is required")
	}
	// last_used_at and ttl_seconds are nullable in the schema. Empty / 0
	// inputs map to SQL NULL so callers can omit them naturally. added_at
	// is filled by SQLite's column default when the caller leaves it empty.
	var ttl any
	if row.TTLSeconds > 0 {
		ttl = row.TTLSeconds
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_known_tools
		    (agent_id, tool_name, pinned, sort_order, activation_count, last_used_at,
		     added_at, ttl_seconds, reason)
		 VALUES (?, ?, ?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?)`,
		row.AgentID, row.ToolName, row.Pinned, row.SortOrder, row.ActivationCount,
		nullIfEmpty(row.LastUsedAt),
		row.AddedAt,
		ttl, row.Reason,
	)
	if err != nil {
		return fmt.Errorf("insert agent_known_tools: %w", err)
	}
	return nil
}

// ListAgentKnownTools returns every known-tool row for an agent. Callers that
// render agent-facing order should apply sortKnownToolRows so the store method
// remains a simple accessor.
func (s *Store) ListAgentKnownTools(ctx context.Context, agentID string) ([]AgentKnownTool, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentKnownToolColumns+`
		 FROM agent_known_tools
		 WHERE agent_id = ?
		 ORDER BY pinned DESC, tool_name ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_known_tools: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentKnownTool, 0)
	for rows.Next() {
		var t AgentKnownTool
		if err := scanAgentKnownTool(rows, &t); err != nil {
			return nil, fmt.Errorf("scan agent_known_tools: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAgentKnownTool returns a single known-tool row by (agent_id, tool_name).
// Returns ErrAgentKnownToolNotFound when the row does not exist.
func (s *Store) GetAgentKnownTool(ctx context.Context, agentID, toolName string) (*AgentKnownTool, error) {
	var t AgentKnownTool
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentKnownToolColumns+`
		 FROM agent_known_tools
		 WHERE agent_id = ? AND tool_name = ?`,
		agentID, toolName,
	)
	if err := scanAgentKnownTool(row, &t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentKnownToolNotFound
		}
		return nil, fmt.Errorf("get agent_known_tools: %w", err)
	}
	return &t, nil
}

// DeleteAgentKnownTool removes a single known-tool row. Returns
// ErrAgentKnownToolNotFound if no row matched.
func (s *Store) DeleteAgentKnownTool(ctx context.Context, agentID, toolName string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_known_tools WHERE agent_id = ? AND tool_name = ?`,
		agentID, toolName,
	)
	if err != nil {
		return fmt.Errorf("delete agent_known_tools: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_known_tools rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentKnownToolNotFound
	}
	return nil
}

// BumpActivation was originally introduced by FU-7b to increment
// activation_count and refresh last_used_at on each tool use, so the per-
// turn sort could prioritize recently-used tools.
//
// FU-14 (2026-05-20): made a NO-OP.
//
// The activation_count + last_used_at churn caused agent_known_tools to
// re-sort every turn, which changed the byte order of the tool block in
// the assembled prefix, which invalidated Anthropic's prompt cache. The
// observed symptom was strict every-other-turn `cache_read=0` + degenerate
// echo output (Passes 15-24 of the live Torque Supervisor shadow run).
//
// The sort is now stable: hardcoded activation values seeded from
// roleSeedPriority (internal/api/role_seed_priority.go), and the
// secondary sort drops last_used_at entirely. BumpActivation stays in the
// API for backward compatibility with the post-call hook that calls it,
// but performs no DB write. Real tool-call telemetry lives in event_log
// (event_type='tool_call'); the FU-17 scheduled "tuning" job reads
// event_log and updates roleSeedPriority offline.
func (s *Store) BumpActivation(ctx context.Context, agentID, toolName string) error {
	// Intentional no-op. See doc comment above (FU-14).
	_ = ctx
	_ = agentID
	_ = toolName
	return nil
}
