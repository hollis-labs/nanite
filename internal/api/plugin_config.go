package api

import (
	"net/http"
)

func (a *API) handleGetPluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")
	settings, err := a.Services.PluginConfig.Get(r.Context(), pluginID)
	if err != nil {
		a.jsonResp(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "settings": map[string]any{}, "schema": []any{}})
		return
	}
	a.jsonResp(w, http.StatusOK, pluginSettingsView(settings))
}
func (a *API) handleUpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")
	var incoming map[string]any
	if err := a.decode(r, &incoming); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	updated, err := a.Services.PluginConfig.Update(r.Context(), pluginID, incoming)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated.Settings == nil {
		a.jsonResp(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "settings": updated.Fallback})
		return
	}
	a.jsonResp(w, http.StatusOK, pluginSettingsView(updated.Settings))
}
func (a *API) handleListPluginSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Services.PluginConfig.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list plugin settings")
		return
	}
	if settings == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}
	a.jsonResp(w, http.StatusOK, pluginSettingsViews(settings))
}
