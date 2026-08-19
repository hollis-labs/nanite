package store

import (
	"context"
	"fmt"
	"time"
)

// GrantAgentTool inserts an agent_tools row (agent_id, tool_id) if it does
// not already exist. This is the real FK-based replacement for
// agent_profiles.tools/tool_permissions/role_tools' free-text selection
// semantics -- see TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md.
//
// grantedVia is a provenance tag ("explicit" | "role_seed" |
// "legacy_backfill" -- see the migration's doc comment); empty defaults to
// "explicit". Existing rows are not overwritten (ON CONFLICT DO NOTHING),
// so re-granting an already-granted tool never changes its original
// provenance tag.
func (s *Store) GrantAgentTool(ctx context.Context, agentID, toolID, grantedVia string) error {
	if agentID == "" || toolID == "" {
		return fmt.Errorf("grant agent_tools: agent_id and tool_id are required")
	}
	if grantedVia == "" {
		grantedVia = "explicit"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_tools (agent_id, tool_id, granted_via, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(agent_id, tool_id) DO NOTHING`,
		agentID, toolID, grantedVia, now,
	)
	if err != nil {
		return fmt.Errorf("grant agent_tools: %w", err)
	}
	return nil
}

// HasLegacyToolsBackfillRun reports whether BackfillAgentToolsFromLegacyColumns
// has already considered agentID -- a dedicated, independent marker
// (agent_tools_legacy_backfill), NOT derived from whether any resulting
// agent_tools rows still exist. See that table's migration doc comment
// (110_known_tools_and_agent_tools.sql) for why: an agent with a
// deny-everything policy legitimately gets zero grants and must still be
// marked as "considered" so it isn't recomputed forever, and an operator
// revoking every grant a backfill produced must not un-mark the agent as
// backfilled (which would let a later boot silently re-derive and
// re-assert the very grant the operator revoked).
func (s *Store) HasLegacyToolsBackfillRun(ctx context.Context, agentID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tools_legacy_backfill WHERE agent_id = ?`,
		agentID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check agent_tools_legacy_backfill: %w", err)
	}
	return n > 0, nil
}

// MarkLegacyToolsBackfillRun records that BackfillAgentToolsFromLegacyColumns
// has considered agentID, regardless of how many (if any) agent_tools rows
// it granted. Idempotent -- INSERT OR IGNORE.
func (s *Store) MarkLegacyToolsBackfillRun(ctx context.Context, agentID string) error {
	if agentID == "" {
		return fmt.Errorf("mark agent_tools_legacy_backfill: agent_id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_tools_legacy_backfill (agent_id, created_at) VALUES (?, ?)
		 ON CONFLICT(agent_id) DO NOTHING`,
		agentID, now,
	)
	if err != nil {
		return fmt.Errorf("mark agent_tools_legacy_backfill: %w", err)
	}
	return nil
}

// HasAgentToolGrantedVia reports whether agentID has at least one
// agent_tools row tagged with the given grantedVia provenance. This is a
// provenance/audit query, NOT the backfill's one-time-per-agent guard --
// see HasLegacyToolsBackfillRun for that.
func (s *Store) HasAgentToolGrantedVia(ctx context.Context, agentID, grantedVia string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tools WHERE agent_id = ? AND granted_via = ? LIMIT 1`,
		agentID, grantedVia,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check agent_tools granted_via: %w", err)
	}
	return n > 0, nil
}

// RevokeAgentTool deletes a single agent_tools grant row. No error if the
// row does not exist.
func (s *Store) RevokeAgentTool(ctx context.Context, agentID, toolID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_tools WHERE agent_id = ? AND tool_id = ?`,
		agentID, toolID,
	)
	if err != nil {
		return fmt.Errorf("revoke agent_tools: %w", err)
	}
	return nil
}

// ListAgentToolNames returns the names of every known_tools row this agent
// has been granted, joined through agent_tools. Used by the eventual
// SelectForAgent read path (see TASKS/phase-1/11-wire-select-for-agent-to-
// read-agent-tools.md -- deferred follow-up, not yet a live consumer).
func (s *Store) ListAgentToolNames(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT kt.name
		   FROM agent_tools at
		   JOIN known_tools kt ON kt.id = at.tool_id
		  WHERE at.agent_id = ?
		  ORDER BY kt.name`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_tools names: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan agent_tools name: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// CountAgentTools returns the number of agent_tools grant rows for an
// agent. Used by callers (e.g. the backfill) that need to know whether an
// agent already has explicit grants without loading the full name list.
func (s *Store) CountAgentTools(ctx context.Context, agentID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_tools WHERE agent_id = ?`, agentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count agent_tools: %w", err)
	}
	return n, nil
}

// GrantAgentDispatchTool inserts an agent_dispatch_tool_allowlist row
// (agent_id, tool_id) if it does not already exist -- the tools this agent
// may authorize a subagent it dispatches to use. Deliberately a separate
// table from agent_profiles.parent_dispatch_allowlist (role slugs a parent
// may dispatch task_execute to) -- see this migration's doc comment
// (110_known_tools_and_agent_tools.sql) for the naming-collision analysis.
func (s *Store) GrantAgentDispatchTool(ctx context.Context, agentID, toolID string) error {
	if agentID == "" || toolID == "" {
		return fmt.Errorf("grant agent_dispatch_tool_allowlist: agent_id and tool_id are required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_dispatch_tool_allowlist (agent_id, tool_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(agent_id, tool_id) DO NOTHING`,
		agentID, toolID, now,
	)
	if err != nil {
		return fmt.Errorf("grant agent_dispatch_tool_allowlist: %w", err)
	}
	return nil
}

// RevokeAgentDispatchTool deletes a single agent_dispatch_tool_allowlist
// row. No error if the row does not exist.
func (s *Store) RevokeAgentDispatchTool(ctx context.Context, agentID, toolID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_dispatch_tool_allowlist WHERE agent_id = ? AND tool_id = ?`,
		agentID, toolID,
	)
	if err != nil {
		return fmt.Errorf("revoke agent_dispatch_tool_allowlist: %w", err)
	}
	return nil
}

// ListAgentDispatchToolNames returns the names of every known_tools row
// this agent may authorize a dispatched subagent to use.
func (s *Store) ListAgentDispatchToolNames(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT kt.name
		   FROM agent_dispatch_tool_allowlist adt
		   JOIN known_tools kt ON kt.id = adt.tool_id
		  WHERE adt.agent_id = ?
		  ORDER BY kt.name`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_dispatch_tool_allowlist names: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan agent_dispatch_tool_allowlist name: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
