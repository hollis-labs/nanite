package store

import (
	"context"
	"errors"
	"os"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/nanite/internal/transcript"
)

// RebuildIndex replays the conversation projection while retaining local tool state.
func (s *Store) RebuildIndex(ctx context.Context) error {
	dirs, err := os.ReadDir(s.transcripts)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
			return err
		}
		for _, stmt := range []string{"DELETE FROM transcript_cursors", "DELETE FROM compactions", "DELETE FROM messages", "DELETE FROM turns", "DELETE FROM conversations"} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		for _, dir := range dirs {
			if !dir.IsDir() {
				continue
			}
			entries, err := transcript.Replay(s.transcripts, dir.Name(), transcript.Cursor{})
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := projectEntry(ctx, tx, dir.Name(), entry); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
