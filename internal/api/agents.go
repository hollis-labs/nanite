package api

import (
	"errors"
	"log/slog"
	"net/http"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentvalidation"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.Services.Agents.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The management surface (Admin > Agents) passes ?manageable=1 to exclude
	// embedded internal harness primitives, which are not user content. Other
	// consumers (chat picker, roster, command palette) get the full list so
	// the canonical Chat/Planner/Worker agents remain selectable.
	manageableOnly := r.URL.Query().Get("manageable") == "1" || r.URL.Query().Get("manageable") == "true"
	views := make([]AgentProfileView, 0, len(agents))
	for i := range agents {
		view := a.agentView(agents[i])
		if manageableOnly && view.ManageClass == string(agentpkg.ManageClassInternal) {
			continue
		}
		views = append(views, view)
	}
	a.jsonResp(w, http.StatusOK, views)
}

// agentView decorates a stored profile with management metadata (class,
// editability, file revision) for the GUI. nil-safe when AgentConfig is unset
// (lightweight test setups) — it falls back to source-only classification.
func (a *API) agentView(p store.AgentProfile) AgentProfileView {
	var class agentpkg.ManageClass
	revision := ""
	persisted := true
	if a.Services != nil && a.Services.AgentConfig != nil {
		class = a.Services.AgentConfig.Classify(&p)
		revision = a.Services.AgentConfig.Revision(&p)
		persisted = a.Services.AgentConfig.Persisted(&p)
	} else {
		class = agentpkg.Classification{}.Classify(p.Source, p.SourceRef)
	}
	return AgentProfileView{
		AgentProfile:  p,
		ManageClass:   string(class),
		Editable:      class.Editable(),
		CopyToManaged: class.CopyToManagedAllowed(),
		Revision:      revision,
		Persisted:     persisted,
	}
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
	// CW-20260512-0111: default omitted source to 'user' so callers that
	// don't explicitly tag provenance don't insert an empty string (the
	// error message below previously claimed "or omit" was equivalent to
	// source='user', but the handler didn't actually default it — leaving
	// an empty source column on the row).
	if req.Source == "" {
		req.Source = "user"
	}
	// source='internal' is reserved for file-sourced internal profiles
	// (boot sync from internal/agent/builtin/profiles/). API-created agents
	// cannot claim that provenance.
	if req.Source == "internal" {
		a.errorResp(w, http.StatusBadRequest, "source='internal' is reserved for file-sourced internal profiles; use source='user' (the default) or another non-reserved value")
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
		RoleTools:               req.RoleTools,
		RoleSkills:              req.RoleSkills,
		ContextPolicy:           req.ContextPolicy,
		Durable:                 req.Durable,
		ActivationMode:          req.ActivationMode,
		Class:                   req.Class,
		DefaultState:            req.DefaultState,
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

	res, err := a.Services.AgentConfig.Create(agent, nil)
	if err != nil {
		if errors.Is(err, service.ErrManagedSlugExists) {
			a.errorResp(w, http.StatusConflict, err.Error())
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	view := a.agentView(*res.Profile)
	a.jsonResp(w, http.StatusCreated, view)
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
		"agent": a.agentView(*ag),
		"modes": modes,
	})
}

func (a *API) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Resolve through the AgentService so both stamped managed agents (real
	// UUID) and legacy "file-<slug>" identities resolve, and the in-memory
	// def's SourceRef/Source drive classification.
	existing, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	// Editability gate. Managed file-backed agents are writable in place;
	// embedded internal and plugin/vendor agents are not — but instead of a
	// dead-end we tell the client whether a copy-to-managed path is offered.
	if class := a.Services.AgentConfig.Classify(existing); !class.Editable() {
		a.writeNotManaged(w, existing, class)
		return
	}

	// Snapshot the pre-edit profile so the write service has the original
	// identity (ID), file path (SourceRef), and provenance to drive the
	// in-place rewrite, rename, and optimistic-concurrency baseline.
	original := *existing

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
	if req.RoleTools != nil {
		existing.RoleTools = *req.RoleTools
	}
	if req.RoleSkills != nil {
		existing.RoleSkills = *req.RoleSkills
	}
	if req.ContextPolicy != nil {
		existing.ContextPolicy = *req.ContextPolicy
	}
	if req.Durable != nil {
		existing.Durable = *req.Durable
	}
	if req.ActivationMode != nil {
		existing.ActivationMode = *req.ActivationMode
	}
	if req.Class != nil {
		existing.Class = *req.Class
	}
	if req.DefaultState != nil {
		existing.DefaultState = *req.DefaultState
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

	// Preserve the file's procedures across a profile edit — they are managed
	// through the dedicated capability endpoints, not the profile body.
	var procedures []agentpkg.ProcedureDefinition
	if original.SourceRef != "" {
		if def, err := agentpkg.ParseMDFile(original.SourceRef); err == nil {
			procedures = def.Procedures
		}
	}
	res, err := a.Services.AgentConfig.Update(&original, existing, procedures, req.Revision)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAgentRevisionConflict):
			a.errorResp(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrAgentNotManaged):
			a.writeNotManaged(w, &original, a.Services.AgentConfig.Classify(&original))
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, a.agentView(*res.Profile))
}

// writeNotManaged emits the standard 409 response for an attempt to mutate a
// read-only agent, telling the client the management class, the file ref, and
// whether a copy-to-managed ("make editable") path is offered instead of a
// dead-end.
func (a *API) writeNotManaged(w http.ResponseWriter, ag *store.AgentProfile, class agentpkg.ManageClass) {
	msg := "agent is not a writable managed config"
	switch class {
	case agentpkg.ManageClassInternal:
		msg = "agent is an embedded internal harness profile and is managed by Nanite, not editable here"
	case agentpkg.ManageClassPlugin:
		msg = "agent is plugin/vendor-provided (read-only); copy it to the managed layer to edit"
	case agentpkg.ManageClassExternal:
		msg = "agent is not in a writable managed location (read-only); copy it to the managed layer to edit"
	}
	a.jsonResp(w, http.StatusConflict, map[string]any{
		"error":           "agent_not_managed",
		"message":         msg,
		"manage_class":    string(class),
		"copy_to_managed": class.CopyToManagedAllowed(),
		"source_ref":      ag.SourceRef,
		"slug":            ag.Slug,
	})
}

// handleDeleteAgent removes a managed file-backed agent: the file, the DB
// projection (and its FK children), and the live registry entry. Read-only
// sources (internal/plugin/external) are rejected. The GUI gates this behind
// an irreversible-confirmation dialog.
func (a *API) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	if class := a.Services.AgentConfig.Classify(existing); !class.Editable() {
		a.writeNotManaged(w, existing, class)
		return
	}
	if err := a.Services.AgentConfig.Delete(existing); err != nil {
		if errors.Is(err, service.ErrAgentNotManaged) {
			a.writeNotManaged(w, existing, a.Services.AgentConfig.Classify(existing))
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted", "slug": existing.Slug})
}

// handleCopyAgentToManaged forks a read-only agent (plugin/vendor/external)
// into a fresh editable managed config with a new identity ("make editable").
func (a *API) handleCopyAgentToManaged(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	source, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	res, err := a.Services.AgentConfig.CopyToManaged(source, nil)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAgentAlreadyManaged):
			a.errorResp(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrAgentNotIngested):
			// Distinct from ErrAgentAlreadyManaged: the file classifies as a
			// managed config but has no backing agent_profiles row (ingestion
			// failed at startup). 409 would misreport it as a working config.
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		case errors.Is(err, service.ErrManagedSlugExists):
			a.errorResp(w, http.StatusConflict, err.Error())
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusCreated, a.agentView(*res.Profile))
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
