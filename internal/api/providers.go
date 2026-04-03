package api

import "net/http"

func (a *API) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := a.Services.Store.ListProviders()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, providers)
}

func (a *API) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.Services.Store.ListModels()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, models)
}
