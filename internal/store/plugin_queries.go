package store

import (
	"context"
	"fmt"
	"strings"
)

const maxPluginQueryRows = 101 // query limit plus one row to report omitted data

// ReadPluginQuerySessions returns a bounded metadata projection. An absent
// allowlist never expands to all sessions: workspace access must be explicit.
func (s *Store) ReadPluginQuerySessions(ctx context.Context, ids []string, allSessions bool, limit int) ([]Session, error) {
	if limit < 1 || limit > maxPluginQueryRows || allSessions && len(ids) != 0 || len(ids) > 64 {
		return nil, fmt.Errorf("invalid plugin session query scope or limit")
	}
	if !allSessions && len(ids) == 0 {
		return []Session{}, nil
	}
	query := `SELECT id, short_code, COALESCE(title, ''), COALESCE(custom_name, ''), COALESCE(project_id, ''), COALESCE(provider, ''), COALESCE(model, ''), status, created_at, updated_at FROM sessions WHERE status != 'archived'`
	args := make([]any, 0, len(ids)+1)
	if !allSessions {
		// #nosec G202 -- only a bounded count of placeholders is concatenated; every session ID is bound separately.
		query += ` AND id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query += ` ORDER BY last_activity DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read plugin session metadata: %w", err)
	}
	defer closeRows(rows)
	out := make([]Session, 0)
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.ShortCode, &session.Title, &session.CustomName, &session.ProjectID, &session.Provider, &session.Model, &session.Status, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan plugin session metadata: %w", err)
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

// ReadPluginQueryMetrics bounds per-session accounting at SQL and projects
// private debug/configuration/error strings away before loading any rows.
func (s *Store) ReadPluginQueryMetrics(ctx context.Context, sessionID string, limit int) ([]ExecutionMetrics, error) {
	if sessionID == "" || limit < 1 || limit > maxPluginQueryRows {
		return nil, fmt.Errorf("invalid plugin execution query scope or limit")
	}
	const projection = `id, session_id, message_id, provider, adapter, model,
        agent_id, agent_slug, mode, duration_ms, context_messages, context_tokens,
        input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
        estimated_cost_usd, tool_iterations, tool_calls, is_utility, stop_reason,
        CASE WHEN error != '' THEN 'present' ELSE '' END, '', created_at,
        profile_name, profile_digest, ''`
	rows, err := s.DB.QueryContext(ctx, `SELECT `+projection+` FROM execution_metrics WHERE session_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("read plugin execution accounting: %w", err)
	}
	defer closeRows(rows)
	out := make([]ExecutionMetrics, 0)
	for rows.Next() {
		metric, err := scanExecutionMetrics(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plugin execution accounting: %w", err)
		}
		out = append(out, metric)
	}
	return out, rows.Err()
}
