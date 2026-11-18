package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hollis-labs/libs/agent/core/toolresult"
	core "github.com/hollis-labs/libs/core/store"
)

type ResultStore struct{ store *Store }

func (s *Store) Results() ResultStore { return ResultStore{s} }

func (r ResultStore) Put(ctx context.Context, e toolresult.Entry) error {
	var body any
	if e.BodyStored {
		body = e.Body
	}
	return r.store.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO tool_results(scope,id,tool_name,tool_call_id,created_at,body,byte_size,expires_at)
   VALUES(?,?,?,?,?,?,?,?)`, e.Scope, e.ID, e.Tool, e.CallID, format(e.CreatedAt), body, e.ByteSize, format(e.ExpiresAt))
		return err
	})
}

func (r ResultStore) Get(ctx context.Context, scope, id string) (toolresult.Entry, error) {
	e := toolresult.Entry{ID: id, Scope: scope}
	var body sql.NullString
	var created, expires string
	err := r.store.db.Reader().QueryRowContext(ctx, `SELECT tool_name,tool_call_id,created_at,body,byte_size,expires_at
  FROM tool_results WHERE scope=? AND id=?`, scope, id).Scan(&e.Tool, &e.CallID, &created, &body, &e.ByteSize, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return toolresult.Entry{}, toolresult.ErrNotFound
	}
	if err != nil {
		return toolresult.Entry{}, err
	}
	e.Body, e.BodyStored = body.String, body.Valid
	e.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return toolresult.Entry{}, err
	}
	e.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	return e, err
}

func (r ResultStore) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	var removed int64
	err := r.store.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM tool_results WHERE expires_at < ?", format(before))
		if err != nil {
			return err
		}
		removed, err = result.RowsAffected()
		return err
	})
	return int(removed), err
}

var _ toolresult.Store = ResultStore{}
