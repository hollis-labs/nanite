package api

import (
	"net/http"
	"os"
	"strings"

	svcerr "github.com/hollis-labs/libs/core/util/svcerr"
	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/safego"

	"github.com/hollis-labs/nanite/internal/store"
)

// visibleAgentSlugs reads NANITE_AGENT_SLUGS — a comma-separated allowlist that
// narrows what GET /api/agents returns.
//
// Unset means unchanged behavior: every agent is listed. Set, only those slugs
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
		a.serviceError(w, r, err)
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
// the GUI. Revision identifies the complete persisted profile. nil-safe when AgentConfig is unset
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
	return agentProfileToView(&p, agentViewMeta{
		ManageClass:   string(class),
		Editable:      false,
		CopyToManaged: false,
		Revision:      revision,
		Persisted:     persisted,
	})
}

// Phase 0 item 21 ("Cut Modes, in full") removed the "modes" key this
// response used to carry (a.Services.Agents.ListModes — Legacy Agent Mode
// is gone). The frontend's AgentProfileManager.tsx / AgentDetailView.tsx
// "Modes" tab were updated in lock-step to stop expecting it.
func (a *API) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ag, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"agent": a.agentView(*ag),
	})
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
	a.jsonResp(w, svcerr.StatusFor(svcerr.New(svcerr.CodePermission, msg, svcerr.WithStatus(http.StatusConflict)), http.StatusInternalServerError), map[string]any{
		"error":           "agent_not_managed",
		"message":         msg,
		"manage_class":    string(class),
		"copy_to_managed": class.CopyToManagedAllowed(),
		"source_ref":      ag.SourceRef,
		"slug":            ag.Slug,
	})
}

func (a *API) handleListSessionAgents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agents, err := a.Services.AgentMembership.ListSessionAgents(r.Context(), sessionID)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, sessionAgentsToView(agents))
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
	if _, err := a.Services.Agents.Get(r.Context(), req.AgentID); err != nil {
		a.serviceError(w, r, err)
		return
	}

	isPrimary := req.Role == "primary"
	mode := "default"

	// Setting a new primary demotes the current one; the previous agent ID
	// comes back so we can emit agent.switched.
	previousAgentID, err := a.Services.AgentMembership.SetSessionAgent(r.Context(), sessionID, req.AgentID, mode, isPrimary)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}

	// Emit agent.switched plugin event when primary changes to a different agent.
	if isPrimary && previousAgentID != "" && previousAgentID != req.AgentID && a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.agents.emit.agent-switched", func() {
			a.Services.Plugins.EmitAgentSwitched(sessionID, previousAgentID, req.AgentID)
		})
	}

	// Return the updated agents list.
	agents, err := a.Services.AgentMembership.ListSessionAgents(r.Context(), sessionID)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, sessionAgentsToView(agents))
}

func (a *API) handleRemoveSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	agentID := r.PathValue("agentId")

	if err := a.Services.AgentMembership.RemoveSessionAgent(r.Context(), sessionID, agentID); err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

// --- Agent ↔ Project many-to-many ---

func (a *API) handleListAgentProjects(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	projects, err := a.Services.AgentMembership.ListAgentProjects(r.Context(), agentID)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, projectsToView(projects))
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
	if _, err := a.Services.Agents.Get(r.Context(), agentID); err != nil {
		a.serviceError(w, r, err)
		return
	}

	if err := a.Services.AgentMembership.AddAgentProject(r.Context(), agentID, req.ProjectID); err != nil {
		a.serviceError(w, r, err)
		return
	}

	projects, err := a.Services.AgentMembership.ListAgentProjects(r.Context(), agentID)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusCreated, projectsToView(projects))
}

func (a *API) handleRemoveAgentProject(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	projectID := r.PathValue("projectId")

	if err := a.Services.AgentMembership.RemoveAgentProject(r.Context(), agentID, projectID); err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (a *API) handleListProjectAgents(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	agents, err := a.Services.AgentMembership.ListProjectAgents(r.Context(), projectID)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	views := make([]AgentProfileView, 0, len(agents))
	for i := range agents {
		views = append(views, a.agentView(agents[i]))
	}
	a.jsonResp(w, http.StatusOK, views)
}

// Historical mutable profile authoring is retired. Explicit historical export
// and audited retirement remain separate supported handlers.
func (a *API) handleRetiredAgentMutation(w http.ResponseWriter, r *http.Request) {
	a.errorResp(w, http.StatusGone, "Mutable profile operations are retired; author a pinned definition and configure host settings")
}
