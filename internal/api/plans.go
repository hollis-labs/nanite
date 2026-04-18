package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListPlans(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.PlanFilter{
		Scope:   q.Get("scope"),
		ScopeID: q.Get("scope_id"),
		Status:  q.Get("status"),
	}
	plans, err := a.Services.Todos.ListPlans(r.Context(), f)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, plans)
}

func (a *API) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var p store.Plan
	if err := a.decode(r, &p); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := a.Services.Todos.CreatePlan(r.Context(), &p); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusCreated, p)
}

func (a *API) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	p, err := a.Services.Todos.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, p)
}

func (a *API) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	var updates service.PlanUpdates
	if err := a.decode(r, &updates); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	p, err := a.Services.Todos.UpdatePlan(r.Context(), r.PathValue("id"), updates)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusOK, p)
}

func (a *API) handleUpdatePlanStep(w http.ResponseWriter, r *http.Request) {
	var updates store.PlanStep
	if err := a.decode(r, &updates); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := a.Services.Todos.UpdatePlanStep(r.Context(), r.PathValue("id"), r.PathValue("stepID"), updates); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	// Return the updated plan.
	p, err := a.Services.Todos.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusOK, p)
}

func (a *API) handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	if err := a.Services.Todos.DeletePlan(r.Context(), r.PathValue("id")); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

func (a *API) handleApprovePlan(w http.ResponseWriter, r *http.Request) {
	var req ApprovePlanRequest
	if err := a.decode(r, &req); err != nil {
		// Default to not creating todos if body is empty/invalid.
		req.CreateTodos = false
	}
	p, err := a.Services.Todos.ApprovePlan(r.Context(), r.PathValue("id"), req.CreateTodos)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusOK, p)
}
