package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// DefaultSessionObjectMaxBytes is the default hard cap on a single session_objects
// payload. Per-object rather than per-session: an oversized payload is rejected
// at put-time. In-session total-size caps + LRU are a follow-up (see BLG).
const DefaultSessionObjectMaxBytes = 1 * 1024 * 1024 // 1 MiB

// ErrSessionObjectNotFound is returned by GetSessionObject when no row matches
// the (session_id, id) tuple. Cross-session lookups intentionally surface this
// error (D5: no fallback to same-id-different-session lookups).
var ErrSessionObjectNotFound = errors.New("session object not found")

// ErrSessionObjectTooLarge is returned by PutSessionObject when the payload
// byte length exceeds DefaultSessionObjectMaxBytes.
var ErrSessionObjectTooLarge = errors.New("session object payload exceeds maximum size")

// SessionObject is a row in the session_objects table. Payload is opaque JSON;
// the consumer (e.g. card-envelope pipeline CW-20260420-0011) validates + types.
type SessionObject struct {
	ID          string
	SessionID   string
	ContentType string
	ByteSize    int
	Payload     string
	CreatedAt   time.Time
}

// SessionObjectInput is the payload supplied to PutSessionObject. ID is
// server-generated (ULID); callers do not supply it.
type SessionObjectInput struct {
	SessionID   string
	ContentType string // optional; defaults to "application/json"
	Payload     string
}

// PutSessionObject inserts a new session object and returns the resulting row
// (including the server-generated ULID). Rejects oversize payloads with
// ErrSessionObjectTooLarge.
func (s *Store) PutSessionObject(ctx context.Context, in SessionObjectInput) (SessionObject, error) {
	if in.SessionID == "" {
		return SessionObject{}, fmt.Errorf("put session object: session_id is required")
	}
	if in.Payload == "" {
		return SessionObject{}, fmt.Errorf("put session object: payload is required")
	}
	byteSize := len(in.Payload)
	if byteSize > DefaultSessionObjectMaxBytes {
		return SessionObject{}, fmt.Errorf("put session object (size=%d cap=%d): %w", byteSize, DefaultSessionObjectMaxBytes, ErrSessionObjectTooLarge)
	}
	ct := in.ContentType
	if ct == "" {
		ct = "application/json"
	}
	id := newSessionObjectULID()
	now := time.Now().UTC()

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO session_objects (id, session_id, content_type, byte_size, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, in.SessionID, ct, byteSize, in.Payload, now.Format(time.RFC3339),
	)
	if err != nil {
		return SessionObject{}, fmt.Errorf("put session object: %w", err)
	}

	return SessionObject{
		ID:          id,
		SessionID:   in.SessionID,
		ContentType: ct,
		ByteSize:    byteSize,
		Payload:     in.Payload,
		CreatedAt:   now,
	}, nil
}

// GetSessionObject fetches a single row by the (session_id, id) tuple. D5:
// both values are required and the lookup is strictly scoped — a mismatch on
// either returns ErrSessionObjectNotFound with no fallback path.
func (s *Store) GetSessionObject(ctx context.Context, sessionID, id string) (SessionObject, error) {
	if sessionID == "" || id == "" {
		return SessionObject{}, ErrSessionObjectNotFound
	}
	var rec SessionObject
	var createdAtStr string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, content_type, byte_size, payload, created_at
		 FROM session_objects WHERE id = ? AND session_id = ?`,
		id, sessionID,
	).Scan(&rec.ID, &rec.SessionID, &rec.ContentType, &rec.ByteSize, &rec.Payload, &createdAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionObject{}, ErrSessionObjectNotFound
	}
	if err != nil {
		return SessionObject{}, fmt.Errorf("get session object: %w", err)
	}
	t, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return SessionObject{}, fmt.Errorf("parse created_at for %s: %w", id, err)
	}
	rec.CreatedAt = t
	return rec, nil
}

// ListSessionObjects returns all session_objects rows for the given session,
// ordered by created_at DESC. Cross-session rows are excluded by the WHERE
// clause; no pagination in v1 (expected cardinality is low per session — card
// pipeline writes one object per structured tool result).
func (s *Store) ListSessionObjects(ctx context.Context, sessionID string) ([]SessionObject, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, content_type, byte_size, payload, created_at
		 FROM session_objects WHERE session_id = ? ORDER BY created_at DESC, id DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list session objects: %w", err)
	}
	defer closeRows(rows)

	out := make([]SessionObject, 0)
	for rows.Next() {
		var rec SessionObject
		var createdAtStr string
		if err := rows.Scan(&rec.ID, &rec.SessionID, &rec.ContentType, &rec.ByteSize, &rec.Payload, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scan session object: %w", err)
		}
		t, err := time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at for %s: %w", rec.ID, err)
		}
		rec.CreatedAt = t
		out = append(out, rec)
	}
	return out, rows.Err()
}

// EvictSessionObjects deletes all session_objects rows for the given session
// and returns the count of deleted rows. ArchiveSession inlines an equivalent
// DELETE inside its transaction so archive + eviction are atomic (D5); this
// method uses s.DB directly, so it must NOT be called from within an
// in-progress transaction — inline the DELETE via tx.Exec instead.
func (s *Store) EvictSessionObjects(ctx context.Context, sessionID string) (int, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM session_objects WHERE session_id = ?`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("evict session objects for %s: %w", sessionID, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// newSessionObjectULID generates a fresh ULID string. Matches the
// internal/tool/cache.go pattern so IDs are comparable across stores.
func newSessionObjectULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
