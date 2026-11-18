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
	defer rows.Close()
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
