package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// PluginSettings holds per-plugin configuration stored in the database.
type PluginSettings struct {
	PluginID  string         `json:"plugin_id"`
	Settings  map[string]any `json:"settings"`
	Schema    []ConfigField  `json:"schema"`
	Icon      string         `json:"icon"`
	UpdatedAt string         `json:"updated_at"`
}

// ConfigField defines a single configuration field for frontend rendering.
type ConfigField struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"` // "string", "bool", "int", "select", "secret"
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"`   // for "select" type
	Component   string   `json:"component,omitempty"` // custom React component name (requires developer_mode)
}

// GetPluginSettings returns settings for a specific plugin.
func (s *Store) GetPluginSettings(ctx context.Context, pluginID string) (*PluginSettings, error) {
	var settingsJSON, schemaJSON, icon, updatedAt string
	err := s.DB.QueryRowContext(ctx,
		`SELECT settings, schema, COALESCE(icon,''), updated_at FROM plugin_settings WHERE plugin_id = ?`,
		pluginID,
	).Scan(&settingsJSON, &schemaJSON, &icon, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get plugin settings %s: %w", pluginID, err)
	}

	ps := &PluginSettings{
		PluginID:  pluginID,
		Icon:      icon,
		UpdatedAt: updatedAt,
	}
	if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
		return nil, fmt.Errorf("parse plugin settings: %w", err)
	}
	if err := json.Unmarshal([]byte(schemaJSON), &ps.Schema); err != nil {
		return nil, fmt.Errorf("parse plugin schema: %w", err)
	}
	return ps, nil
}

// UpsertPluginSettings creates or updates settings for a plugin.
func (s *Store) UpsertPluginSettings(ctx context.Context, pluginID string, settings map[string]any) error {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal plugin settings: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO plugin_settings (plugin_id, settings, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(plugin_id) DO UPDATE SET settings = ?, updated_at = ?`,
		pluginID, string(settingsJSON), now,
		string(settingsJSON), now,
	)
	if err != nil {
		return fmt.Errorf("upsert plugin settings: %w", err)
	}
	return nil
}

// UpsertPluginSchema registers a config schema for a plugin.
func (s *Store) UpsertPluginSchema(ctx context.Context, pluginID string, schema []ConfigField) error {
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal plugin schema: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO plugin_settings (plugin_id, schema, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(plugin_id) DO UPDATE SET schema = ?, updated_at = ?`,
		pluginID, string(schemaJSON), now,
		string(schemaJSON), now,
	)
	if err != nil {
		return fmt.Errorf("upsert plugin schema: %w", err)
	}
	return nil
}

// ClearPluginSchema resets the persisted config schema for a plugin to the
// empty array, leaving settings values intact. Called by the plugin host on
// UnloadPlugin so a hot-reloaded plugin starts with a fresh schema and the
// admin UI stops advertising fields from the old (now-unloaded) build. We
// don't DELETE the row because user-entered settings live in the same row
// and a reinstall should be able to re-associate them with a new schema.
func (s *Store) ClearPluginSchema(ctx context.Context, pluginID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE plugin_settings SET schema = '[]', updated_at = ? WHERE plugin_id = ?`,
		now, pluginID,
	)
	if err != nil {
		return fmt.Errorf("clear plugin schema: %w", err)
	}
	return nil
}

// ListPluginSettings returns settings for all plugins that have saved config.
func (s *Store) ListPluginSettings(ctx context.Context) ([]*PluginSettings, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT plugin_id, settings, schema, COALESCE(icon,''), updated_at FROM plugin_settings ORDER BY plugin_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list plugin settings: %w", err)
	}
	defer rows.Close()

	var results []*PluginSettings
	for rows.Next() {
		var pid, settingsJSON, schemaJSON, icon, updatedAt string
		if err := rows.Scan(&pid, &settingsJSON, &schemaJSON, &icon, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan plugin settings: %w", err)
		}
		ps := &PluginSettings{
			PluginID:  pid,
			Icon:      icon,
			UpdatedAt: updatedAt,
		}
		if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
			return nil, fmt.Errorf("parse plugin settings %s: %w", pid, err)
		}
		if err := json.Unmarshal([]byte(schemaJSON), &ps.Schema); err != nil {
			return nil, fmt.Errorf("parse plugin schema %s: %w", pid, err)
		}
		results = append(results, ps)
	}
	return results, nil
}

// UpdatePluginIcon sets the icon for a plugin.
func (s *Store) UpdatePluginIcon(ctx context.Context, pluginID, icon string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE plugin_settings SET icon = ?, updated_at = ? WHERE plugin_id = ?`,
		nullIfEmpty(icon), now, pluginID,
	)
	if err != nil {
		return fmt.Errorf("update plugin icon: %w", err)
	}
	return nil
}

// GetPluginSettingValue returns a single config value for a plugin.
// Returns empty string if not found.
func (s *Store) GetPluginSettingValue(ctx context.Context, pluginID, key string) (string, error) {
	ps, err := s.GetPluginSettings(ctx, pluginID)
	if err != nil {
		return "", err
	}
	if val, ok := ps.Settings[key]; ok {
		switch v := val.(type) {
		case string:
			return v, nil
		default:
			b, _ := json.Marshal(v)
			return string(b), nil
		}
	}
	return "", nil
}
