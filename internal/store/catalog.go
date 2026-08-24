package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CatalogSource represents a remote plugin catalog registry.
type CatalogSource struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Type      string    `json:"type"` // "official" or "custom"
	Enabled   bool      `json:"enabled"`
	Priority  int       `json:"priority"`   // higher = wins on conflict
	PublicKey string    `json:"public_key"` // hex-encoded Ed25519 public key for signature verification
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListCatalogSources returns all catalog sources ordered by priority descending.
func (s *Store) ListCatalogSources(ctx context.Context) ([]CatalogSource, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, url, type, enabled, priority, public_key, created_at, updated_at
		FROM catalog_sources
		ORDER BY priority DESC, name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list catalog sources: %w", err)
	}
	defer closeRows(rows)

	sources := make([]CatalogSource, 0)
	for rows.Next() {
		var cs CatalogSource
		var enabled int
		if err := rows.Scan(&cs.ID, &cs.Name, &cs.URL, &cs.Type, &enabled, &cs.Priority, &cs.PublicKey, &cs.CreatedAt, &cs.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan catalog source: %w", err)
		}
		cs.Enabled = enabled == 1
		sources = append(sources, cs)
	}
	return sources, rows.Err()
}

// CreateCatalogSource adds a new catalog source.
func (s *Store) CreateCatalogSource(ctx context.Context, name, url, sourceType string, priority int) (*CatalogSource, error) {
	id := uuid.NewString()
	now := time.Now().UTC()

	if sourceType == "" {
		sourceType = "custom"
	}

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO catalog_sources (id, name, url, type, enabled, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?)
	`, id, name, url, sourceType, priority, now, now)
	if err != nil {
		return nil, fmt.Errorf("create catalog source: %w", err)
	}

	return &CatalogSource{
		ID:        id,
		Name:      name,
		URL:       url,
		Type:      sourceType,
		Enabled:   true,
		Priority:  priority,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// UpdateCatalogSource updates mutable fields of a catalog source.
func (s *Store) UpdateCatalogSource(ctx context.Context, id, name, url string, enabled bool, priority int) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := s.DB.ExecContext(ctx, `
		UPDATE catalog_sources SET name=?, url=?, enabled=?, priority=?, updated_at=datetime('now')
		WHERE id=?
	`, name, url, enabledInt, priority, id)
	if err != nil {
		return fmt.Errorf("update catalog source: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("catalog source %q not found", id)
	}
	return nil
}

// SetCatalogSourcePublicKey sets the trusted public key for a catalog source.
func (s *Store) SetCatalogSourcePublicKey(ctx context.Context, id, publicKey string) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE catalog_sources SET public_key=?, updated_at=datetime('now') WHERE id=?
	`, publicKey, id)
	if err != nil {
		return fmt.Errorf("set public key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("catalog source %q not found", id)
	}
	return nil
}

// DeleteCatalogSource removes a catalog source.
func (s *Store) DeleteCatalogSource(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM catalog_sources WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete catalog source: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("catalog source %q not found", id)
	}
	return nil
}

// GetCatalogSource returns a single catalog source by ID.
func (s *Store) GetCatalogSource(ctx context.Context, id string) (*CatalogSource, error) {
	var cs CatalogSource
	var enabled int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, name, url, type, enabled, priority, public_key, created_at, updated_at
		FROM catalog_sources WHERE id=?
	`, id).Scan(&cs.ID, &cs.Name, &cs.URL, &cs.Type, &enabled, &cs.Priority, &cs.PublicKey, &cs.CreatedAt, &cs.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get catalog source %q: %w", id, err)
	}
	cs.Enabled = enabled == 1
	return &cs, nil
}
