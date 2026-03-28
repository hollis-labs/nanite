package store

import (
	"fmt"
	"time"
)

// ProviderConfig represents a provider record.
type ProviderConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url"`
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
func (s *Store) ListProviders() ([]ProviderConfig, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, provider_type, COALESCE(base_url,''), is_enabled,
		        settings, created_at, updated_at
		 FROM providers ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	out := make([]ProviderConfig, 0)
	for rows.Next() {
		var p ProviderConfig
		if err := rows.Scan(
			&p.ID, &p.Name, &p.ProviderType, &p.BaseURL, &p.IsEnabled,
			&p.Settings, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListModels returns all models, joined with provider_type.
func (s *Store) ListModels() ([]Model, error) {
	rows, err := s.DB.Query(
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
	defer rows.Close()

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
	BaseURL   *string `json:"base_url,omitempty"`
	Settings  *string `json:"settings,omitempty"`
}

// UpdateProvider updates mutable fields on a provider.
func (s *Store) UpdateProvider(id string, u ProviderUpdate) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if u.IsEnabled != nil {
		if _, err := s.DB.Exec(
			`UPDATE providers SET is_enabled = ?, updated_at = ? WHERE id = ?`,
			*u.IsEnabled, now, id,
		); err != nil {
			return fmt.Errorf("update provider is_enabled: %w", err)
		}
	}
	if u.BaseURL != nil {
		if _, err := s.DB.Exec(
			`UPDATE providers SET base_url = ?, updated_at = ? WHERE id = ?`,
			*u.BaseURL, now, id,
		); err != nil {
			return fmt.Errorf("update provider base_url: %w", err)
		}
	}
	if u.Settings != nil {
		if _, err := s.DB.Exec(
			`UPDATE providers SET settings = ?, updated_at = ? WHERE id = ?`,
			*u.Settings, now, id,
		); err != nil {
			return fmt.Errorf("update provider settings: %w", err)
		}
	}
	return nil
}

// SetProviderAPIKey stores an API key for a provider. Write-only — never returned in queries.
func (s *Store) SetProviderAPIKey(id, apiKey string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE providers SET api_key = ?, updated_at = ? WHERE id = ?`,
		apiKey, now, id,
	)
	if err != nil {
		return fmt.Errorf("set provider api key: %w", err)
	}
	return nil
}

// HasProviderAPIKey returns true if a provider has a non-empty API key stored.
func (s *Store) HasProviderAPIKey(id string) (bool, error) {
	var key string
	err := s.DB.QueryRow(`SELECT COALESCE(api_key, '') FROM providers WHERE id = ?`, id).Scan(&key)
	if err != nil {
		return false, fmt.Errorf("check provider api key: %w", err)
	}
	return key != "", nil
}

// GetProvider returns a single provider by ID.
func (s *Store) GetProvider(id string) (*ProviderConfig, error) {
	var p ProviderConfig
	err := s.DB.QueryRow(
		`SELECT id, name, provider_type, COALESCE(base_url,''), is_enabled,
		        settings, created_at, updated_at
		 FROM providers WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.ProviderType, &p.BaseURL, &p.IsEnabled,
		&p.Settings, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get provider %s: %w", id, err)
	}
	return &p, nil
}
