package api

import (
	"encoding/json"
	"net/http"
)

func (a *API) handleGetPluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")

	settings, err := a.Store.GetPluginSettings(pluginID)
	if err != nil {
		// No settings saved yet — return empty defaults.
		a.jsonResp(w, http.StatusOK, map[string]any{
			"plugin_id": pluginID,
			"settings":  map[string]any{},
			"schema":    []any{},
		})
		return
	}
	a.jsonResp(w, http.StatusOK, settings)
}

func (a *API) handleUpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")

	var settings map[string]any
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Merge with existing settings (partial update).
	existing, err := a.Store.GetPluginSettings(pluginID)
	if err == nil && existing != nil && existing.Settings != nil {
		for k, v := range settings {
			existing.Settings[k] = v
		}
		settings = existing.Settings
	}

	if err := a.Store.UpsertPluginSettings(pluginID, settings); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to save plugin config")
		return
	}

	// Return the full settings after merge.
	updated, err := a.Store.GetPluginSettings(pluginID)
	if err != nil {
		a.jsonResp(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "settings": settings})
		return
	}
	a.jsonResp(w, http.StatusOK, updated)
}

func (a *API) handleListPluginSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Store.ListPluginSettings()
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
