package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Tether delivery states. The ledger only moves forward; a row in a terminal
// state (consumed, failed) is never re-injected.
const (
	TetherDeliveryReceived  = "received"
	TetherDeliveryAccepted  = "accepted"
	TetherDeliverySubmitted = "submitted"
	TetherDeliveryConsumed  = "consumed"
	TetherDeliveryFailed    = "failed"
)

// TetherDelivery is the idempotency record for one Tether message id.
type TetherDelivery struct {
	MessageID      string
	ActorURN       string
	SessionID      string
	ProfileID      string
	LocalMessageID string
	EnvelopeJSON   string
	State          string
	Attempts       int
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Terminal reports whether the delivery needs no further work.
func (d TetherDelivery) Terminal() bool {
	return d.State == TetherDeliveryConsumed || d.State == TetherDeliveryFailed
}

const tetherDeliveryColumns = `message_id, actor_urn, session_id, profile_id, local_message_id, envelope_json, state, attempts, last_error, created_at, updated_at`

func scanTetherDelivery(row interface{ Scan(...any) error }) (*TetherDelivery, error) {
	var d TetherDelivery
	var created, updated string
	if err := row.Scan(&d.MessageID, &d.ActorURN, &d.SessionID, &d.ProfileID, &d.LocalMessageID,
		&d.EnvelopeJSON, &d.State, &d.Attempts, &d.LastError, &created, &updated); err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &d, nil
}

// RecordTetherDelivery inserts d in state received unless a row for its
// message id already exists, and returns the row that is now authoritative
// plus whether this call created it. A second sighting of the same Tether
// message id returns created=false with the original row untouched.
func (s *Store) RecordTetherDelivery(ctx context.Context, d TetherDelivery) (*TetherDelivery, bool, error) {
	if d.MessageID == "" {
		return nil, false, errors.New("record tether delivery: empty message id")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO tether_deliveries (`+tetherDeliveryColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		d.MessageID, d.ActorURN, d.SessionID, d.ProfileID, "", d.EnvelopeJSON, TetherDeliveryReceived, 0, "", now, now)
	if err != nil {
		return nil, false, fmt.Errorf("record tether delivery %s: %w", d.MessageID, err)
	}
	n, _ := res.RowsAffected()
	got, err := s.GetTetherDelivery(ctx, d.MessageID)
	if err != nil {
		return nil, false, err
	}
	return got, n == 1, nil
}

// GetTetherDelivery returns the ledger row for a Tether message id, or
// sql.ErrNoRows wrapped when none exists.
func (s *Store) GetTetherDelivery(ctx context.Context, messageID string) (*TetherDelivery, error) {
	d, err := scanTetherDelivery(s.DB.QueryRowContext(ctx,
		`SELECT `+tetherDeliveryColumns+` FROM tether_deliveries WHERE message_id = ?`, messageID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("get tether delivery %s: %w", messageID, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("get tether delivery %s: %w", messageID, err)
	}
	return d, nil
}

// AdvanceTetherDelivery moves a delivery to state, optionally recording the
// local mailbox message id and an error. It refuses to leave a terminal state
// and returns false when the row was not changed.
func (s *Store) AdvanceTetherDelivery(ctx context.Context, messageID, state, localMessageID, lastErr string, bumpAttempt bool) (bool, error) {
	attempt := 0
	if bumpAttempt {
		attempt = 1
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE tether_deliveries
		    SET state = ?,
		        local_message_id = CASE WHEN ? != '' THEN ? ELSE local_message_id END,
		        last_error = ?,
		        attempts = attempts + ?,
		        updated_at = ?
		  WHERE message_id = ? AND state NOT IN ('consumed','failed')`,
		state, localMessageID, localMessageID, lastErr, attempt,
		time.Now().UTC().Format(time.RFC3339Nano), messageID)
	if err != nil {
		return false, fmt.Errorf("advance tether delivery %s: %w", messageID, err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ListOpenTetherDeliveries returns deliveries not yet in a terminal state,
// oldest first. The bridge resumes these after a restart.
func (s *Store) ListOpenTetherDeliveries(ctx context.Context, limit int) ([]TetherDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+tetherDeliveryColumns+` FROM tether_deliveries
		  WHERE state NOT IN ('consumed','failed') ORDER BY created_at LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list open tether deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TetherDelivery
	for rows.Next() {
		d, err := scanTetherDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
