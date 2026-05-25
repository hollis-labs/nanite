package api

import (
	"errors"
	"net/http"
	"strings"

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
	serviceReq := durableAgentRecipeRequestToService(req)
	plan, err := a.Services.DurableAgentRecipes.DryRun(r.Context(), r.PathValue("id"), serviceReq)
	if errors.Is(err, service.ErrDurableAgentRecipeNotFound) {
		a.errorResp(w, http.StatusNotFound, "durable agent recipe not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(plan.MissingRequirements) > 0 {
		a.errorResp(w, http.StatusBadRequest, service.ErrDurableAgentRecipeMissingInputs.Error()+": "+strings.Join(plan.MissingRequirements, ", "))
		return
	}
	if len(plan.Unsupported) > 0 {
		a.errorResp(w, http.StatusConflict, service.ErrDurableAgentRecipeApplyNotReady.Error()+": "+strings.Join(plan.Unsupported, ", "))
		return
	}
	inst, err := a.saveManagedDurableInstance(&plan.Instance, false)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	result := &service.DurableAgentRecipeApplyResult{
		Plan:     *plan,
		Instance: inst,
	}
	if serviceReq.Start {
		launch, err := a.Services.DurableAgents.Start(r.Context(), inst.ID, service.DurableAgentStartRequest{
			WorkspaceID: serviceReq.WorkspaceID,
			ProjectID:   serviceReq.ProjectID,
			WakePayload: plan.WakePayload,
		})
		if err != nil {
			a.errorResp(w, http.StatusConflict, err.Error())
			return
		}
		result.Instance = launch.Instance
		result.LaunchResult = launch
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
