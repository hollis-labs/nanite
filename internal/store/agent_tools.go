package store

import (
	"context"
	"fmt"
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
	return grantAgentTool(ctx, s.DB, agentID, toolID, grantedVia)
}

func grantAgentTool(ctx context.Context, db agentConfigDB, agentID, toolID, grantedVia string) error {
	// Host issuance must be supplied through a verified actor grant port.
	return ErrVerifiedActorRequired
}

// HasLegacyToolsBackfillRun reports whether BackfillAgentToolsFromLegacyColumns
// has already considered agentID -- a dedicated, independent marker
// (agent_tools_legacy_backfill), NOT derived from whether any resulting
// agent_tools rows still exist. See that table's migration doc comment
// (116_known_tools_and_agent_tools.sql) for why: an agent with a
// deny-everything policy legitimately gets zero grants and must still be
// marked as "considered" so it isn't recomputed forever, and an operator
// revoking every grant a backfill produced must not un-mark the agent as
// backfilled (which would let a later boot silently re-derive and
// re-assert the very grant the operator revoked).
func (s *Store) HasLegacyToolsBackfillRun(ctx context.Context, agentID string) (bool, error) {
	// Legacy profile declarations cannot initialize actor authority.
	return false, ErrVerifiedActorRequired
}

// MarkLegacyToolsBackfillRun records that BackfillAgentToolsFromLegacyColumns
// has considered agentID, regardless of how many (if any) agent_tools rows
// it granted. Idempotent -- INSERT OR IGNORE.
func (s *Store) MarkLegacyToolsBackfillRun(ctx context.Context, agentID string) error {
	// Legacy profile declarations cannot initialize actor authority.
	return ErrVerifiedActorRequired
}

// HasAgentToolGrantedVia reports whether agentID has at least one
// agent_tools row tagged with the given grantedVia provenance. This is a
// provenance/audit query, NOT the backfill's one-time-per-agent guard --
// see HasLegacyToolsBackfillRun for that.
func (s *Store) HasAgentToolGrantedVia(ctx context.Context, agentID, grantedVia string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM actor_granted_tools WHERE agent_id = ? AND granted_via = ? AND EXISTS (SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id WHERE b.actor_uri=actor_granted_tools.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0) LIMIT 1`,
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
		`DELETE FROM actor_granted_tools WHERE agent_id = ? AND tool_id = ?`,
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
		   FROM actor_granted_tools at
		   JOIN known_tools kt ON kt.id = at.tool_id
		  WHERE at.agent_id = ? AND EXISTS (SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id WHERE b.actor_uri=at.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0)
		  ORDER BY kt.name`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_tools names: %w", err)
	}
	defer closeRows(rows)

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
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM actor_granted_tools WHERE agent_id = ? AND EXISTS (SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id WHERE b.actor_uri=actor_granted_tools.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0)`, agentID).Scan(&n)
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
// (116_known_tools_and_agent_tools.sql) for the naming-collision analysis.
func (s *Store) GrantAgentDispatchTool(ctx context.Context, agentID, toolID string) error {
	// Host issuance must be supplied through a verified actor grant port.
	return ErrVerifiedActorRequired
}

// RevokeAgentDispatchTool deletes a single agent_dispatch_tool_allowlist
// row. No error if the row does not exist.
func (s *Store) RevokeAgentDispatchTool(ctx context.Context, agentID, toolID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM actor_dispatch_tool_allowlist WHERE agent_id = ? AND tool_id = ?`,
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
		   FROM actor_dispatch_tool_allowlist adt
		   JOIN known_tools kt ON kt.id = adt.tool_id
		  WHERE adt.agent_id = ? AND EXISTS (SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id WHERE b.actor_uri=adt.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0)
		  ORDER BY kt.name`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_dispatch_tool_allowlist names: %w", err)
	}
	defer closeRows(rows)

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

// InitialAgentToolGrant is one resolved catalog grant from an operator's
// installation declaration. Request metadata is never an input here.
type InitialAgentToolGrant struct {
	ToolID     string
	GrantedVia string
}

// InitializeAgentToolGrants snapshots installation grants exactly once. The
// marker and grants commit together, including an intentionally empty grant
// set. Subsequent imports or boot backfills cannot restore revoked grants.
func (s *Store) InitializeAgentToolGrants(ctx context.Context, agentID string, grants []InitialAgentToolGrant) error {
	// Legacy profile declarations cannot initialize actor authority.
	return ErrVerifiedActorRequired
}
