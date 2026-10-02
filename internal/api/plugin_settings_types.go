package api

import (
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// PluginSettingsView is the plugin configuration wire contract.
type PluginSettingsView struct {
	PluginID  string                  `json:"plugin_id"`
	Settings  map[string]any          `json:"settings"`
	Schema    []PluginConfigFieldView `json:"schema"`
	Icon      string                  `json:"icon"`
	UpdatedAt string                  `json:"updated_at"`
}
type PluginConfigFieldView struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"`
	Component   string   `json:"component,omitempty"`
}

func pluginSettingsView(row *store.PluginSettings) *PluginSettingsView {
	if row == nil {
		return nil
	}
	var schema []PluginConfigFieldView
	if row.Schema != nil {
		schema = make([]PluginConfigFieldView, len(row.Schema))
		for i, f := range row.Schema {
			schema[i] = PluginConfigFieldView{Key: f.Key, Type: f.Type, Label: f.Label, Description: f.Description, Default: f.Default, Required: f.Required, Options: f.Options, Component: f.Component}
		}
	}
	return &PluginSettingsView{PluginID: row.PluginID, Settings: row.Settings, Schema: schema, Icon: row.Icon, UpdatedAt: row.UpdatedAt}
}
func pluginSettingsViews(rows []*store.PluginSettings) []*PluginSettingsView {
	if rows == nil {
		return nil
	}
	views := make([]*PluginSettingsView, len(rows))
	for i, row := range rows {
		views[i] = pluginSettingsView(row)
	}
	return views
}

type CatalogSourceView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Type      string    `json:"type"`
	Enabled   bool      `json:"enabled"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func catalogSourceView(row *store.CatalogSource) *CatalogSourceView {
	if row == nil {
		return nil
	}
	return &CatalogSourceView{ID: row.ID, Name: row.Name, URL: row.URL, Type: row.Type, Enabled: row.Enabled, Priority: row.Priority, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func catalogSourceViews(rows []store.CatalogSource) []*CatalogSourceView {
	if rows == nil {
		return nil
	}
	views := make([]*CatalogSourceView, len(rows))
	for i := range rows {
		views[i] = catalogSourceView(&rows[i])
	}
	return views
}
