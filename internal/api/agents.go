package api

import (
	"log/slog"
	"net/http"

	"github.com/hollis-labs/nanite/internal/agentvalidation"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.Services.Agents.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}

func (a *API) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req CreateAgentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" || req.SystemPrompt == "" {
		a.errorResp(w, http.StatusBadRequest, "name, slug, and system_prompt are required")
		return
	}

	agent := &store.AgentProfile{
		ID:                      req.ID,
		Name:                    req.Name,
		Slug:                    req.Slug,
		Avatar:                  req.Avatar,
		SystemPrompt:            req.SystemPrompt,
		Description:             req.Description,
		Modes:                   req.Modes,
		DefaultMode:             req.DefaultMode,
		DefaultModel:            req.DefaultModel,
		MCPServers:              req.MCPServers,
		ToolPermissions:         req.ToolPermissions,
		CanExecute:              req.CanExecute,
		Settings:                req.Settings,
		Tools:                   req.Tools,
		Directories:             req.Directories,
		Constraints:             req.Constraints,
		Tags:                    req.Tags,
		Status:                  req.Status,
		Source:                  req.Source,
		SourceRef:               req.SourceRef,
		Icon:                    req.Icon,
		ParentDispatchAllowlist: req.ParentDispatchAllowlist,
	}
	// Validate agent config before persisting.
	if vr := agentvalidation.ValidateAgentConfig(agent); !vr.OK() {
		a.jsonResp(w, http.StatusBadRequest, map[string]any{
			"error":    "validation_failed",
			"details":  vr.Errors,
			"warnings": vr.Warnings,
		})
		return
	} else if len(vr.Warnings) > 0 {
		for _, w := range vr.Warnings {
			slog.Warn("agent config warning", "slug", agent.Slug, "warning", w)
		}
	}

	if err := a.Services.Store.CreateAgent(agent); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, agent)
}

func (a *API) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ag, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	modes, err := a.Services.Agents.ListModes(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"agent": ag,
		"modes": modes,
	})
}

func (a *API) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetAgent(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	var req UpdateAgentRequest
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
	if req.Tools != nil {
		existing.Tools = *req.Tools
	}
	if req.Directories != nil {
		existing.Directories = *req.Directories
	}
	if req.Constraints != nil {
		existing.Constraints = *req.Constraints
	}
	if req.Tags != nil {
		existing.Tags = *req.Tags
	}
	if req.Status != nil {
		existing.Status = *req.Status
	}
	if req.Icon != nil {
		existing.Icon = *req.Icon
	}
	if req.ParentDispatchAllowlist != nil {
		existing.ParentDispatchAllowlist = *req.ParentDispatchAllowlist
	}

	// Validate agent config before persisting.
	if vr := agentvalidation.ValidateAgentConfig(existing); !vr.OK() {
		a.jsonResp(w, http.StatusBadRequest, map[string]any{
			"error":    "validation_failed",
			"details":  vr.Errors,
			"warnings": vr.Warnings,
		})
		return
	} else if len(vr.Warnings) > 0 {
		for _, w := range vr.Warnings {
			slog.Warn("agent config warning", "slug", existing.Slug, "warning", w)
		}
	}

	if err := a.Services.Store.UpdateAgent(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

func (a *API) handleListAgentModes(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	modes, err := a.Services.Store.ListAgentModes(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, modes)
}

func (a *API) handleListSessionAgents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agents, err := a.Services.Store.ListSessionAgents(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}

func (a *API) handleAddSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req AddSessionAgentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	// Verify agent exists.
	if _, err := a.Services.Store.GetAgent(req.AgentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	isPrimary := req.Role == "primary"
	mode := "default"

	// If setting a new primary, demote the current primary first and capture
	// the previous agent ID so we can emit agent.switched.
	var previousAgentID string
	if isPrimary {
		if cur, err := a.Services.Store.GetSessionPrimaryAgent(sessionID); err == nil {
			previousAgentID = cur.AgentID
			_ = a.Services.Store.EnsureSessionAgent(sessionID, cur.AgentID, cur.Mode, false)
		}
	}

	if err := a.Services.Store.EnsureSessionAgent(sessionID, req.AgentID, mode, isPrimary); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit agent.switched plugin event when primary changes to a different agent.
	if isPrimary && previousAgentID != "" && previousAgentID != req.AgentID && a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.agents.emit.agent-switched", func() {
			a.Services.Plugins.EmitAgentSwitched(sessionID, previousAgentID, req.AgentID)
		})
	}

	// Return the updated agents list.
	agents, err := a.Services.Store.ListSessionAgents(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, agents)
}

func (a *API) handleRemoveSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agentID := r.PathValue("agentId")

	if err := a.Services.Store.DeleteSessionAgent(sessionID, agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session agent not found")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

// --- Agent ↔ Project many-to-many ---

func (a *API) handleListAgentProjects(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	projects, err := a.Services.Store.ListAgentProjects(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, projects)
}

func (a *API) handleAddAgentProject(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	var req AddAgentProjectRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ProjectID == "" {
		a.errorResp(w, http.StatusBadRequest, "project_id is required")
		return
	}

	if err := a.Services.Store.AddAgentProject(agentID, req.ProjectID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	projects, err := a.Services.Store.ListAgentProjects(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, projects)
}

func (a *API) handleRemoveAgentProject(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	projectID := r.PathValue("projectId")

	if err := a.Services.Store.RemoveAgentProject(agentID, projectID); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (a *API) handleListProjectAgents(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	agents, err := a.Services.Store.ListProjectAgents(projectID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}

func (a *API) handleCreateAgentMode(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	// Verify agent exists.
	if _, err := a.Services.Store.GetAgent(agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	var req CreateAgentModeRequest
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
	if err := a.Services.Store.CreateAgentMode(mode); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, mode)
}
