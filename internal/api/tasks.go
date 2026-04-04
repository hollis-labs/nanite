package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/task"
)

func (a *API) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	parentID := r.URL.Query().Get("parent_id")
	status := r.URL.Query().Get("status")

	ctx := r.Context()
	var tasks []*task.Task
	var err error

	switch {
	case sessionID != "":
		tasks, err = a.Services.Tasks.ListBySession(ctx, sessionID)
	case parentID != "":
		tasks, err = a.Services.Tasks.ListByParent(ctx, parentID)
	default:
		tasks, err = a.Services.Tasks.ListAll(ctx)
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Apply status filter if both session/parent and status are set.
	if status != "" && tasks != nil {
		filtered := make([]*task.Task, 0)
		for _, t := range tasks {
			if string(t.Status) == status {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	if tasks == nil {
		tasks = []*task.Task{}
	}
	a.jsonResp(w, http.StatusOK, tasks)
}

func (a *API) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "task service not available")
		return
	}

	var t task.Task
	if err := a.decode(r, &t); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if t.Title == "" {
		a.errorResp(w, http.StatusBadRequest, "title is required")
		return
	}
	if t.SessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}

	if err := a.Services.Tasks.Create(r.Context(), &t); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, t)
}

func (a *API) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "task service not available")
		return
	}

	t, err := a.Services.Tasks.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, t)
}

func (a *API) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "task service not available")
		return
	}

	id := r.PathValue("id")
	existing, err := a.Services.Tasks.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	var update task.Task
	if err := a.decode(r, &update); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Apply partial update to existing task.
	if update.Title != "" {
		existing.Title = update.Title
	}
	if update.Description != "" {
		existing.Description = update.Description
	}
	if update.Result != "" {
		existing.Result = update.Result
	}
	if update.Metadata != nil {
		for k, v := range update.Metadata {
			existing.Metadata[k] = v
		}
	}

	if err := a.Services.Tasks.Update(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleTransitionTask(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "task service not available")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status == "" {
		a.errorResp(w, http.StatusBadRequest, "status is required")
		return
	}

	if err := a.Services.Tasks.Transition(r.Context(), r.PathValue("id"), task.Status(req.Status)); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	t, _ := a.Services.Tasks.Get(r.Context(), r.PathValue("id"))
	a.jsonResp(w, http.StatusOK, t)
}

func (a *API) handleAssignTask(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "task service not available")
		return
	}

	var req struct {
		AgentID         string `json:"agent_id"`
		WorkerSessionID string `json:"worker_session_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.Services.Tasks.Assign(r.Context(), r.PathValue("id"), req.AgentID, req.WorkerSessionID); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	t, _ := a.Services.Tasks.Get(r.Context(), r.PathValue("id"))
	a.jsonResp(w, http.StatusOK, t)
}

func (a *API) handleListSessionTasks(w http.ResponseWriter, r *http.Request) {
	if a.Services.Tasks == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	tasks, err := a.Services.Tasks.ListBySession(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*task.Task{}
	}
	a.jsonResp(w, http.StatusOK, tasks)
}
