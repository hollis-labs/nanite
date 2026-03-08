package api

import "net/http"

func (a *API) handleGetSessionUsage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	summary, err := a.Store.GetSessionUsage(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, summary)
}

func (a *API) handleGetUsageSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.Store.GetUsageSummary()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, summary)
}
