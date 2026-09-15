package api

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentvalidation"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// visibleAgentSlugs reads NANITE_AGENT_SLUGS — a comma-separated allowlist that
// narrows what GET /api/agents returns.
//
// Unset means unchanged behaviour: every agent is listed. Set, only those slugs
// are, which is how a demo shows two agents instead of eleven without deleting
// anything. It filters the LISTING only — dispatch, the runtime and every other
// caller read the store directly and are untouched, so a hidden agent still
// works if something addresses it by slug.
func visibleAgentSlugs() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("NANITE_AGENT_SLUGS"))
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out[s] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

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
	// Optional operator allowlist — see visibleAgentSlugs. Listing only; every
	// other path reads the store directly.
	visible := visibleAgentSlugs()
	views := make([]AgentProfileView, 0, len(agents))
	for i := range agents {
		view := a.agentView(agents[i])
		if manageableOnly && view.ManageClass == string(agentpkg.ManageClassInternal) {
			continue
		}
		if visible != nil && !visible[agents[i].Slug] {
			continue
		}
		views = append(views, view)
	}
	a.jsonResp(w, http.StatusOK, views)
}

// agentView decorates a stored profile with database ownership metadata for
// the GUI. Revision remains empty for wire compatibility. nil-safe when AgentConfig is unset
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
		class = agentpkg.Classification{}.Classify(p.Source)
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
	// source='internal' is reserved for compiled-in internal profiles
	// (boot sync from internal/agent/builtin/profiles/). API-created agents
	// cannot claim that provenance.
	if req.Source == "internal" {
		a.errorResp(w, http.StatusBadRequest, "source='internal' is reserved for compiled-in internal profiles; use source='user' (the default) or another non-reserved value")
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

	// role_id/consumer_id/model_id use the composition writer so pointer/FK
	// validation remains shared with the assignment API.
	if req.RoleID != "" || req.ConsumerID != "" || req.ModelID != "" {
		if err := a.Services.Store.UpdateAgentComposition(r.Context(), res.Profile.ID, ptrOrNilString(req.RoleID), ptrOrNilString(req.ConsumerID), ptrOrNilString(req.ModelID)); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		if refreshed, err := a.Services.Store.GetAgent(r.Context(), res.Profile.ID); err == nil {
			res.Profile = refreshed
		}
	}

	// protocol/transport (TASKS/agent-host-acp/11) -- same DB-only,
	// zero-frontmatter-representation shape as role_id/consumer_id/
	// model_id immediately above.
	if req.Protocol != "" || req.Transport != "" {
		if err := a.Services.Store.UpdateAgentACPConfig(r.Context(), res.Profile.ID, ptrOrNilString(req.Protocol), ptrOrNilString(req.Transport)); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		if refreshed, err := a.Services.Store.GetAgent(r.Context(), res.Profile.ID); err == nil {
			res.Profile = refreshed
		}
	}

	view := a.agentView(*res.Profile)
	a.jsonResp(w, http.StatusCreated, view)
}

// ptrOrNilString returns nil for an empty string, or a pointer to v
// otherwise. Used for CreateAgentRequest's plain (non-pointer) RoleID/
// ConsumerID/ModelID fields, whose "not provided" and "explicitly empty"
// cases are indistinguishable on create (matching every other plain-string
// field on that struct) -- an empty value here is simply "don't set this
// column," not "clear an existing one" (there is nothing to clear yet on a
// freshly created agent).
func ptrOrNilString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// Phase 0 item 21 ("Cut Modes, in full") removed the "modes" key this
// response used to carry (a.Services.Agents.ListModes — Legacy Agent Mode
// is gone). The frontend's AgentProfileManager.tsx / AgentDetailView.tsx
// "Modes" tab were updated in lock-step to stop expecting it.
func (a *API) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ag, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"agent": a.agentView(*ag),
	})
}

func (a *API) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Resolve through the AgentService; the stored provenance drives the
	// database ownership classification below.
	existing, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	// Editability gate. Operator-owned agents are writable in place; embedded
	// internal and plugin/vendor agents are not — but instead of a
	// dead-end we tell the client whether a copy-to-managed path is offered.
	if class := a.Services.AgentConfig.Classify(existing); !class.Editable() {
		a.writeNotManaged(w, existing, class)
		return
	}

	// Snapshot the pre-edit profile so the write service has the original
	// identity and provenance.
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

	// Procedures are relational capability rows and are untouched by profile
	// edits; no filesystem round-trip is needed to preserve them.
	res, err := a.Services.AgentConfig.Update(&original, existing, nil, req.Revision)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAgentNotManaged):
			a.writeNotManaged(w, &original, a.Services.AgentConfig.Classify(&original))
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// role_id/consumer_id/model_id -- see handleCreateAgent's matching
	// comment. Pointer semantics here (nil = untouched, non-nil = set or
	// clear) match every other partial-update field on UpdateAgentRequest.
	if req.RoleID != nil || req.ConsumerID != nil || req.ModelID != nil {
		if err := a.Services.Store.UpdateAgentComposition(r.Context(), res.Profile.ID, req.RoleID, req.ConsumerID, req.ModelID); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		if refreshed, err := a.Services.Store.GetAgent(r.Context(), res.Profile.ID); err == nil {
			res.Profile = refreshed
		}
	}

	// protocol/transport -- see handleCreateAgent's matching comment.
	if req.Protocol != nil || req.Transport != nil {
		if err := a.Services.Store.UpdateAgentACPConfig(r.Context(), res.Profile.ID, req.Protocol, req.Transport); err != nil {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		if refreshed, err := a.Services.Store.GetAgent(r.Context(), res.Profile.ID); err == nil {
			res.Profile = refreshed
		}
	}

	a.jsonResp(w, http.StatusOK, a.agentView(*res.Profile))
}

// writeNotManaged emits the standard 409 response for an attempt to mutate a
// read-only agent, telling the client the management class, provenance, and
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
		msg = "agent has external/imported provenance (read-only); copy it to the managed layer to edit"
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

// handleDeleteAgent removes an operator-managed database profile and its FK
// children. Read-only
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

// handleCopyAgentToManaged forks a plugin/vendor/external agent into a fresh
// editable managed config with a new identity ("make editable"). Internal
// harness profiles are read-only but deliberately not copyable.
func (a *API) handleCopyAgentToManaged(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	source, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	class := a.Services.AgentConfig.Classify(source)
	if !class.CopyToManagedAllowed() {
		if class.Editable() {
			a.errorResp(w, http.StatusConflict, service.ErrAgentAlreadyManaged.Error())
		} else {
			a.writeNotManaged(w, source, class)
		}
		return
	}
	res, err := a.Services.AgentConfig.CopyToManaged(source, nil)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAgentAlreadyManaged):
			a.errorResp(w, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrAgentNotManaged):
			a.writeNotManaged(w, source, class)
		case errors.Is(err, service.ErrManagedSlugExists):
			a.errorResp(w, http.StatusConflict, err.Error())
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusCreated, a.agentView(*res.Profile))
}

func (a *API) handleListSessionAgents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agents, err := a.Services.Store.ListSessionAgents(r.Context(), sessionID)
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
	if _, err := a.Services.Store.GetAgent(r.Context(), req.AgentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	isPrimary := req.Role == "primary"
	mode := "default"

	// If setting a new primary, demote the current primary first and capture
	// the previous agent ID so we can emit agent.switched.
	var previousAgentID string
	if isPrimary {
		if cur, err := a.Services.Store.GetSessionPrimaryAgent(r.Context(), sessionID); err == nil {
			previousAgentID = cur.AgentID
			_ = a.Services.Store.EnsureSessionAgent(r.Context(), sessionID, cur.AgentID, cur.Mode, false)
		}
	}

	if err := a.Services.Store.EnsureSessionAgent(r.Context(), sessionID, req.AgentID, mode, isPrimary); err != nil {
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
	agents, err := a.Services.Store.ListSessionAgents(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, agents)
}

func (a *API) handleRemoveSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agentID := r.PathValue("agentId")

	if err := a.Services.Store.DeleteSessionAgent(r.Context(), sessionID, agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "session agent not found")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

// --- Agent ↔ Project many-to-many ---

func (a *API) handleListAgentProjects(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	projects, err := a.Services.Store.ListAgentProjects(r.Context(), agentID)
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

	// Verify agent exists as a real agent_profiles DB row. Previously there
	// was no check here at all -- agent_projects.agent_id now carries a real
	// FK to agent_profiles(id) (Phase 1 #05), so reject up front rather than
	// letting the INSERT fail deeper in the store layer.
	if _, err := a.Services.Store.GetAgent(r.Context(), agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}

	if err := a.Services.Store.AddAgentProject(r.Context(), agentID, req.ProjectID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	projects, err := a.Services.Store.ListAgentProjects(r.Context(), agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, projects)
}

func (a *API) handleRemoveAgentProject(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	projectID := r.PathValue("projectId")

	if err := a.Services.Store.RemoveAgentProject(r.Context(), agentID, projectID); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (a *API) handleListProjectAgents(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	agents, err := a.Services.Store.ListProjectAgents(r.Context(), projectID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, agents)
}
