package api

import (
	"net/http"
	"time"
)

const defaultStaleThreshold = 5 * time.Minute

func (a *API) handleProcessHealth(w http.ResponseWriter, r *http.Request) {
	if a.Services.ProcessTracker == nil {
		a.jsonResp(w, http.StatusOK, map[string]any{
			"processes": []any{},
			"total":     0,
		})
		return
	}

	health := a.Services.ProcessTracker.HealthCheck(defaultStaleThreshold)
	a.jsonResp(w, http.StatusOK, map[string]any{
		"processes":       health,
		"total":           a.Services.ProcessTracker.Count(),
		"stale_threshold": defaultStaleThreshold.String(),
	})
}

func (a *API) handleKillStaleProcesses(w http.ResponseWriter, r *http.Request) {
	if a.Services.ProcessTracker == nil {
		a.jsonResp(w, http.StatusOK, map[string]any{"killed": 0})
		return
	}

	killed := a.Services.ProcessTracker.KillStale(defaultStaleThreshold)
	a.jsonResp(w, http.StatusOK, map[string]any{"killed": killed})
}
