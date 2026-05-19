package gomsg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	messaging "github.com/hollis-labs/go-messaging"
)

// timeFormat is RFC3339 with forced 9-digit fractional seconds. The
// fixed width is load-bearing: created_at is stored as TEXT and ordered
// lexically, so a variable-width format (time.RFC3339Nano trims trailing
// zeros) would break chronological ordering for sub-second bursts.
const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// envelopeColumns is the canonical SELECT list for the messaging_envelopes
// table — kept in one place so scanEnvelope stays in lockstep with reads.
const envelopeColumns = `id, kind, channel, from_urn, to_urn, thread_id,
	in_reply_to, payload, content_type, metadata, created_at,
	delivered_at, consumed_at`

// SQLStore is a durable, SQLite-backed messaging.Store. It implements
// the go-messaging Store contract identically to the in-memory reference
// (memstore) — conformance is verified by messagingtest.RunContract in
// sqlstore_test.go.
//
// Backing table: messaging_envelopes (migration 064). Subscribe is an
// in-process, best-effort live fan-out; Inbox is the durable
// exactly-once-per-recipient delivery path.
type SQLStore struct {
	db *sql.DB

	mu   sync.Mutex
	subs []*subscriber
}

// Compile-time assertion: SQLStore satisfies the shared contract.
var _ messaging.Store = (*SQLStore)(nil)

type subscriber struct {
	to     messaging.Address
	filter messaging.Filter
	ch     chan messaging.Envelope
}

// NewSQLStore constructs a SQLStore over db. The messaging_envelopes
// table (migration 064) must already exist. The caller owns the DB
// handle's lifecycle.
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

// Send persists an envelope. Store assigns a fresh UUIDv7 ID and
// CreatedAt; caller values on those fields are overwritten. DeliveredAt
// and ConsumedAt must be nil on input — otherwise ErrPresetLifecycle.
func (s *SQLStore) Send(ctx context.Context, env messaging.Envelope) (messaging.Envelope, error) {
	if env.DeliveredAt != nil || env.ConsumedAt != nil {
		return messaging.Envelope{}, messaging.ErrPresetLifecycle
	}
	id, err := uuid.NewV7()
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("gomsg: uuid v7: %w", err)
	}
	env.ID = id.String()
	env.CreatedAt = time.Now().UTC()
	env.DeliveredAt = nil
	env.ConsumedAt = nil

	metaJSON := "{}"
	if len(env.Metadata) > 0 {
		b, mErr := json.Marshal(env.Metadata)
		if mErr != nil {
			return messaging.Envelope{}, fmt.Errorf("gomsg: marshal metadata: %w", mErr)
		}
		metaJSON = string(b)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO messaging_envelopes
		   (id, kind, channel, from_urn, to_urn, thread_id, in_reply_to,
		    payload, content_type, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		env.ID, string(env.Kind), string(env.Channel),
		env.From.URN(), env.To.URN(), env.ThreadID, env.InReplyTo,
		string(env.Payload), env.ContentType, metaJSON,
		env.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("gomsg: send: %w", err)
	}

	s.fanOut(env)
	return env, nil
}

// Get retrieves a single envelope by ID. Returns ErrNotFound if absent.
func (s *SQLStore) Get(ctx context.Context, id string) (messaging.Envelope, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+envelopeColumns+` FROM messaging_envelopes WHERE id = ?`, id)
	env, err := scanEnvelope(row)
	if errors.Is(err, sql.ErrNoRows) {
		return messaging.Envelope{}, messaging.ErrNotFound
	}
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("gomsg: get: %w", err)
	}
	return env, nil
}

// Inbox returns undelivered, non-canceled envelopes for `to`,
// chronologically by CreatedAt (UUIDv7 ID tie-break), and atomically
// marks them delivered for this recipient. A delivered envelope never
// reappears in a future Inbox call. The whole select-then-mark runs in
// one transaction so concurrent Inbox calls cannot double-deliver.
func (s *SQLStore) Inbox(ctx context.Context, to messaging.Address, f messaging.Filter) ([]messaging.Envelope, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("gomsg: inbox tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	where := []string{"to_urn = ?", "delivered_at IS NULL", "canceled = 0"}
	args := []any{to.URN()}
	where, args = applyFilter(where, args, f)

	rows, err := tx.QueryContext(ctx, buildSelect(where, f.Limit), args...)
	if err != nil {
		return nil, fmt.Errorf("gomsg: inbox query: %w", err)
	}
	out := make([]messaging.Envelope, 0)
	ids := make([]string, 0)
	for rows.Next() {
		env, sErr := scanEnvelope(rows)
		if sErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("gomsg: inbox scan: %w", sErr)
		}
		out = append(out, env)
		ids = append(ids, env.ID)
	}
	if cErr := rows.Err(); cErr != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("gomsg: inbox rows: %w", cErr)
	}
	_ = rows.Close()

	if len(ids) == 0 {
		return out, tx.Commit()
	}

	now := time.Now().UTC()
	markArgs := make([]any, 0, len(ids)+1)
	markArgs = append(markArgs, now.Format(timeFormat))
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		markArgs = append(markArgs, id)
	}
	// markQ is composed only from package-constant text and ? placeholders.
	//nolint:gosec // no user input in the SQL string; values are bound params
	markQ := `UPDATE messaging_envelopes SET delivered_at = ? WHERE id IN (` +
		strings.Join(placeholders, ",") + `)`
	if _, err = tx.ExecContext(ctx, markQ, markArgs...); err != nil {
		return nil, fmt.Errorf("gomsg: inbox mark delivered: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("gomsg: inbox commit: %w", err)
	}

	for i := range out {
		dt := now
		out[i].DeliveredAt = &dt
	}
	return out, nil
}

// Thread returns envelopes sharing a ThreadID, chronological order.
// Read-only — no delivery side effects.
func (s *SQLStore) Thread(ctx context.Context, threadID string, f messaging.Filter) ([]messaging.Envelope, error) {
	where := []string{"thread_id = ?"}
	args := []any{threadID}
	where, args = applyFilter(where, args, f)

	rows, err := s.db.QueryContext(ctx, buildSelect(where, f.Limit), args...)
	if err != nil {
		return nil, fmt.Errorf("gomsg: thread query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]messaging.Envelope, 0)
	for rows.Next() {
		env, sErr := scanEnvelope(rows)
		if sErr != nil {
			return nil, fmt.Errorf("gomsg: thread scan: %w", sErr)
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

// Consume advances ConsumedAt for the envelope. Idempotent — a repeat
// call keeps the original timestamp. Returns ErrNotFound for a missing
// id. The recipient argument is part of the contract signature; this
// single-recipient store records consumption on the envelope row.
func (s *SQLStore) Consume(ctx context.Context, id string, _ messaging.Address) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE messaging_envelopes SET consumed_at = COALESCE(consumed_at, ?) WHERE id = ?`,
		time.Now().UTC().Format(timeFormat), id)
	if err != nil {
		return fmt.Errorf("gomsg: consume: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return messaging.ErrNotFound
	}
	return nil
}

// Cancel marks an envelope dead so it is excluded from Inbox/Subscribe.
// Idempotent. Returns ErrNotFound for a missing id.
func (s *SQLStore) Cancel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE messaging_envelopes SET canceled = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("gomsg: cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return messaging.ErrNotFound
	}
	return nil
}

// Subscribe streams envelopes created after subscription time, addressed
// to `to` and matching the filter, until ctx is canceled (at which point
// the channel closes). No historical replay — use Inbox for that.
func (s *SQLStore) Subscribe(ctx context.Context, to messaging.Address, f messaging.Filter) (<-chan messaging.Envelope, error) {
	sub := &subscriber{
		to:     to,
		filter: f,
		ch:     make(chan messaging.Envelope, 16),
	}
	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, sv := range s.subs {
			if sv == sub {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				break
			}
		}
		close(sub.ch)
	}()

	return sub.ch, nil
}

// fanOut delivers a freshly-sent envelope to matching live subscribers.
// The whole loop holds s.mu so it can never race the Subscribe janitor's
// close(sub.ch); the send is non-blocking (buffered channel + default),
// so holding the lock briefly is cheap. A full buffer drops for that
// subscriber — Inbox is the durable path, Subscribe is a live hint.
func (s *SQLStore) fanOut(env messaging.Envelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs {
		if !sub.to.IsZero() && sub.to != env.To {
			continue
		}
		if !sub.filter.Matches(env) {
			continue
		}
		select {
		case sub.ch <- env:
		default:
		}
	}
}

// buildSelect assembles a chronological SELECT over messaging_envelopes.
// The query is composed only from package-constant text and the caller's
// pre-built WHERE fragments (themselves constant text + ? placeholders) —
// no user input reaches the SQL string, so gosec's heuristic G202
// concatenation warning is suppressed.
func buildSelect(where []string, limit int) string {
	//nolint:gosec // assembled from package constants + ? placeholders only
	q := `SELECT ` + envelopeColumns + ` FROM messaging_envelopes WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY created_at ASC, id ASC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	return q
}

// applyFilter appends WHERE clauses + args for a messaging.Filter.
// Within a slice values are OR-combined (IN); across fields, AND.
func applyFilter(where []string, args []any, f messaging.Filter) ([]string, []any) {
	if len(f.Kind) > 0 {
		ph := make([]string, len(f.Kind))
		for i, k := range f.Kind {
			ph[i] = "?"
			args = append(args, string(k))
		}
		where = append(where, "kind IN ("+strings.Join(ph, ",")+")")
	}
	if len(f.Channel) > 0 {
		ph := make([]string, len(f.Channel))
		for i, c := range f.Channel {
			ph[i] = "?"
			args = append(args, string(c))
		}
		where = append(where, "channel IN ("+strings.Join(ph, ",")+")")
	}
	if f.ThreadID != "" {
		where = append(where, "thread_id = ?")
		args = append(args, f.ThreadID)
	}
	return where, args
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanEnvelope reads one messaging_envelopes row into a messaging.Envelope.
func scanEnvelope(sc rowScanner) (messaging.Envelope, error) {
	var (
		env         messaging.Envelope
		kind        string
		channel     string
		fromURN     string
		toURN       string
		payload     string
		contentType string
		metadata    string
		createdAt   string
		deliveredAt sql.NullString
		consumedAt  sql.NullString
	)
	if err := sc.Scan(&env.ID, &kind, &channel, &fromURN, &toURN,
		&env.ThreadID, &env.InReplyTo, &payload, &contentType, &metadata,
		&createdAt, &deliveredAt, &consumedAt); err != nil {
		return messaging.Envelope{}, err
	}

	env.Kind = messaging.Kind(kind)
	env.Channel = messaging.Channel(channel)
	env.ContentType = contentType

	from, err := messaging.ParseURN(fromURN)
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("gomsg: parse from_urn %q: %w", fromURN, err)
	}
	env.From = from
	to, err := messaging.ParseURN(toURN)
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("gomsg: parse to_urn %q: %w", toURN, err)
	}
	env.To = to

	if payload != "" {
		env.Payload = json.RawMessage(payload)
	}
	if metadata != "" && metadata != "{}" {
		m := make(map[string]string)
		if uErr := json.Unmarshal([]byte(metadata), &m); uErr != nil {
			return messaging.Envelope{}, fmt.Errorf("gomsg: unmarshal metadata: %w", uErr)
		}
		env.Metadata = m
	}

	t, err := time.Parse(timeFormat, createdAt)
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("gomsg: parse created_at %q: %w", createdAt, err)
	}
	env.CreatedAt = t

	if deliveredAt.Valid {
		if dt, pErr := time.Parse(timeFormat, deliveredAt.String); pErr == nil {
			env.DeliveredAt = &dt
		}
	}
	if consumedAt.Valid {
		if ct, pErr := time.Parse(timeFormat, consumedAt.String); pErr == nil {
			env.ConsumedAt = &ct
		}
	}
	return env, nil
}
