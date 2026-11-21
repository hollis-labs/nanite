package store

import (
	"context"
	"time"

	core "github.com/hollis-labs/libs/core/store"
)

type Notice struct{ ID, ConversationID, Kind, Body string }

func (s *Store) AddNotice(ctx context.Context, n Notice) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO notices(id,conversation_id,kind,body,created_at) VALUES(?,?,?,?,?)", n.ID, n.ConversationID, n.Kind, n.Body, format(time.Now()))
		return err
	})
}

func (s *Store) PendingNotices(ctx context.Context, conversation string) ([]Notice, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,kind,body FROM notices WHERE conversation_id=?
  AND delivered_turn_id IS NULL ORDER BY created_at,id`, conversation)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
	var notices []Notice
	for rows.Next() {
		n := Notice{ConversationID: conversation}
		if err := rows.Scan(&n.ID, &n.Kind, &n.Body); err != nil {
			return nil, err
		}
		notices = append(notices, n)
	}
	return notices, rows.Err()
}

func (s *Store) MarkNoticesDelivered(ctx context.Context, conversation, turn string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE notices SET delivered_turn_id=? WHERE id=? AND conversation_id=?
    AND delivered_turn_id IS NULL`, turn, id, conversation); err != nil {
				return err
			}
		}
		return nil
	})
}

// WithdrawNotice deletes a notice not yet marked delivered. A turn marks the
// notices it showed only when it ends, so the caller must not withdraw one a
// running turn may have read.
func (s *Store) WithdrawNotice(ctx context.Context, conversation, id string) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM notices WHERE id=? AND conversation_id=? AND delivered_turn_id IS NULL`, id, conversation)
		return err
	})
}

// QueueNotice inserts a stable notice once. Completed delivery survives both
// acknowledgement failure and process restart. Failed turns may retry delivery;
// interrupted turns retain it until their recovery decision is made.
func (s *Store) QueueNotice(ctx context.Context, n Notice) (bool, error) {
	var handled bool
	err := s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notices(id,conversation_id,kind,body,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, n.ID, n.ConversationID, n.Kind, n.Body, format(time.Now())); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(t.status,'') FROM notices n LEFT JOIN turns t ON t.id=n.delivered_turn_id WHERE n.id=? AND n.conversation_id=?`, n.ID, n.ConversationID).Scan(&status); err != nil {
			return err
		}
		handled = status == "completed"
		if status == "failed" || status == "cancelled" {
			_, err := tx.ExecContext(ctx, `UPDATE notices SET delivered_turn_id=NULL WHERE id=?`, n.ID)
			return err
		}
		return nil
	})
	return handled, err
}
