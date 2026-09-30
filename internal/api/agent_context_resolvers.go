package api

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md):
// DB-CRUD REST surface for an agent's cmd/http dynamic context
// resolvers — the API-only half of "build minimal DB-CRUD/API surface
// for defining a resolver on an agent." No GUI is wired in this pass
// (TASKS/phase-1/09-build-assignment-ui-api.md, the natural place to
// surface this in the assignment UI, is itself not-started as of this
// task — see this file's package doc / this task's Work Log for the
// judgment call).

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListAgentContextResolvers returns every resolver (enabled and
// disabled) bound to the agent.
// GET /api/agents/{id}/context-resolvers
func (a *API) handleListAgentContextResolvers(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.AgentCapabilities.ListContextResolvers(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, contextResolversToView(rows))
}

// handleCreateAgentContextResolver creates a new resolver bound to the
// agent.
// POST /api/agents/{id}/context-resolvers
func (a *API) handleCreateAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req CreateAgentContextResolverRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row := store.AgentContextResolver{
		AgentID:        agent.ID,
		SlotName:       req.SlotName,
		Kind:           req.Kind,
		Run:            req.Run,
		CWD:            req.CWD,
		Timeout:        req.Timeout,
		URL:            req.URL,
		HeadersJSON:    req.HeadersJSON,
		ResponseFormat: req.ResponseFormat,
		JSONPath:       req.JSONPath,
		Enabled:        enabled,
	}
	created, err := a.Services.AgentCapabilities.CreateContextResolver(r.Context(), row)
	if err != nil {
		var we *service.CapabilityWriteError
		if errors.As(err, &we) {
			a.errorResp(w, http.StatusBadRequest, we.Error())
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, contextResolverToView(created))
}

// ownedContextResolver reads the resolver in the path for the agent in the
// path. A missing resolver is a 404; one owned by a different agent is a 404
// too unless notOwnedMsg is set, in which case it is a 400 with that
// message. It reports whether the handler should continue.
func (a *API) ownedContextResolver(w http.ResponseWriter, r *http.Request, agentID, notOwnedMsg string) (*store.AgentContextResolver, bool) {
	row, err := a.Services.AgentCapabilities.OwnedContextResolver(r.Context(), agentID, r.PathValue("resolverId"))
	switch {
	case err == nil:
		return row, true
	case errors.Is(err, store.ErrAgentContextResolverNotFound):
		a.errorResp(w, http.StatusNotFound, "context resolver not found")
	case errors.Is(err, service.ErrContextResolverNotOwned):
		if notOwnedMsg == "" {
			a.errorResp(w, http.StatusNotFound, "context resolver not found")
		} else {
			a.errorResp(w, http.StatusBadRequest, notOwnedMsg)
		}
	default:
		a.errorResp(w, http.StatusInternalServerError, err.Error())
	}
	return nil, false
}

// handleGetAgentContextResolver returns a single resolver by id.
// GET /api/agents/{id}/context-resolvers/{resolverId}
func (a *API) handleGetAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	resolver, ok := a.ownedContextResolver(w, r, agent.ID, "")
	if !ok {
		return
	}
	a.jsonResp(w, http.StatusOK, contextResolverToView(resolver))
}

// handleUpdateAgentContextResolver patches an existing resolver's
// editable fields.
// PATCH /api/agents/{id}/context-resolvers/{resolverId}
func (a *API) handleUpdateAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	current, ok := a.ownedContextResolver(w, r, agent.ID, "cannot patch a different agent's context resolver through this endpoint")
	if !ok {
		return
	}

	var req UpdateAgentContextResolverRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	resolver, err := a.Services.AgentCapabilities.UpdateContextResolver(r.Context(), current, service.ContextResolverPatch{
		SlotName:       req.SlotName,
		Kind:           req.Kind,
		Run:            req.Run,
		CWD:            req.CWD,
		Timeout:        req.Timeout,
		URL:            req.URL,
		HeadersJSON:    req.HeadersJSON,
		ResponseFormat: req.ResponseFormat,
		JSONPath:       req.JSONPath,
		Enabled:        req.Enabled,
	})
	if err != nil {
		var we *service.CapabilityWriteError
		switch {
		case errors.Is(err, store.ErrAgentContextResolverNotFound):
			a.errorResp(w, http.StatusNotFound, "context resolver not found")
		case errors.As(err, &we):
			a.errorResp(w, http.StatusBadRequest, we.Error())
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a.jsonResp(w, http.StatusOK, contextResolverToView(resolver))
}

// handleDeleteAgentContextResolver deletes a resolver by id.
// DELETE /api/agents/{id}/context-resolvers/{resolverId}
func (a *API) handleDeleteAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	resolverID := r.PathValue("resolverId")
	if _, ok := a.ownedContextResolver(w, r, agent.ID, "cannot delete a different agent's context resolver through this endpoint"); !ok {
		return
	}
	if err := a.Services.AgentCapabilities.DeleteContextResolver(r.Context(), resolverID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"id": resolverID, "status": "deleted"})
}
