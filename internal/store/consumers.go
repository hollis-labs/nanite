package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Consumer represents a row in the `consumers` table: a minimal ownership/
// tenancy tag identifying which external system an agent belongs to (e.g.
// Loom owns Curator), per TASKS/phase-1/03-add-consumers-table.md and
// docs/engineering/architecture/01-agent-construction.md's "Ownership and
// instancing" section. This is deliberately not a facet catalog like
// `roles` -- just id/slug/name/created_at. See GLOSSARY.md's Consumer entry
// for the distinction from Instance (a fully separate, isolated database).
type Consumer struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

const consumerColumns = `id, slug, name, created_at`

// ListConsumers returns all consumers ordered by slug.
func (s *Store) ListConsumers(ctx context.Context) ([]Consumer, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+consumerColumns+` FROM consumers ORDER BY slug`,
	)
	if err != nil {
		return nil, fmt.Errorf("list consumers: %w", err)
	}
	defer rows.Close()

	out := make([]Consumer, 0)
	for rows.Next() {
		var c Consumer
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan consumer: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConsumer returns a consumer by ID. Returns nil, nil if not found.
func (s *Store) GetConsumer(ctx context.Context, id string) (*Consumer, error) {
	var c Consumer
	err := s.DB.QueryRowContext(ctx,
		`SELECT `+consumerColumns+` FROM consumers WHERE id = ?`, id,
	).Scan(&c.ID, &c.Slug, &c.Name, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get consumer %s: %w", id, err)
	}
	return &c, nil
}

// GetConsumerBySlug returns a consumer by slug. Returns nil, nil if not found.
func (s *Store) GetConsumerBySlug(ctx context.Context, slug string) (*Consumer, error) {
	var c Consumer
	err := s.DB.QueryRowContext(ctx,
		`SELECT `+consumerColumns+` FROM consumers WHERE slug = ?`, slug,
	).Scan(&c.ID, &c.Slug, &c.Name, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get consumer by slug %s: %w", slug, err)
	}
	return &c, nil
}

// CreateConsumer inserts a new consumer, generating an ID if none is set.
func (s *Store) CreateConsumer(ctx context.Context, c *Consumer) error {
	if c.Slug == "" {
		return fmt.Errorf("create consumer: slug is required")
	}
	if c.Name == "" {
		return fmt.Errorf("create consumer: name is required")
	}
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO consumers (`+consumerColumns+`) VALUES (?, ?, ?, ?)`,
		c.ID, c.Slug, c.Name, now,
	)
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}
	c.CreatedAt = now
	return nil
}

// UpdateConsumer updates a consumer's slug/name by ID.
func (s *Store) UpdateConsumer(ctx context.Context, c *Consumer) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE consumers SET slug = ?, name = ? WHERE id = ?`,
		c.Slug, c.Name, c.ID,
	)
	if err != nil {
		return fmt.Errorf("update consumer: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("consumer %q not found", c.ID)
	}
	return nil
}

// DeleteConsumer removes a consumer by ID. This codebase runs with
// PRAGMA foreign_keys=1 (see github.com/hollis-labs/go-sqlite/sqlitekit),
// so deleting a consumer that is still referenced by any
// agent_profiles.consumer_id row fails with a FOREIGN KEY constraint
// error -- callers must nullify those rows first (via UpdateAgent) before
// deleting the consumer they point at.
func (s *Store) DeleteConsumer(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM consumers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete consumer %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("consumer %q not found", id)
	}
	return nil
}
