package store

import (
	"context"
	"time"

	core "github.com/hollis-labs/libs/core/store"
)

// SaveBudget commits the agent-wide counter and optional threshold notice
// together, so a restart cannot retain the warning marker without its notice.
func (s *Store) SaveBudget(ctx context.Context, key, value string, notice *Notice) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO conversation_settings(conversation_id,key,value,updated_at) VALUES('',?,?,?) ON CONFLICT(conversation_id,key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, format(time.Now()))
		if err != nil || notice == nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO notices(id,conversation_id,kind,body,created_at) VALUES(?,?,?,?,?)", notice.ID, notice.ConversationID, notice.Kind, notice.Body, format(time.Now()))
		return err
	})
}
