package api

import (
	"net/http"
)

func (a *API) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if a.WorkflowLoader == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}
	a.jsonResp(w, http.StatusOK, a.WorkflowLoader.List())
}

func (a *API) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if a.WorkflowLoader == nil {
		a.errorResp(w, http.StatusNotFound, "workflow not found")
		return
	}

	def, ok := a.WorkflowLoader.Get(name)
	if !ok {
		a.errorResp(w, http.StatusNotFound, "workflow not found")
		return
	}
	a.jsonResp(w, http.StatusOK, def)
}

func (a *API) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if a.WorkflowLoader == nil || a.WorkflowEngine == nil {
		a.errorResp(w, http.StatusNotFound, "workflow not found")
		return
	}

	def, ok := a.WorkflowLoader.Get(name)
	if !ok {
		a.errorResp(w, http.StatusNotFound, "workflow not found")
		return
	}

	var req struct {
		Inputs map[string]string `json:"inputs"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Inputs == nil {
		req.Inputs = make(map[string]string)
	}

	result, err := a.WorkflowEngine.Execute(r.Context(), def, req.Inputs)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, result)
}
