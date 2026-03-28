package api

import (
	"net/http"
	"strconv"
)

func (a *API) handleGetSessionExecutionMetrics(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	metrics, err := a.Store.GetSessionExecutionMetrics(sessionID)
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
	metrics, err := a.Store.GetRecentExecutionMetrics(limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load execution metrics")
		return
	}
	a.jsonResp(w, http.StatusOK, metrics)
}

func (a *API) handleGetUtilityCallSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.Store.GetUtilityCallSummary()
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
	log, err := a.Store.GetUtilityCallLog(limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load utility call log")
		return
	}
	a.jsonResp(w, http.StatusOK, log)
}
