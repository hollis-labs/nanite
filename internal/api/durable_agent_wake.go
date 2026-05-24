package api

import (
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type DurableAgentWakeRunRequestBody struct {
	DryRun bool   `json:"dry_run"`
	Now    string `json:"now,omitempty"`
}

func (a *API) handleListDurableAgentDueWake(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	if raw := r.URL.Query().Get("now"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			now = parsed.UTC()
		}
	}
	items, err := a.Services.DurableWake.ListDue(r.Context(), now)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, items)
}

func (a *API) handleRunDurableAgentDueWake(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentWakeRunRequestBody
	_ = a.decode(r, &req)
	now := time.Now().UTC()
	if req.Now != "" {
		parsed, err := time.Parse(time.RFC3339, req.Now)
		if err != nil {
			a.errorResp(w, http.StatusBadRequest, "now must be RFC3339")
			return
		}
		now = parsed.UTC()
	}
	result, err := a.Services.DurableWake.RunDue(r.Context(), service.DurableAgentWakeRunRequest{
		Now:    now,
		DryRun: req.DryRun,
	})
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}

func (a *API) handleDurableAgentWake(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentStartRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableWake.Wake(r.Context(), r.PathValue("id"), service.DurableAgentWakeRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
	})
	if err != nil {
		a.errorResp(w, http.StatusConflict, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}

func (a *API) handleListDurableAgentSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := a.Services.DurableWake.ListSchedules(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, schedules)
}

func (a *API) handlePauseDurableAgentSchedule(w http.ResponseWriter, r *http.Request) {
	a.handleSetDurableAgentScheduleStatus(w, r, store.ScheduleStatusPaused)
}

func (a *API) handleResumeDurableAgentSchedule(w http.ResponseWriter, r *http.Request) {
	a.handleSetDurableAgentScheduleStatus(w, r, store.ScheduleStatusActive)
}

func (a *API) handleSetDurableAgentScheduleStatus(w http.ResponseWriter, r *http.Request, status string) {
	if err := a.Services.DurableWake.UpdateScheduleStatus(r.Context(), r.PathValue("id"), r.PathValue("scheduleId"), status); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
