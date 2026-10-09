package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) registerProtectedProfileRetirementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agents/{id}/protected-retirement-export", a.handleProtectedProfileRetirementExport)
	mux.HandleFunc("POST /api/agents/{id}/protected-retire", a.handleProtectedProfileRetire)
}

func protectedRetirementErrorStatus(err error) int {
	if errors.Is(err, store.ErrProtectedRetirementAuthority) {
		return http.StatusForbidden
	}
	if errors.Is(err, store.ErrProfileRetirementActive) {
		return http.StatusConflict
	}
	if errors.Is(err, service.ErrUnsupportedDefinition) || errors.Is(err, service.ErrDefinitionDigestMismatch) {
		return http.StatusUnprocessableEntity
	}
	return profileRetirementErrorStatus(err)
}

func decodeProtectedRetirement(w http.ResponseWriter, r *http.Request, request any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		http.Error(w, "invalid protected retirement request", http.StatusBadRequest)
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "invalid protected retirement request", http.StatusBadRequest)
		return false
	}
	return true
}

func (a *API) handleProtectedProfileRetirementExport(w http.ResponseWriter, r *http.Request) {
	var request service.ProtectedRetirementRequest
	if !decodeProtectedRetirement(w, r, &request) {
		return
	}
	receipt, err := a.Services.AgentConfig.ExportProtectedProfile(r.Context(), a.Services.CognitiveViews, r.PathValue("id"), request)
	if err != nil {
		http.Error(w, "protected profile export refused", protectedRetirementErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(receipt)
}

func (a *API) handleProtectedProfileRetire(w http.ResponseWriter, r *http.Request) {
	var request struct {
		service.ProtectedRetirementRequest
		ExportID string `json:"export_id"`
		Digest   string `json:"digest"`
	}
	if !decodeProtectedRetirement(w, r, &request) {
		return
	}
	receipt, err := a.Services.AgentConfig.RetireProtectedProfile(r.Context(), r.PathValue("id"), request.ExportID, request.Digest, request.ProtectedRetirementRequest)
	if err != nil && !receipt.Retired {
		http.Error(w, "protected profile retirement refused", protectedRetirementErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	_ = json.NewEncoder(w).Encode(receipt)
}
