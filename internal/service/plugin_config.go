package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/store"
)

// PluginConfigStore is the persistence surface for per-plugin configuration.
type PluginConfigStore interface {
	GetPluginSettings(context.Context, string) (*store.PluginSettings, error)
	UpsertPluginSettings(context.Context, string, map[string]any) error
	ListPluginSettings(context.Context) ([]*store.PluginSettings, error)
}

var ErrPluginSecretWrite = errors.New("failed to store secret in keychain")
var ErrPluginConfigWrite = errors.New("failed to save plugin config")

// PluginConfigService owns merge, keychain routing, masking and change events.
// Schema-declared secrets are routed to the keychain; change events carry keys.
type PluginConfigService struct {
	store     PluginConfigStore
	hasSecret func(string) bool
	setSecret func(string, string) error
	events    interface{ EmitConfigChanged(string, string, string) }
}

func NewPluginConfigService(st PluginConfigStore, host *plugin.Host) *PluginConfigService {
	s := &PluginConfigService{store: st, hasSecret: secrets.Has, setSecret: secrets.Set}
	if host != nil {
		s.events = host
	}
	return s
}
func pluginSecretKeyName(pluginID, fieldKey string) string {
	return "plugin:" + pluginID + ":" + fieldKey
}
func secretFieldKeys(schema []store.ConfigField) map[string]bool {
	keys := make(map[string]bool)
	for _, f := range schema {
		if f.Type == "secret" {
			keys[f.Key] = true
		}
	}
	return keys
}
func (s *PluginConfigService) mask(row *store.PluginSettings) {
	for key := range secretFieldKeys(row.Schema) {
		if s.hasSecret(pluginSecretKeyName(row.PluginID, key)) {
			row.Settings[key] = "********"
		} else {
			delete(row.Settings, key)
		}
	}
}
func (s *PluginConfigService) Get(ctx context.Context, id string) (*store.PluginSettings, error) {
	row, err := s.store.GetPluginSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	s.mask(row)
	return row, nil
}

// PluginConfigUpdate retains the existing fallback response when the write
// succeeds but the post-write read fails.
type PluginConfigUpdate struct {
	Settings *store.PluginSettings
	Fallback map[string]any
}

func (s *PluginConfigService) Update(ctx context.Context, id string, incoming map[string]any) (*PluginConfigUpdate, error) {
	existing, err := s.store.GetPluginSettings(ctx, id)
	keys := make(map[string]bool)
	if err == nil && existing != nil {
		keys = secretFieldKeys(existing.Schema)
		for k, v := range incoming {
			existing.Settings[k] = v
		}
		incoming = existing.Settings
	}
	dbSettings := make(map[string]any)
	for k, v := range incoming {
		if keys[k] {
			val, _ := v.(string)
			if val != "" && val != "********" {
				if secretErr := s.setSecret(pluginSecretKeyName(id, k), val); secretErr != nil {
					return nil, ErrPluginSecretWrite
				}
			}
		} else {
			dbSettings[k] = v
		}
	}
	if writeErr := s.store.UpsertPluginSettings(ctx, id, dbSettings); writeErr != nil {
		return nil, ErrPluginConfigWrite
	}
	if s.events != nil {
		for key := range incoming {
			k := key
			safego.Go(ctx, "api.plugin_config.emit.config-changed", func() { s.events.EmitConfigChanged(id, k, "") })
		}
	}
	updated, readErr := s.store.GetPluginSettings(ctx, id)
	if readErr != nil {
		return &PluginConfigUpdate{Fallback: dbSettings}, nil
	}
	s.mask(updated)
	return &PluginConfigUpdate{Settings: updated}, nil
}

// List preserves the list endpoint's stored settings and nil shape.
func (s *PluginConfigService) List(ctx context.Context) ([]*store.PluginSettings, error) {
	return s.store.ListPluginSettings(ctx)
}
