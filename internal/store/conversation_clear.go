package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ConversationClear is host-owned state in session_events. Message metadata is
// only its transcript presentation; request metadata cannot establish a cut.
type ConversationClear struct {
	MessageID         string `json:"message_id"`
	KeepHandoff       bool   `json:"keep_handoff"`
	CompactionEventID string `json:"compaction_event_id,omitempty"`
}

func (s *Store) LatestConversationClear(ctx context.Context, sessionID string) (*ConversationClear, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT envelope_pointer_json FROM session_events WHERE session_id = ? AND event_type = 'conversation_cleared' ORDER BY rowid DESC LIMIT 1`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cut ConversationClear
	if err = json.Unmarshal([]byte(raw), &cut); err != nil {
		return nil, err
	}
	if cut.MessageID == "" {
		return nil, fmt.Errorf("clear boundary has no message")
	}
	return &cut, nil
}

// ClearConversation commits a visible boundary, its authoritative event,
// handoff disposal and provider-resume reset together. Prior messages, pins,
// system context and authority are untouched. The caller drains turn owners
// and fences admissions before calling this method.
func (s *Store) ClearConversation(ctx context.Context, sessionID string, keepHandoff bool) (*ConversationClear, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackUnlessCommitted(tx)
	var exists string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id = ?`, sessionID).Scan(&exists); err != nil {
		return nil, err
	}
	cut := &ConversationClear{MessageID: uuid.NewString(), KeepHandoff: keepHandoff}
	err = tx.QueryRowContext(ctx, `SELECT id FROM compaction_events WHERE session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, sessionID).Scan(&cut.CompactionEventID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	raw, err := json.Marshal(cut)
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(map[string]any{"conversation_cleared": cut})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages (id, session_id, role, content, metadata, is_compacted, created_at) VALUES (?, ?, 'system', 'Conversation cleared here', ?, 0, ?)`, cut.MessageID, sessionID, string(metadata), now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO session_events (id, session_id, event_type, envelope_pointer_json, created_at) VALUES (?, ?, 'conversation_cleared', ?, ?)`, uuid.NewString(), sessionID, string(raw), now); err != nil {
		return nil, err
	}
	if !keepHandoff {
		if _, err = tx.ExecContext(ctx, `DELETE FROM handoff_stashes WHERE session_id = ?`, sessionID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agent_runtime SET provider_session_id = '', updated_at = ? WHERE id = ?`, now, sessionID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET message_count = message_count + 1, last_activity = ?, updated_at = ? WHERE id = ?`, now, now, sessionID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return cut, nil
}

// ListWorkingMessages reads the conversation after the host's last boundary.
// Joining the retained marker makes equal-second timestamps unambiguous, and
// does not persist SQLite rowids across reopen or VACUUM.
func (s *Store) ListWorkingMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	cut, err := s.LatestConversationClear(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if cut == nil {
		return s.ListMessages(ctx, sessionID, limit)
	}
	var boundary int64
	if err = s.DB.QueryRowContext(ctx, `SELECT rowid FROM messages WHERE session_id = ? AND id = ?`, sessionID, cut.MessageID).Scan(&boundary); err != nil {
		return nil, fmt.Errorf("resolve clear boundary: %w", err)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, session_id, COALESCE(agent_id,''), role, content, COALESCE(envelope,''), COALESCE(metadata,'{}'), COALESCE(parent_id,''), is_compacted, created_at FROM messages WHERE session_id = ? AND rowid > ? ORDER BY created_at DESC, rowid DESC LIMIT ?`, sessionID, boundary, limit)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := make([]Message, 0)
	for rows.Next() {
		var msg Message
		if err = rows.Scan(&msg.ID, &msg.SessionID, &msg.AgentID, &msg.Role, &msg.Content, &msg.Envelope, &msg.Metadata, &msg.ParentID, &msg.IsCompacted, &msg.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// A fork that includes the transcript also includes its authoritative cut;
// copying a presentation marker alone would revive cleared working history.
func copyConversationClear(ctx context.Context, tx *sql.Tx, sourceID, targetID string, copiedIDs map[string]string, now string) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT envelope_pointer_json FROM session_events WHERE session_id = ? AND event_type = 'conversation_cleared' ORDER BY rowid DESC LIMIT 1`, sourceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var cut ConversationClear
	if err = json.Unmarshal([]byte(raw), &cut); err != nil {
		return err
	}
	markerID, ok := copiedIDs[cut.MessageID]
	if !ok {
		return fmt.Errorf("copied transcript omitted clear boundary")
	}
	cut.MessageID = markerID
	rawBytes, err := json.Marshal(cut)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session_events (id, session_id, event_type, envelope_pointer_json, created_at) VALUES (?, ?, 'conversation_cleared', ?, ?)`, uuid.NewString(), targetID, string(rawBytes), now)
	return err
}
