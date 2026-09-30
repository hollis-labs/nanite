package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// writeReflexValidation writes the 400 body a create or patch returns for an
// invalid definition, and reports whether err was one.
func (a *API) writeReflexValidation(w http.ResponseWriter, err error) bool {
	var invalid *service.ReflexValidationError
	if !errors.As(err, &invalid) {
		return false
	}
	a.jsonResp(w, http.StatusBadRequest, map[string]any{"valid": false, "errors": invalid.Errors})
	return true
}

func (a *API) handleListPendingReflexes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Services.Reflexes.ListPending(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, pendingReflexesToView(rows))
}

func (a *API) handleApprovePendingReflex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReviewedBy string `json:"reviewed_by"`
	}
	_ = a.decode(r, &req)
	reflex, err := a.Services.Reflexes.ApprovePending(r.Context(), r.PathValue("id"), req.ReviewedBy)
	if err != nil {
		if errors.Is(err, store.ErrPendingReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "pending reflex not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agentReflexToView(reflex))
}

func (a *API) handleRejectPendingReflex(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ReviewedBy string `json:"reviewed_by"`
		Reason     string `json:"reason"`
	}
	_ = a.decode(r, &req)
	if err := a.Services.Reflexes.RejectPending(r.Context(), id, req.ReviewedBy, req.Reason); err != nil {
		if errors.Is(err, store.ErrPendingReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "pending reflex not found or already reviewed")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"id": id, "status": store.PendingReflexStatusRejected})
}

func (a *API) handleListAgentReflexes(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.Reflexes.ListForAgent(r.Context(), agent.ID, agent.Class)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agentReflexesToView(rows))
}

func (a *API) handleCreateAgentReflex(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req struct {
		Name                      string `json:"name"`
		TriggerKind               string `json:"trigger_kind"`
		TriggerSpec               string `json:"trigger_spec"`
		ActionKind                string `json:"action_kind"`
		ActionSpec                string `json:"action_spec"`
		Priority                  int64  `json:"priority"`
		OptOutAllowed             *bool  `json:"opt_out_allowed"`
		RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// opt_out_allowed defaults to true (permissive) when omitted — the same
	// "default-on, agent may opt out" default the DB column carries.
	// Reflexes created through this endpoint are never the hand-picked
	// safety-critical base seeds; an operator who needs a non-opt-outable
	// agent-specific reflex passes opt_out_allowed:false explicitly.
	optOutAllowed := true
	if req.OptOutAllowed != nil {
		optOutAllowed = *req.OptOutAllowed
	}
	created, err := a.Services.Reflexes.Create(r.Context(), store.AgentReflex{
		AgentID:                   agent.ID,
		Name:                      req.Name,
		TriggerKind:               req.TriggerKind,
		TriggerSpec:               req.TriggerSpec,
		ActionKind:                req.ActionKind,
		ActionSpec:                req.ActionSpec,
		Priority:                  req.Priority,
		CreatedBy:                 "operator",
		OptOutAllowed:             optOutAllowed,
		RecurrenceOverrideSeconds: req.RecurrenceOverrideSeconds,
	})
	if err != nil {
		var writeErr *service.ReflexWriteError
		switch {
		case a.writeReflexValidation(w, err):
		case errors.As(err, &writeErr):
			a.errorResp(w, http.StatusBadRequest, err.Error())
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusCreated, agentReflexToView(created))
}

func (a *API) handlePatchAgentReflex(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	// A missing or foreign reflex is reported before the body is read.
	if _, err := a.Services.Reflexes.GetOwned(r.Context(), agent.ID, reflexID); err != nil {
		switch {
		case errors.Is(err, store.ErrAgentReflexNotFound):
			a.errorResp(w, http.StatusNotFound, "reflex not found")
		case errors.Is(err, service.ErrReflexNotOwned):
			a.errorResp(w, http.StatusBadRequest, "cannot patch inherited or different-agent reflex through this endpoint")
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	var req struct {
		Name          *string `json:"name"`
		TriggerKind   *string `json:"trigger_kind"`
		TriggerSpec   *string `json:"trigger_spec"`
		ActionKind    *string `json:"action_kind"`
		ActionSpec    *string `json:"action_spec"`
		Status        *string `json:"status"`
		Priority      *int64  `json:"priority"`
		FiredCount    *int64  `json:"fired_count"`
		LastFiredAt   *string `json:"last_fired_at"`
		OptOutAllowed *bool   `json:"opt_out_allowed"`
		// nil leaves the override alone, 0 clears it, a positive value sets
		// it; see service.ReflexPatch.
		RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	reflex, err := a.Services.Reflexes.Patch(r.Context(), agent.ID, reflexID, service.ReflexPatch{
		Name:                      req.Name,
		TriggerKind:               req.TriggerKind,
		TriggerSpec:               req.TriggerSpec,
		ActionKind:                req.ActionKind,
		ActionSpec:                req.ActionSpec,
		Status:                    req.Status,
		Priority:                  req.Priority,
		FiredCount:                req.FiredCount,
		LastFiredAt:               req.LastFiredAt,
		OptOutAllowed:             req.OptOutAllowed,
		RecurrenceOverrideSeconds: req.RecurrenceOverrideSeconds,
	})
	if err != nil {
		var writeErr *service.ReflexWriteError
		switch {
		case a.writeReflexValidation(w, err):
		case errors.Is(err, store.ErrAgentReflexNotFound):
			a.errorResp(w, http.StatusNotFound, "reflex not found")
		case errors.Is(err, service.ErrReflexNotOwned):
			a.errorResp(w, http.StatusBadRequest, "cannot patch inherited or different-agent reflex through this endpoint")
		case errors.As(err, &writeErr):
			a.errorResp(w, http.StatusBadRequest, err.Error())
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, agentReflexToView(reflex))
}

func (a *API) handleDeleteAgentReflex(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	if err := a.Services.Reflexes.DeleteOwned(r.Context(), agent.ID, reflexID); err != nil {
		switch {
		case errors.Is(err, store.ErrAgentReflexNotFound):
			a.errorResp(w, http.StatusNotFound, "reflex not found")
		case errors.Is(err, service.ErrReflexNotOwned):
			a.errorResp(w, http.StatusBadRequest, "cannot delete inherited or different-agent reflex through this endpoint")
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"id": reflexID, "status": "deleted"})
}

// handleSetAgentReflexOptOut detaches agent from a class-wide reflex it
// would otherwise inherit. Rejects reflexes with opt_out_allowed=false.
// POST /api/agents/{id}/reflexes/{reflexId}/opt-out
func (a *API) handleSetAgentReflexOptOut(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	if err := a.Services.Reflexes.SetOptOut(r.Context(), agent.ID, reflexID); err != nil {
		switch {
		case errors.Is(err, store.ErrAgentReflexNotFound):
			a.errorResp(w, http.StatusNotFound, "reflex not found")
		case errors.Is(err, service.ErrReflexOptOutNotAllowed):
			a.errorResp(w, http.StatusBadRequest, "reflex does not allow opt-out")
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"agent_id": agent.ID, "reflex_id": reflexID, "opted_out": true})
}

// handleClearAgentReflexOptOut re-enables a class-wide reflex previously
// opted out of via handleSetAgentReflexOptOut.
// DELETE /api/agents/{id}/reflexes/{reflexId}/opt-out
func (a *API) handleClearAgentReflexOptOut(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	reflexID := r.PathValue("reflexId")
	if err := a.Services.Reflexes.ClearOptOut(r.Context(), agent.ID, reflexID); err != nil {
		if errors.Is(err, store.ErrAgentReflexNotFound) {
			a.errorResp(w, http.StatusNotFound, "reflex not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"agent_id": agent.ID, "reflex_id": reflexID, "opted_out": false})
}

func (a *API) handleValidateReflex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TriggerKind string         `json:"trigger_kind"`
		TriggerSpec string         `json:"trigger_spec"`
		ActionKind  string         `json:"action_kind"`
		ActionSpec  string         `json:"action_spec"`
		State       map[string]any `json:"state"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	row := store.AgentReflex{
		Name:        "validation",
		TriggerKind: req.TriggerKind,
		TriggerSpec: req.TriggerSpec,
		ActionKind:  req.ActionKind,
		ActionSpec:  req.ActionSpec,
		Status:      store.ReflexStatusActive,
	}
	errs := a.Services.Reflexes.ValidateDefinition(r.Context(), row)
	a.jsonResp(w, http.StatusOK, map[string]any{
		"valid":        len(errs) == 0,
		"errors":       errs,
		"fired":        len(errs) == 0 && evaluatesSimpleReflex(req.TriggerSpec, req.State),
		"state_source": stateSource(req.State),
		"state_summary": map[string]any{
			"messages": messageCount(req.State),
		},
	})
}

func evaluatesSimpleReflex(triggerSpec string, state map[string]any) bool {
	var spec struct {
		Kind   string  `json:"kind"`
		Window int     `json:"window"`
		Op     string  `json:"op"`
		Value  float64 `json:"value"`
	}
	if err := json.Unmarshal([]byte(triggerSpec), &spec); err != nil {
		return false
	}
	if spec.Kind != "tool_calls_window" || spec.Op != "=" || spec.Window <= 0 {
		return false
	}
	messages, ok := state["messages"].([]any)
	if !ok || len(messages) < spec.Window {
		return false
	}
	start := len(messages) - spec.Window
	for _, raw := range messages[start:] {
		msg, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if msg["tool_calls"] != spec.Value {
			return false
		}
	}
	return true
}

func stateSource(state map[string]any) string {
	if len(state) > 0 {
		return "request"
	}
	return "empty"
}

func messageCount(state map[string]any) int {
	messages, ok := state["messages"].([]any)
	if !ok {
		return 0
	}
	return len(messages)
}
