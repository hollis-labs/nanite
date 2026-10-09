package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) registerLogicalAgentProvisionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agents/provision/general-chat", a.handleLogicalGeneralChatProvision)
}

func (a *API) handleLogicalGeneralChatProvision(w http.ResponseWriter, r *http.Request) {
	var request service.ProvisionGeneralChatRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid logical agent request", http.StatusBadRequest)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "invalid logical agent request", http.StatusBadRequest)
		return
	}
	result, err := a.Services.AgentConfig.ProvisionGeneralChat(r.Context(), a.Services.CognitiveViews, request)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, service.ErrDefinitionNotFound):
			status = http.StatusNotFound
		case errors.Is(err, service.ErrDefinitionDigestMismatch), errors.Is(err, service.ErrManagedSlugExists):
			status = http.StatusConflict
		case errors.Is(err, service.ErrUnsupportedDefinition), errors.Is(err, service.ErrUnsupportedModel):
			status = http.StatusUnprocessableEntity
		}
		http.Error(w, "logical agent provisioning refused", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(result)
}
