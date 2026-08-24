package api

import (
	"net/http"
)

func (a *API) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	if a.Services.Workers == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	workers := a.Services.Workers.List()
	if workers == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}
	a.jsonResp(w, http.StatusOK, workers)
}

func (a *API) handleCancelWorker(w http.ResponseWriter, r *http.Request) {
	if a.Services.Workers == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "worker manager not available")
		return
	}

	workerID := r.PathValue("id")
	if err := a.Services.Workers.Cancel(workerID); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "canceled"})
}
