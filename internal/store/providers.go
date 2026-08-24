package store

import (
	"context"
	"fmt"
	"time"
)

// ProviderConfig represents a provider record.
type ProviderConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProviderType string `json:"provider_type"`
	IsEnabled    bool   `json:"is_enabled"`
	Settings     string `json:"settings"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Model represents a model record.
type Model struct {
	ID             string `json:"id"`
	ProviderID     string `json:"provider_id"`
	ModelID        string `json:"model_id"`
	DisplayName    string `json:"display_name"`
	ContextWindow  int    `json:"context_window"`
	MaxOutput      int    `json:"max_output"`
	SupportsTools  bool   `json:"supports_tools"`
	SupportsVision bool   `json:"supports_vision"`
	IsEnabled      bool   `json:"is_enabled"`
	Pricing        string `json:"pricing"`
	SortOrder      int    `json:"sort_order"`
	// Joined field from provider.
	ProviderType string `json:"provider_type"`
}

// ListProviders returns all providers.
func (s *Store) ListProviders(ctx context.Context) ([]ProviderConfig, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, provider_type, is_enabled,
		        settings, created_at, updated_at
		 FROM providers ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer closeRows(rows)

	out := make([]ProviderConfig, 0)
	for rows.Next() {
		var p ProviderConfig
		if err := rows.Scan(
			&p.ID, &p.Name, &p.ProviderType, &p.IsEnabled,
			&p.Settings, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListModels returns all models, joined with provider_type.
func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT m.id, m.provider_id, m.model_id, m.display_name,
		        COALESCE(m.context_window, 0), COALESCE(m.max_output, 0),
		        m.supports_tools, m.supports_vision, m.is_enabled,
		        m.pricing, m.sort_order, p.provider_type
		 FROM models m
		 JOIN providers p ON p.id = m.provider_id
		 ORDER BY m.sort_order, m.display_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer closeRows(rows)

	out := make([]Model, 0)
	for rows.Next() {
		var m Model
		if err := rows.Scan(
			&m.ID, &m.ProviderID, &m.ModelID, &m.DisplayName,
			&m.ContextWindow, &m.MaxOutput,
			&m.SupportsTools, &m.SupportsVision, &m.IsEnabled,
			&m.Pricing, &m.SortOrder, &m.ProviderType,
		); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ProviderUpdate holds mutable fields for a provider. Nil fields are not updated.
type ProviderUpdate struct {
	IsEnabled *bool   `json:"is_enabled,omitempty"`
	Settings  *string `json:"settings,omitempty"`
}

// UpdateProvider updates mutable fields on a provider.
func (s *Store) UpdateProvider(ctx context.Context, id string, u ProviderUpdate) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if u.IsEnabled != nil {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE providers SET is_enabled = ?, updated_at = ? WHERE id = ?`,
			*u.IsEnabled, now, id,
		); err != nil {
			return fmt.Errorf("update provider is_enabled: %w", err)
		}
	}
	if u.Settings != nil {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE providers SET settings = ?, updated_at = ? WHERE id = ?`,
			*u.Settings, now, id,
		); err != nil {
			return fmt.Errorf("update provider settings: %w", err)
		}
	}
	return nil
}

// GetProvider returns a single provider by ID.
func (s *Store) GetProvider(ctx context.Context, id string) (*ProviderConfig, error) {
	var p ProviderConfig
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, provider_type, is_enabled,
		        settings, created_at, updated_at
		 FROM providers WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.ProviderType, &p.IsEnabled,
		&p.Settings, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get provider %s: %w", id, err)
	}
	return &p, nil
}
