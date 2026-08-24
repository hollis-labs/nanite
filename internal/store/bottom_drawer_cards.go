package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BottomDrawerPinCap is the maximum number of pinned cards per session for
// the bottom chat drawer (C1, CW-20260428-0012). The 11th pin attempt is
// rejected at the API layer with ErrBottomDrawerPinCapExceeded so the FE
// can surface a "10-tab limit; unpin one first" toast.
const BottomDrawerPinCap = 10

// ErrBottomDrawerPinCapExceeded is returned by PinBottomDrawerCard when the
// session already has BottomDrawerPinCap pins.
var ErrBottomDrawerPinCapExceeded = errors.New("bottom drawer pin cap exceeded")

// BottomDrawerPinnedCard is one persisted drawer-card pin (C1). It is NOT
// the same as the J11 pinned_content (slot-context pins) — see store/pinned_content.go.
type BottomDrawerPinnedCard struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	CardType   string `json:"card_type"`
	ContentRef string `json:"content_ref"`
	Title      string `json:"title"`
	Payload    string `json:"payload"` // JSON blob (envelope snapshot or other type-specific data)
	Position   int    `json:"position"`
	CreatedAt  string `json:"created_at"`
}

// ListBottomDrawerPinnedCards returns the session's pinned drawer cards in
// position-asc, created-asc order.
func (s *Store) ListBottomDrawerPinnedCards(ctx context.Context, sessionID string) ([]BottomDrawerPinnedCard, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, card_type, content_ref, title, payload, position, created_at
		   FROM bottom_drawer_pinned_cards
		  WHERE session_id = ?
		  ORDER BY position ASC, created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list bottom drawer pinned cards: %w", err)
	}
	defer closeRows(rows)

	out := make([]BottomDrawerPinnedCard, 0)
	for rows.Next() {
		var c BottomDrawerPinnedCard
		if err := rows.Scan(&c.ID, &c.SessionID, &c.CardType, &c.ContentRef,
			&c.Title, &c.Payload, &c.Position, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan bottom drawer pinned card: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountBottomDrawerPinnedCards returns the number of pins for a session.
// Used by the cap-enforcement gate in PinBottomDrawerCard.
func (s *Store) CountBottomDrawerPinnedCards(ctx context.Context, sessionID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bottom_drawer_pinned_cards WHERE session_id = ?`,
		sessionID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count bottom drawer pinned cards: %w", err)
	}
	return n, nil
}

// PinBottomDrawerCard inserts a pinned drawer card. Enforces BottomDrawerPinCap
// atomically — the count check and INSERT happen as a single SQL statement
// (INSERT…SELECT…WHERE) so concurrent pin requests cannot both pass the gate
// and exceed the cap. Position is computed inside the SQL as the current
// COUNT(*), giving a stable append-to-end ordering.
//
// ID is generated when empty. Returns ErrBottomDrawerPinCapExceeded when the
// session already has BottomDrawerPinCap pins (RowsAffected==0 path).
func (s *Store) PinBottomDrawerCard(ctx context.Context, c *BottomDrawerPinnedCard) error {
	if c == nil {
		return errors.New("nil pinned card")
	}
	if c.SessionID == "" {
		return errors.New("session_id required")
	}
	if c.CardType == "" {
		return errors.New("card_type required")
	}

	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.Payload == "" {
		c.Payload = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.CreatedAt = now

	result, err := s.DB.ExecContext(ctx,
		`INSERT INTO bottom_drawer_pinned_cards
		   (id, session_id, card_type, content_ref, title, payload, position, created_at)
		 SELECT ?, ?, ?, ?, ?, ?,
		        (SELECT COUNT(*) FROM bottom_drawer_pinned_cards WHERE session_id = ?),
		        ?
		  WHERE (SELECT COUNT(*) FROM bottom_drawer_pinned_cards WHERE session_id = ?) < ?`,
		c.ID, c.SessionID, c.CardType, c.ContentRef, c.Title, c.Payload,
		c.SessionID, now, c.SessionID, BottomDrawerPinCap,
	)
	if err != nil {
		return fmt.Errorf("pin bottom drawer card: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pin bottom drawer card: rows affected: %w", err)
	}
	if n == 0 {
		return ErrBottomDrawerPinCapExceeded
	}

	// Read back the in-SQL position so the caller's struct reflects the
	// stored value (used by API responses + tests).
	if err := s.DB.QueryRowContext(ctx,
		`SELECT position FROM bottom_drawer_pinned_cards WHERE id = ?`,
		c.ID,
	).Scan(&c.Position); err != nil {
		return fmt.Errorf("pin bottom drawer card: read position: %w", err)
	}
	return nil
}

// UnpinBottomDrawerCard removes a pinned card by ID. No-op if missing.
func (s *Store) UnpinBottomDrawerCard(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM bottom_drawer_pinned_cards WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("unpin bottom drawer card %s: %w", id, err)
	}
	return nil
}

// GetBottomDrawerPinnedCard fetches a single pinned card by ID.
func (s *Store) GetBottomDrawerPinnedCard(ctx context.Context, id string) (*BottomDrawerPinnedCard, error) {
	var c BottomDrawerPinnedCard
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, card_type, content_ref, title, payload, position, created_at
		   FROM bottom_drawer_pinned_cards WHERE id = ?`, id,
	).Scan(&c.ID, &c.SessionID, &c.CardType, &c.ContentRef,
		&c.Title, &c.Payload, &c.Position, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get bottom drawer pinned card %s: %w", id, err)
	}
	return &c, nil
}
