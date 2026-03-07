package api

import "net/http"

func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.Store.ListAgents()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}
