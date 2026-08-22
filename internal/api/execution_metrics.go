package api

import (
	"net/http"
	"strconv"
)

func (a *API) handleGetSessionExecutionMetrics(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	metrics, err := a.Services.Store.GetSessionExecutionMetrics(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load execution metrics")
		return
	}
	a.jsonResp(w, http.StatusOK, metrics)
}

func (a *API) handleGetRecentExecutionMetrics(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	metrics, err := a.Services.Store.GetRecentExecutionMetrics(r.Context(), limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load execution metrics")
		return
	}
	a.jsonResp(w, http.StatusOK, metrics)
}

func (a *API) handleGetUtilityCallSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.Services.Store.GetUtilityCallSummary(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load utility call summary")
		return
	}
	a.jsonResp(w, http.StatusOK, summary)
}

func (a *API) handleGetUtilityCallLog(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	log, err := a.Services.Store.GetUtilityCallLog(r.Context(), limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load utility call log")
		return
	}
	a.jsonResp(w, http.StatusOK, log)
}
