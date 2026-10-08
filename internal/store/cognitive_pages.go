package store

import (
	"context"
	"fmt"
)

// ListCognitiveSessionPage uses immutable creation order, so new activity
// cannot move a view across a pagination boundary.
func (s *Store) ListCognitiveSessionPage(ctx context.Context, beforeCreated, beforeID string, limit int, includeArchived bool) ([]Session, error) {
	query := `SELECT id FROM sessions WHERE (? OR status != 'archived')`
	args := []any{includeArchived}
	if beforeID != "" {
		query += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, beforeCreated, beforeCreated, beforeID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list cognitive views: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			closeRows(rows)
			return nil, scanErr
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(ids))
	for _, id := range ids {
		view, err := s.GetSession(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *view)
	}
	return out, nil
}

// ListCognitiveMessagePage returns the latest/older bounded page in oldest-first
// order. The ID tie-breaker keeps equal timestamps deterministic.
func (s *Store) ListCognitiveMessagePage(ctx context.Context, sessionID, beforeCreated, beforeID string, limit int) ([]Message, error) {
	query := `SELECT id, session_id, COALESCE(agent_id,''), role, content,
 COALESCE(envelope,''), COALESCE(metadata,'{}'), COALESCE(parent_id,''), is_compacted, created_at
 FROM messages WHERE session_id = ?`
	args := []any{sessionID}
	if beforeID != "" {
		query += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, beforeCreated, beforeCreated, beforeID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list cognitive history: %w", err)
	}
	defer closeRows(rows)
	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.AgentID, &m.Role, &m.Content, &m.Envelope, &m.Metadata, &m.ParentID, &m.IsCompacted, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
