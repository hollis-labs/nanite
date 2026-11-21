package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/transcript"
)

// IntegrityIndex reads what the index holds for one conversation, in one
// read transaction, in the shape integrity.Validate compares.
func (s *Store) IntegrityIndex(ctx context.Context, conversation string) (integrity.Index, error) {
	var out integrity.Index
	err := s.db.ReadTx(ctx, func(ctx context.Context, q core.Querier) error {
		var raw string
		switch err := q.QueryRowContext(ctx, "SELECT cursor FROM transcript_cursors WHERE conversation_id=?", conversation).Scan(&raw); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			var cursor transcript.Cursor
			if err := json.Unmarshal([]byte(raw), &cursor); err != nil {
				return err
			}
			out.Cursor = &cursor
		}
		return errors.Join(
			scanRows(ctx, q, "SELECT id,status,user_message_id FROM turns WHERE conversation_id=? ORDER BY seq", conversation,
				func(rows *sql.Rows) error {
					var t integrity.IndexTurn
					err := rows.Scan(&t.ID, &t.Status, &t.UserMessageID)
					out.Turns = append(out.Turns, t)
					return err
				}),
			scanRows(ctx, q, "SELECT id,turn_id,role FROM messages WHERE conversation_id=? ORDER BY seq", conversation,
				func(rows *sql.Rows) error {
					var m integrity.IndexMessage
					err := rows.Scan(&m.ID, &m.TurnID, &m.Role)
					out.Messages = append(out.Messages, m)
					return err
				}),
			scanRows(ctx, q, "SELECT id,turn_id,through_message_id FROM compactions WHERE conversation_id=? ORDER BY seq", conversation,
				func(rows *sql.Rows) error {
					var c integrity.IndexCompaction
					err := rows.Scan(&c.ID, &c.TurnID, &c.ThroughMessageID)
					out.Compactions = append(out.Compactions, c)
					return err
				}),
			scanRows(ctx, q, "SELECT id,COALESCE(delivered_turn_id,'') FROM notices WHERE conversation_id=? ORDER BY created_at,id", conversation,
				func(rows *sql.Rows) error {
					var n integrity.IndexNotice
					err := rows.Scan(&n.ID, &n.DeliveredTurnID)
					out.Notices = append(out.Notices, n)
					return err
				}),
		)
	})
	return out, err
}

func scanRows(ctx context.Context, q core.Querier, query, conversation string, scan func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, conversation)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ValidateConversation checks one conversation's transcript and its index.
// It reads the index before replaying the transcript: a writer appending in
// between can only make the index look behind (recoverable), never ahead.
// hold, if set, is the conversation's saved interrupted-turn hold. It writes
// nothing.
func (s *Store) ValidateConversation(ctx context.Context, conversation string, hold *integrity.Hold) (integrity.Report, error) {
	if _, err := transcript.ConversationDir(s.transcripts, conversation); err != nil {
		return integrity.Report{}, err
	}
	index, err := s.IntegrityIndex(ctx, conversation)
	if err != nil {
		return integrity.Report{}, err
	}
	entries, replayErr := transcript.Replay(s.transcripts, conversation, transcript.Cursor{})
	return integrity.Validate(integrity.Input{ConversationID: conversation, Entries: entries, ReplayErr: replayErr, Index: &index, Hold: hold}), nil
}
