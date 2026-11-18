package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	core "github.com/hollis-labs/libs/core/store"
)

type Todo struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

func (s *Store) Todos(ctx context.Context, conversation string) ([]Todo, error) {
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT content,status FROM todos WHERE conversation_id=? ORDER BY position", conversation)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
	out := []Todo{}
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.Text, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceTodos(ctx context.Context, conversation string, todos []Todo) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM todos WHERE conversation_id=?", conversation); err != nil {
			return err
		}
		for i, t := range todos {
			if _, err := tx.ExecContext(ctx, "INSERT INTO todos(id,conversation_id,content,status,position) VALUES(?,?,?,?,?)",
				NewID(time.Now()), conversation, t.Text, t.Status, i); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Scratch(ctx context.Context, conversation, key string) (string, error) {
	var content string
	err := s.db.Reader().QueryRowContext(ctx, "SELECT content FROM scratchpad WHERE conversation_id=? AND key=?", conversation, key).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return content, err
}

func (s *Store) SetScratch(ctx context.Context, conversation, key, content string) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO scratchpad(conversation_id,key,content,updated_at) VALUES(?,?,?,?)
   ON CONFLICT(conversation_id,key) DO UPDATE SET content=excluded.content,updated_at=excluded.updated_at`,
			conversation, key, content, format(time.Now()))
		return err
	})
}
