package api

import (
	"net/http"
)

// handleListConnectors returns all registered connectors with their health status.
// If ?check=true is passed, it probes Health() on each connector first.
func (a *API) handleListConnectors(w http.ResponseWriter, r *http.Request) {
	if a.PluginHost == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "plugin system not initialized")
		return
	}

	check := r.URL.Query().Get("check") == "true"

	if check {
		statuses := a.PluginHost.CheckAllConnectorHealth()
		a.jsonResp(w, http.StatusOK, map[string]any{
			"connectors": statuses,
			"count":      len(statuses),
			"checked":    true,
		})
		return
	}

	statuses := a.PluginHost.GetConnectorStatuses()
	a.jsonResp(w, http.StatusOK, map[string]any{
		"connectors": statuses,
		"count":      len(statuses),
		"checked":    false,
	})
}

// handleCheckConnectorHealth probes a single connector's Health() method.
func (a *API) handleCheckConnectorHealth(w http.ResponseWriter, r *http.Request) {
	if a.PluginHost == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "plugin system not initialized")
		return
	}

	name := r.PathValue("name")
	status := a.PluginHost.CheckConnectorHealth(name)
	if status == nil {
		a.errorResp(w, http.StatusNotFound, "connector not found: "+name)
		return
	}
	a.jsonResp(w, http.StatusOK, status)
}
