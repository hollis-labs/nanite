package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) handleListDurableAgentRecipes(w http.ResponseWriter, r *http.Request) {
	recipes, err := a.Services.DurableAgentRecipes.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, recipes)
}

func (a *API) handleGetDurableAgentRecipe(w http.ResponseWriter, r *http.Request) {
	recipe, err := a.Services.DurableAgentRecipes.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, service.ErrDurableAgentRecipeNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent recipe not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, recipe)
}

func (a *API) handleDryRunDurableAgentRecipe(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentRecipeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	plan, err := a.Services.DurableAgentRecipes.DryRun(r.Context(), r.PathValue("id"), durableAgentRecipeRequestToService(req))
	if errors.Is(err, service.ErrDurableAgentRecipeNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent recipe not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, plan)
}

func (a *API) handleApplyDurableAgentRecipe(w http.ResponseWriter, r *http.Request) {
	var req DurableAgentRecipeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := a.Services.DurableAgentRecipes.Apply(r.Context(), r.PathValue("id"), durableAgentRecipeRequestToService(req))
	if errors.Is(err, service.ErrDurableAgentRecipeNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent recipe not found")
		return
	}
	if errors.Is(err, service.ErrDurableAgentRecipeMissingInputs) {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, service.ErrDurableAgentRecipeApplyNotReady) ||
		errors.Is(err, service.ErrDurableAgentWorkspaceRequired) {
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
	a.jsonResp(w, http.StatusCreated, result)
}

func durableAgentRecipeRequestToService(req DurableAgentRecipeRequest) service.DurableAgentRecipeRequest {
	return service.DurableAgentRecipeRequest{
		Name:        req.Name,
		Slug:        req.Slug,
		ProfileID:   req.ProfileID,
		Provider:    req.Provider,
		Model:       req.Model,
		RuntimeKind: req.RuntimeKind,
		WorkRoot:    req.WorkRoot,
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		WakePayload: durableAgentWakePayloadFromRequest(req.WakePayload),
		Metadata:    req.Metadata,
		Start:       req.Start,
	}
}
