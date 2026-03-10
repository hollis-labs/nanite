package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// volonBacklogRequest is the payload for creating a Volon backlog item.
type volonBacklogRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Priority  string   `json:"priority"`
	Tags      []string `json:"tags"`
	ProjectID string   `json:"project_id"`
}

// handleCreateVolonBacklog proxies a backlog capture request to the Volon MCP server.
// POST /api/volon/backlog
func (a *API) handleCreateVolonBacklog(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	var req volonBacklogRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		a.errorResp(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Priority == "" {
		req.Priority = "B"
	}

	// Build the MCP tool arguments matching volon_backlog_capture's input schema.
	args := map[string]any{
		"title":    req.Title,
		"body":     req.Body,
		"priority": req.Priority,
	}
	if req.ProjectID != "" {
		args["project_id"] = req.ProjectID
	}
	if len(req.Tags) > 0 {
		tagsJSON, _ := json.Marshal(req.Tags)
		args["tags"] = string(tagsJSON)
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_backlog_capture", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon backlog capture failed: %v", err))
		return
	}

	// Try to parse the result as JSON; if it fails, wrap it.
	var parsed any
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr == nil {
		a.jsonResp(w, http.StatusCreated, parsed)
		return
	}

	a.jsonResp(w, http.StatusCreated, map[string]string{
		"result": result,
	})
}

// --- Sprint planning / backlog grooming proxy endpoints ---

// handleVolonListSprints proxies volon_sprints_list.
// GET /api/volon/sprints?project_id=X
func (a *API) handleVolonListSprints(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	args := map[string]any{}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		args["project_id"] = pid
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_sprints_list", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon sprints_list: %v", err))
		return
	}

	a.writeVolonJSON(w, http.StatusOK, result)
}

// handleVolonListTasks proxies volon_tasks_list.
// GET /api/volon/tasks?sprint_id=X&status=Y&project_id=Z
func (a *API) handleVolonListTasks(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	args := map[string]any{}
	if sid := r.URL.Query().Get("sprint_id"); sid != "" {
		args["sprint_id"] = sid
	}
	if status := r.URL.Query().Get("status"); status != "" {
		args["status"] = status
	}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		args["project_id"] = pid
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_tasks_list", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon tasks_list: %v", err))
		return
	}

	a.writeVolonJSON(w, http.StatusOK, result)
}

// handleVolonListBacklog proxies volon_backlog_list.
// GET /api/volon/backlog?project_id=X
func (a *API) handleVolonListBacklog(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	args := map[string]any{}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		args["project_id"] = pid
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_backlog_list", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon backlog_list: %v", err))
		return
	}

	a.writeVolonJSON(w, http.StatusOK, result)
}

// handleVolonTransitionTask proxies volon_task_transition.
// POST /api/volon/tasks/{id}/transition  body: {"status":"done"}
func (a *API) handleVolonTransitionTask(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		a.errorResp(w, http.StatusBadRequest, "missing task id")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Status == "" {
		a.errorResp(w, http.StatusBadRequest, "status is required")
		return
	}

	args := map[string]any{
		"id":     taskID,
		"status": req.Status,
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_task_transition", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon task_transition: %v", err))
		return
	}

	a.writeVolonJSON(w, http.StatusOK, result)
}

// handleVolonPromoteBacklog proxies volon_backlog_promote.
// POST /api/volon/backlog/{id}/promote  body: {"sprint_id":"..."}
func (a *API) handleVolonPromoteBacklog(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	backlogID := r.PathValue("id")
	if backlogID == "" {
		a.errorResp(w, http.StatusBadRequest, "missing backlog item id")
		return
	}

	var req struct {
		SprintID string `json:"sprint_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SprintID == "" {
		a.errorResp(w, http.StatusBadRequest, "sprint_id is required")
		return
	}

	args := map[string]any{
		"id":        backlogID,
		"sprint_id": req.SprintID,
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_backlog_promote", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon backlog_promote: %v", err))
		return
	}

	a.writeVolonJSON(w, http.StatusOK, result)
}

// handleVolonDeleteTask proxies volon_task_delete.
// DELETE /api/volon/tasks/{id}
func (a *API) handleVolonDeleteTask(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		a.errorResp(w, http.StatusBadRequest, "missing task id")
		return
	}

	args := map[string]any{
		"id": taskID,
	}

	result, err := a.MCPManager.ExecuteTool(r.Context(), "mcp__volon__volon_task_delete", args)
	if err != nil {
		a.errorResp(w, http.StatusBadGateway, fmt.Sprintf("volon task_delete: %v", err))
		return
	}

	a.writeVolonJSON(w, http.StatusOK, result)
}

// writeVolonJSON writes a pre-serialized JSON string from an MCP tool result.
// If the string is not valid JSON, it wraps it in a {"result": ...} envelope.
func (a *API) writeVolonJSON(w http.ResponseWriter, status int, raw string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if json.Valid([]byte(raw)) {
		w.Write([]byte(raw))
		return
	}

	envelope := map[string]string{"result": raw}
	json.NewEncoder(w).Encode(envelope)
}
