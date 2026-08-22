package api

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/store"
)

// pluginSecretKeyName returns the keychain key for a plugin's secret field.
func pluginSecretKeyName(pluginID, fieldKey string) string {
	return "plugin:" + pluginID + ":" + fieldKey
}

// secretFieldKeys returns the set of field keys with type "secret" in a plugin's schema.
func secretFieldKeys(schema []store.ConfigField) map[string]bool {
	keys := make(map[string]bool)
	for _, f := range schema {
		if f.Type == "secret" {
			keys[f.Key] = true
		}
	}
	return keys
}

func (a *API) handleGetPluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")

	settings, err := a.Services.Store.GetPluginSettings(r.Context(), pluginID)
	if err != nil {
		// No settings saved yet — return empty defaults.
		a.jsonResp(w, http.StatusOK, map[string]any{
			"plugin_id": pluginID,
			"settings":  map[string]any{},
			"schema":    []any{},
		})
		return
	}

	// For secret fields, indicate presence without exposing the value.
	secKeys := secretFieldKeys(settings.Schema)
	for key := range secKeys {
		keychainKey := pluginSecretKeyName(pluginID, key)
		if secrets.Has(keychainKey) {
			settings.Settings[key] = "********"
		} else {
			delete(settings.Settings, key)
		}
	}

	a.jsonResp(w, http.StatusOK, settings)
}

func (a *API) handleUpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")

	var incoming map[string]any
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Load existing settings + schema to identify secret fields.
	existing, err := a.Services.Store.GetPluginSettings(r.Context(), pluginID)
	var secKeys map[string]bool
	if err == nil && existing != nil {
		secKeys = secretFieldKeys(existing.Schema)
		// Start from existing non-secret settings.
		for k, v := range incoming {
			existing.Settings[k] = v
		}
		incoming = existing.Settings
	} else {
		secKeys = make(map[string]bool)
	}

	// Route secret fields to keychain, keep non-secrets in DB.
	dbSettings := make(map[string]any)
	for k, v := range incoming {
		if secKeys[k] {
			val, _ := v.(string)
			if val != "" && val != "********" {
				keychainKey := pluginSecretKeyName(pluginID, k)
				if err := secrets.Set(keychainKey, val); err != nil {
					a.errorResp(w, http.StatusInternalServerError, "failed to store secret in keychain")
					return
				}
			}
			// Don't store secret values in the DB.
		} else {
			dbSettings[k] = v
		}
	}

	if err := a.Services.Store.UpsertPluginSettings(r.Context(), pluginID, dbSettings); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to save plugin config")
		return
	}

	// Emit plugin event: plugin config changed.
	if a.Services.Plugins != nil {
		for key := range incoming {
			k := key
			safego.Go(r.Context(), "api.plugin_config.emit.config-changed", func() {
				a.Services.Plugins.EmitConfigChanged(pluginID, k, "")
			})
		}
	}

	// Return the full settings after merge.
	updated, err := a.Services.Store.GetPluginSettings(r.Context(), pluginID)
	if err != nil {
		a.jsonResp(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "settings": dbSettings})
		return
	}

	// Mask secrets in response.
	updSecKeys := secretFieldKeys(updated.Schema)
	for key := range updSecKeys {
		keychainKey := pluginSecretKeyName(pluginID, key)
		if secrets.Has(keychainKey) {
			updated.Settings[key] = "********"
		} else {
			delete(updated.Settings, key)
		}
	}

	a.jsonResp(w, http.StatusOK, updated)
}

func (a *API) handleListPluginSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Services.Store.ListPluginSettings(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list plugin settings")
		return
	}
	if settings == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}
	a.jsonResp(w, http.StatusOK, settings)
}
