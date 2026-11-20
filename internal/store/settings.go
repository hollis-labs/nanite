package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	core "github.com/hollis-labs/libs/core/store"
)

// Setting is a conversation's setting by key, "" when unset.
func (s *Store) Setting(ctx context.Context, conversation, key string) (string, error) {
	var v string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT value FROM conversation_settings WHERE conversation_id=? AND key=?`, conversation, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a conversation's setting; "" removes it.
func (s *Store) SetSetting(ctx context.Context, conversation, key, value string) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		if value == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM conversation_settings WHERE conversation_id=? AND key=?`, conversation, key)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO conversation_settings(conversation_id,key,value,updated_at) VALUES(?,?,?,?)
  ON CONFLICT(conversation_id,key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, conversation, key, value, format(time.Now()))
		return err
	})
}
