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

// tetherTimeLayout is fixed-width so the stored text sorts and compares as
// time (RFC3339Nano trims zeros and does not).
const tetherTimeLayout = "2006-01-02T15:04:05.000000000Z"

func tetherTime(t time.Time) string { return t.UTC().Format(tetherTimeLayout) }

// TetherDelivery is the idempotency record for one Tether message id.
type TetherDelivery struct {
	MessageID      string
	ActorURN       string
	LocalMessageID string
	EnvelopeJSON   string
	State          string
	Attempts       int
	LastError      string
	// NextAttemptAt defers the row; the zero time means due now.
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Terminal reports whether the delivery needs no further work.
func (d TetherDelivery) Terminal() bool {
	return d.State == TetherDeliveryConsumed || d.State == TetherDeliveryFailed
}

const tetherDeliveryColumns = `message_id, actor_urn, local_message_id, envelope_json, state, attempts, last_error, next_attempt_at, created_at, updated_at`

func scanTetherDelivery(row interface{ Scan(...any) error }) (*TetherDelivery, error) {
	var d TetherDelivery
	var next, created, updated string
	if err := row.Scan(&d.MessageID, &d.ActorURN, &d.LocalMessageID, &d.EnvelopeJSON,
		&d.State, &d.Attempts, &d.LastError, &next, &created, &updated); err != nil {
		return nil, err
	}
	d.NextAttemptAt, _ = time.Parse(tetherTimeLayout, next)
	d.CreatedAt, _ = time.Parse(tetherTimeLayout, created)
	d.UpdatedAt, _ = time.Parse(tetherTimeLayout, updated)
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
	now := tetherTime(time.Now())
	res, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO tether_deliveries (message_id, actor_urn, local_message_id, envelope_json, state, attempts, last_error, next_attempt_at, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		d.MessageID, d.ActorURN, "", d.EnvelopeJSON, TetherDeliveryReceived, 0, "", "", now, now)
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

// AdvanceTetherDelivery moves a delivery forward to state, optionally
// recording the local mailbox message id. It never leaves a terminal state and
// never moves a row backward: an attempt to do either changes nothing and
// returns false. Reaching a terminal state clears the stored envelope, so a
// finished delivery does not keep the message body.
func (s *Store) AdvanceTetherDelivery(ctx context.Context, messageID, state, localMessageID, lastErr string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE tether_deliveries
		    SET state = ?,
		        local_message_id = CASE WHEN ? != '' THEN ? ELSE local_message_id END,
		        last_error = ?,
		        next_attempt_at = '',
		        envelope_json = CASE WHEN ? IN ('consumed','failed') THEN '' ELSE envelope_json END,
		        updated_at = ?
		  WHERE message_id = ?
		    AND state NOT IN ('consumed','failed')
		    AND (CASE state WHEN 'received' THEN 0 WHEN 'accepted' THEN 1 WHEN 'submitted' THEN 2 END)
		      <= (CASE ? WHEN 'received' THEN 0 WHEN 'accepted' THEN 1 WHEN 'submitted' THEN 2 ELSE 3 END)`,
		state, localMessageID, localMessageID, lastErr, state, tetherTime(time.Now()), messageID, state)
	if err != nil {
		return false, fmt.Errorf("advance tether delivery %s: %w", messageID, err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeferTetherDelivery postpones a row until at, recording why. bumpAttempt
// counts the deferral as a failed attempt; deferrals that are not failures
// (session busy, actor unbound) pass false so they never exhaust a row.
// It returns the attempt count after the call.
func (s *Store) DeferTetherDelivery(ctx context.Context, messageID string, at time.Time, lastErr string, bumpAttempt bool) (int, error) {
	bump := 0
	if bumpAttempt {
		bump = 1
	}
	var attempts int
	err := s.DB.QueryRowContext(ctx,
		`UPDATE tether_deliveries
		    SET next_attempt_at = ?, last_error = ?, attempts = attempts + ?, updated_at = ?
		  WHERE message_id = ? AND state NOT IN ('consumed','failed')
		  RETURNING attempts`,
		tetherTime(at), lastErr, bump, tetherTime(time.Now()), messageID).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("defer tether delivery %s: %w", messageID, err)
	}
	return attempts, nil
}

// ListOpenTetherDeliveries returns deliveries that are not terminal and are
// due, least recently scheduled first. A deferred row sorts behind everything
// else, so a stuck row cannot starve newer ones.
func (s *Store) ListOpenTetherDeliveries(ctx context.Context, limit int) ([]TetherDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+tetherDeliveryColumns+` FROM tether_deliveries
		  WHERE state NOT IN ('consumed','failed') AND next_attempt_at <= ?
		  ORDER BY next_attempt_at, created_at LIMIT ?`, tetherTime(time.Now()), limit)
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
