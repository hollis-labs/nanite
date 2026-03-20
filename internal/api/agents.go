package api

import (
	"net/http"

	"github.com/hollis-labs/conduit/internal/store"
)

func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.Store.ListAgents()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}

func (a *API) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Slug            string `json:"slug"`
		Avatar          string `json:"avatar"`
		SystemPrompt    string `json:"system_prompt"`
		Description     string `json:"description"`
		Modes           string `json:"modes"`
		DefaultMode     string `json:"default_mode"`
		DefaultModel    string `json:"default_model"`
		MCPServers      string `json:"mcp_servers"`
		ToolPermissions string `json:"tool_permissions"`
		CanExecute      bool   `json:"can_execute"`
		Settings        string `json:"settings"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" || req.SystemPrompt == "" {
		a.errorResp(w, http.StatusBadRequest, "name, slug, and system_prompt are required")
		return
	}

	agent := &store.AgentProfile{
		ID:              req.ID,
		Name:            req.Name,
		Slug:            req.Slug,
		Avatar:          req.Avatar,
		SystemPrompt:    req.SystemPrompt,
		Description:     req.Description,
		Modes:           req.Modes,
		DefaultMode:     req.DefaultMode,
		DefaultModel:    req.DefaultModel,
		MCPServers:      req.MCPServers,
		ToolPermissions: req.ToolPermissions,
		CanExecute:      req.CanExecute,
		Settings:        req.Settings,
	}
	if err := a.Store.CreateAgent(agent); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, agent)
}

func (a *API) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	agent, err := a.Store.GetAgent(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	modes, err := a.Store.ListAgentModes(id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"agent": agent,
		"modes": modes,
	})
}

func (a *API) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Store.GetAgent(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Name            *string `json:"name"`
		Slug            *string `json:"slug"`
		Avatar          *string `json:"avatar"`
		SystemPrompt    *string `json:"system_prompt"`
		Description     *string `json:"description"`
		Modes           *string `json:"modes"`
		DefaultMode     *string `json:"default_mode"`
		DefaultModel    *string `json:"default_model"`
		MCPServers      *string `json:"mcp_servers"`
		ToolPermissions *string `json:"tool_permissions"`
		CanExecute      *bool   `json:"can_execute"`
		Settings        *string `json:"settings"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.Avatar != nil {
		existing.Avatar = *req.Avatar
	}
	if req.SystemPrompt != nil {
		existing.SystemPrompt = *req.SystemPrompt
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Modes != nil {
		existing.Modes = *req.Modes
	}
	if req.DefaultMode != nil {
		existing.DefaultMode = *req.DefaultMode
	}
	if req.DefaultModel != nil {
		existing.DefaultModel = *req.DefaultModel
	}
	if req.MCPServers != nil {
		existing.MCPServers = *req.MCPServers
	}
	if req.ToolPermissions != nil {
		existing.ToolPermissions = *req.ToolPermissions
	}
	if req.CanExecute != nil {
		existing.CanExecute = *req.CanExecute
	}
	if req.Settings != nil {
		existing.Settings = *req.Settings
	}

	if err := a.Store.UpdateAgent(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleListAgentModes(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	modes, err := a.Store.ListAgentModes(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, modes)
}

func (a *API) handleListSessionAgents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agents, err := a.Store.ListSessionAgents(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}

func (a *API) handleAddSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req struct {
		AgentID string `json:"agent_id"`
		Role    string `json:"role"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	// Verify agent exists.
	if _, err := a.Store.GetAgent(req.AgentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	isPrimary := req.Role == "primary"
	mode := "default"

	// If setting a new primary, demote the current primary first.
	if isPrimary {
		if cur, err := a.Store.GetSessionPrimaryAgent(sessionID); err == nil {
			_ = a.Store.EnsureSessionAgent(sessionID, cur.AgentID, cur.Mode, false)
		}
	}

	if err := a.Store.EnsureSessionAgent(sessionID, req.AgentID, mode, isPrimary); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return the updated agents list.
	agents, err := a.Store.ListSessionAgents(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, agents)
}

func (a *API) handleRemoveSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agentID := r.PathValue("agentId")

	if err := a.Store.DeleteSessionAgent(sessionID, agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session agent not found")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (a *API) handleCreateAgentMode(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	// Verify agent exists.
	if _, err := a.Store.GetAgent(agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	var req struct {
		Slug           string `json:"slug"`
		Name           string `json:"name"`
		PromptAddendum string `json:"prompt_addendum"`
		ToolOverrides  string `json:"tool_overrides"`
		Settings       string `json:"settings"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Slug == "" || req.Name == "" || req.PromptAddendum == "" {
		a.errorResp(w, http.StatusBadRequest, "slug, name, and prompt_addendum are required")
		return
	}

	mode := &store.AgentMode{
		AgentID:        agentID,
		Slug:           req.Slug,
		Name:           req.Name,
		PromptAddendum: req.PromptAddendum,
		ToolOverrides:  req.ToolOverrides,
		Settings:       req.Settings,
	}
	if err := a.Store.CreateAgentMode(mode); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, mode)
}
