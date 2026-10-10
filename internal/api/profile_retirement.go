package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) registerProfileRetirementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agents/{id}/retirement-export", a.handleProfileRetirementExport)
	mux.HandleFunc("POST /api/agents/{id}/retire", a.handleProfileRetire)
}

func profileRetirementErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrProfileRetirementProtected):
		return http.StatusForbidden
	case errors.Is(err, store.ErrProfileRetirementConflict):
		return http.StatusConflict
	case errors.Is(err, store.ErrProfileRetirementKeep), errors.Is(err, store.ErrProfileRetirementActive):
		return http.StatusConflict
	case errors.Is(err, store.ErrProfileRetirementBound):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func (a *API) handleProfileRetirementExport(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IncludeProtected bool `json:"include_protected"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid retirement export request", http.StatusBadRequest)
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			http.Error(w, "invalid retirement export request", http.StatusBadRequest)
			return
		}
	}
	var receipt service.ProfileRetirementReceipt
	var err error
	if request.IncludeProtected {
		receipt, err = a.Services.AgentConfig.ExportProtectedProfile(r.Context(), r.PathValue("id"))
	} else {
		receipt, err = a.Services.AgentConfig.ExportEditableProfile(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		http.Error(w, "profile export refused", profileRetirementErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(receipt)
}

func (a *API) handleProfileRetire(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ExportID            string `json:"export_id"`
		Digest              string `json:"digest"`
		IncludeProtected    bool   `json:"include_protected"`
		Actor               string `json:"actor"`
		Reason              string `json:"reason"`
		GeneralChatID       string `json:"general_chat_id"`
		GeneralChatRevision string `json:"general_chat_revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid retirement request", http.StatusBadRequest)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "invalid retirement request", http.StatusBadRequest)
		return
	}
	var receipt service.ProfileRetirementReceipt
	var err error
	if request.IncludeProtected {
		receipt, err = a.Services.AgentConfig.RetireProtectedProfile(r.Context(), r.PathValue("id"), request.ExportID, request.Digest, service.ProtectedProfileRetirementRequest{Actor: request.Actor, Reason: request.Reason, GeneralChatID: request.GeneralChatID, GeneralChatRevision: request.GeneralChatRevision})
	} else {
		receipt, err = a.Services.AgentConfig.RetireEditableProfile(r.Context(), r.PathValue("id"), request.ExportID, request.Digest)
	}
	if err != nil && !receipt.Retired {
		http.Error(w, "profile retirement refused", profileRetirementErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	_ = json.NewEncoder(w).Encode(receipt)
}
