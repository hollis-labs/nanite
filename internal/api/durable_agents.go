package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	durableAgentEventsDefaultLimit = 50
	durableAgentEventsMaxLimit     = 200
)

func (a *API) handleCreateDurableAgent(w http.ResponseWriter, r *http.Request) {
	var req CreateDurableAgentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	inst := &store.DurableAgentInstance{
		ID:               req.ID,
		Name:             req.Name,
		Slug:             req.Slug,
		ProfileID:        req.ProfileID,
		LifecycleClass:   req.LifecycleClass,
		Provider:         req.Provider,
		Model:            req.Model,
		RuntimeKind:      req.RuntimeKind,
		LaunchSourceType: req.LaunchSourceType,
		LaunchSourceID:   req.LaunchSourceID,
		WorkRoot:         req.WorkRoot,
		MetadataJSON:     req.MetadataJSON,
	}
	saved, err := a.saveManagedDurableInstance(inst, false)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, saved)
}

func (a *API) saveManagedDurableInstance(inst *store.DurableAgentInstance, archived bool) (*store.DurableAgentInstance, error) {
	profile, err := a.Services.Store.GetAgent(inst.ProfileID)
	if err != nil {
		return nil, err
	}
	metadata, err := durableMetadataMap(inst.MetadataJSON)
	if err != nil {
		return nil, err
	}
	cfg := service.ManagedDurableAgentConfig{
		Name:             inst.Name,
		Slug:             inst.Slug,
		ProfileSlug:      profile.Slug,
		LifecycleClass:   inst.LifecycleClass,
		Provider:         inst.Provider,
		Model:            inst.Model,
		RuntimeKind:      inst.RuntimeKind,
		LaunchSourceType: inst.LaunchSourceType,
		LaunchSourceID:   inst.LaunchSourceID,
		WorkRoot:         inst.WorkRoot,
		Metadata:         metadata,
		Archived:         archived,
	}
	return service.SaveManagedDurableAgentConfig(a.Services.Store, a.Services.ManagedConfigRoot, cfg)
}

// durableMetadataMap parses the persisted metadata_json blob into the
// {[string]string} shape ManagedDurableAgentConfig expects. Invalid JSON or
// non-string values surface as an error so callers can return 400 instead of
// silently discarding operator input.
func durableMetadataMap(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("metadata_json must be a JSON object with string values: %w", err)
	}
	return out, nil
}

func (a *API) handleListDurableAgents(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	instances, err := a.Services.DurableAgents.List(r.Context(), includeArchived)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, instances)
}

func (a *API) handleGetDurableAgent(w http.ResponseWriter, r *http.Request) {
	inst, err := a.Services.DurableAgents.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, inst)
}

func (a *API) handleUpdateDurableAgent(w http.ResponseWriter, r *http.Request) {
	var req UpdateDurableAgentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	existing, err := a.Services.DurableAgents.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.WorkRoot != nil {
		existing.WorkRoot = *req.WorkRoot
	}
	if req.MetadataJSON != nil {
		existing.MetadataJSON = *req.MetadataJSON
	}
	saved, err := a.saveManagedDurableInstance(existing, false)
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, saved)
}

func (a *API) handleArchiveDurableAgent(w http.ResponseWriter, r *http.Request) {
	existing, err := a.Services.DurableAgents.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	archived, err := a.saveManagedDurableInstance(existing, true)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, archived)
}

func (a *API) handleListDurableAgentEvents(w http.ResponseWriter, r *http.Request) {
	limit := durableAgentEventsDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			a.errorResp(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if limit > durableAgentEventsMaxLimit {
		limit = durableAgentEventsMaxLimit
	}
	events, err := a.Services.DurableAgents.ListEvents(r.Context(), r.PathValue("id"), limit)
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, events)
}

func (a *API) handleDurableAgentStartRequest(w http.ResponseWriter, r *http.Request) {
	a.handleDurableAgentLifecycleRequest(w, r, "start")
}

func (a *API) handleDurableAgentLaunchPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := a.Services.DurableAgents.LaunchPlan(r.Context(), r.PathValue("id"), service.DurableAgentWakePayload{
		Reason: service.DurableAgentWakeManual,
	})
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if errors.Is(err, service.ErrDurableAgentUnsupportedLaunchPlan) {
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, plan)
}

func (a *API) handleDurableAgentStart(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentStartRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableAgents.Start(r.Context(), r.PathValue("id"), service.DurableAgentStartRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
	})
	a.writeDurableAgentLaunchResult(w, result, err)
}

func (a *API) handleDurableAgentResume(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentStartRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableAgents.Resume(r.Context(), r.PathValue("id"), service.DurableAgentStartRequest{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
	})
	a.writeDurableAgentLaunchResult(w, result, err)
}

func (a *API) handleDurableAgentStopRequest(w http.ResponseWriter, r *http.Request) {
	a.handleDurableAgentLifecycleRequest(w, r, "stop")
}

func (a *API) handleDurableAgentPauseRequest(w http.ResponseWriter, r *http.Request) {
	a.handleDurableAgentLifecycleRequest(w, r, "pause")
}

func (a *API) handleDurableAgentResumeRequest(w http.ResponseWriter, r *http.Request) {
	a.handleDurableAgentLifecycleRequest(w, r, "resume")
}

func (a *API) handleDurableAgentLifecycleRequest(w http.ResponseWriter, r *http.Request, action string) {
	id := r.PathValue("id")
	var (
		inst *store.DurableAgentInstance
		err  error
	)
	switch action {
	case "start":
		inst, err = a.Services.DurableAgents.RequestStart(r.Context(), id)
	case "stop":
		inst, err = a.Services.DurableAgents.RequestStop(r.Context(), id)
	case "pause":
		inst, err = a.Services.DurableAgents.RequestPause(r.Context(), id)
	case "resume":
		inst, err = a.Services.DurableAgents.RequestResume(r.Context(), id)
	}
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if errors.Is(err, service.ErrDurableAgentNoResumableSession) {
		a.errorResp(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, service.ErrDurableAgentUnsupportedLaunchPlan) {
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, inst)
}

func (a *API) handleAttachDurableAgentSession(w http.ResponseWriter, r *http.Request) {
	var req AttachDurableAgentSessionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}
	if err := a.Services.DurableAgents.AttachSession(r.Context(), r.PathValue("id"), req.SessionID, req.Relation); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleListDurableAgentSessions(w http.ResponseWriter, r *http.Request) {
	relations, err := a.Services.DurableAgents.ListSessionStates(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, relations)
}

func (a *API) writeDurableAgentLaunchResult(w http.ResponseWriter, result *service.DurableAgentLaunchResult, err error) {
	if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent not found")
		return
	}
	if errors.Is(err, service.ErrDurableAgentWorkspaceRequired) ||
		errors.Is(err, service.ErrDurableAgentNoResumableSession) {
		a.errorResp(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, service.ErrDurableAgentUnsupportedLaunchPlan) {
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}

func durableAgentWakePayloadFromRequest(req DurableAgentWakePayloadRequest) service.DurableAgentWakePayload {
	return service.DurableAgentWakePayload{
		Reason:   req.Reason,
		Prompt:   req.Prompt,
		Facts:    req.Facts,
		Metadata: req.Metadata,
	}
}
