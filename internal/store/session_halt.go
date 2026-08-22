package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// HaltStatus is the per-session halt record consumed by the monitor-loop
// driver's pre-tick check. HaltedAt == nil means the session is healthy
// and tick scheduling proceeds normally.
type HaltStatus struct {
	SessionID    string  `json:"session_id"`
	HaltedAt     *string `json:"halted_at,omitempty"`
	HaltedReason *string `json:"halted_reason,omitempty"`
}

// IsHalted is a convenience helper for callers that just want the
// boolean — drives the pre-tick skip in the driver.
func (h HaltStatus) IsHalted() bool { return h.HaltedAt != nil }

// GetSessionHalt returns the halt status for a session. A non-existent
// session returns ErrNoRows; callers should treat that as "no halt"
// rather than an error if they only care about the boolean.
func (s *Store) GetSessionHalt(ctx context.Context, sessionID string) (*HaltStatus, error) {
	var halted sql.NullString
	var reason sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT halted_at, halted_reason FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&halted, &reason)
	if err != nil {
		return nil, fmt.Errorf("get session halt %s: %w", sessionID, err)
	}
	out := &HaltStatus{SessionID: sessionID}
	if halted.Valid {
		out.HaltedAt = &halted.String
	}
	if reason.Valid {
		out.HaltedReason = &reason.String
	}
	return out, nil
}

// MarkSessionHalted stamps halted_at + halted_reason for a session.
// Idempotent — if the session is already halted, the existing halted_at
// is preserved so the reason can be updated without losing the original
// trip time. Callers that want to overwrite must ClearSessionHalt first.
func (s *Store) MarkSessionHalted(ctx context.Context, sessionID, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE sessions
		    SET halted_at = COALESCE(halted_at, ?),
		        halted_reason = ?,
		        updated_at = ?
		  WHERE id = ?`,
		now, reason, now, sessionID,
	)
	if err != nil {
		return fmt.Errorf("mark session halted %s: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark session halted %s: rows affected: %w", sessionID, err)
	}
	if n == 0 {
		return fmt.Errorf("mark session halted %s: session not found", sessionID)
	}
	return nil
}

// ClearSessionHalt clears halted_at + halted_reason so the monitor loop
// resumes ticking the session. Spike-style: the only resume path is
// operator action (manual SQL UPDATE OR POST /api/sessions/{id}/resume).
func (s *Store) ClearSessionHalt(ctx context.Context, sessionID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE sessions
		    SET halted_at = NULL,
		        halted_reason = NULL,
		        updated_at = ?
		  WHERE id = ?`,
		now, sessionID,
	)
	if err != nil {
		return fmt.Errorf("clear session halt %s: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("clear session halt %s: rows affected: %w", sessionID, err)
	}
	if n == 0 {
		return fmt.Errorf("clear session halt %s: session not found", sessionID)
	}
	return nil
}

// LastNTokenUsage returns the most recent N token_usage rows for a
// session, ordered most-recent-first. Used by the monitor-loop circuit
// breaker (FU-21) — Detector B inspects (input_tokens, cache_read_tokens,
// output_tokens) on the last 3 rows; Detector C inspects output_tokens
// for verbatim-echo identity.
func (s *Store) LastNTokenUsage(ctx context.Context, sessionID string, n int) ([]TokenUsage, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, message_id, model,
		        input_tokens, output_tokens, total_tokens,
		        tool_input_tokens, cache_creation_tokens, cache_read_tokens,
		        estimated_cost_usd, created_at
		   FROM token_usage
		  WHERE session_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`,
		sessionID, n,
	)
	if err != nil {
		return nil, fmt.Errorf("last %d token_usage for %s: %w", n, sessionID, err)
	}
	defer rows.Close()
	out := make([]TokenUsage, 0, n)
	for rows.Next() {
		var u TokenUsage
		if err := rows.Scan(
			&u.ID, &u.SessionID, &u.MessageID, &u.Model,
			&u.InputTokens, &u.OutputTokens, &u.TotalTokens,
			&u.ToolInputTokens, &u.CacheCreationTokens, &u.CacheReadTokens,
			&u.EstimatedCostUSD, &u.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan token_usage: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// LastNAssistantMessages returns the most recent N assistant messages
// for a session, ordered most-recent-first. Used by the monitor-loop
// circuit breaker — Detector A scans content for `[generation interrupted]`;
// Detector C compares first-1KB content body for verbatim echo.
func (s *Store) LastNAssistantMessages(ctx context.Context, sessionID string, n int) ([]Message, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, COALESCE(agent_id,''), role, content,
		        COALESCE(envelope,''), COALESCE(metadata,'{}'),
		        COALESCE(parent_id,''), is_compacted, created_at
		   FROM messages
		  WHERE session_id = ? AND role = 'assistant'
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`,
		sessionID, n,
	)
	if err != nil {
		return nil, fmt.Errorf("last %d assistant messages for %s: %w", n, sessionID, err)
	}
	defer rows.Close()
	out := make([]Message, 0, n)
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.SessionID, &m.AgentID, &m.Role, &m.Content,
			&m.Envelope, &m.Metadata, &m.ParentID, &m.IsCompacted, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ToolCallsForMessage returns the tool_calls count recorded on the
// execution_metrics row for the given assistant message id. Zero when
// no execution_metrics row exists (e.g. for pre-FU-21 sessions or
// utility calls) — the caller treats "no row" as zero tool calls,
// which is conservative for Detector C (only halts when ALL three of
// the last N messages have zero tool calls).
func (s *Store) ToolCallsForMessage(ctx context.Context, messageID string) (int, error) {
	var n sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT tool_calls FROM execution_metrics WHERE message_id = ? ORDER BY id DESC LIMIT 1`,
		messageID,
	).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("tool_calls for %s: %w", messageID, err)
	}
	if !n.Valid {
		return 0, nil
	}
	return int(n.Int64), nil
}
