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
	rows, err := a.Services.Store.ListAgentContextResolvers(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
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
	id, err := a.Services.Store.InsertAgentContextResolver(r.Context(), row)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentContextResolver(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
}

// handleGetAgentContextResolver returns a single resolver by id.
// GET /api/agents/{id}/context-resolvers/{resolverId}
func (a *API) handleGetAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	resolver, err := a.Services.Store.GetAgentContextResolver(r.Context(), r.PathValue("resolverId"))
	if err != nil {
		if errors.Is(err, store.ErrAgentContextResolverNotFound) {
			a.errorResp(w, http.StatusNotFound, "context resolver not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resolver.AgentID != agent.ID {
		a.errorResp(w, http.StatusNotFound, "context resolver not found")
		return
	}
	a.jsonResp(w, http.StatusOK, resolver)
}

// handleUpdateAgentContextResolver patches an existing resolver's
// editable fields.
// PATCH /api/agents/{id}/context-resolvers/{resolverId}
func (a *API) handleUpdateAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	resolverID := r.PathValue("resolverId")
	current, err := a.Services.Store.GetAgentContextResolver(r.Context(), resolverID)
	if err != nil {
		if errors.Is(err, store.ErrAgentContextResolverNotFound) {
			a.errorResp(w, http.StatusNotFound, "context resolver not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "cannot patch a different agent's context resolver through this endpoint")
		return
	}

	var req UpdateAgentContextResolverRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	updated := *current
	if req.SlotName != nil {
		updated.SlotName = *req.SlotName
	}
	if req.Kind != nil {
		updated.Kind = *req.Kind
	}
	if req.Run != nil {
		updated.Run = *req.Run
	}
	if req.CWD != nil {
		updated.CWD = *req.CWD
	}
	if req.Timeout != nil {
		updated.Timeout = *req.Timeout
	}
	if req.URL != nil {
		updated.URL = *req.URL
	}
	if req.HeadersJSON != nil {
		updated.HeadersJSON = *req.HeadersJSON
	}
	if req.ResponseFormat != nil {
		updated.ResponseFormat = *req.ResponseFormat
	}
	if req.JSONPath != nil {
		updated.JSONPath = *req.JSONPath
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}

	if err := a.Services.Store.UpdateAgentContextResolver(r.Context(), updated); err != nil {
		if errors.Is(err, store.ErrAgentContextResolverNotFound) {
			a.errorResp(w, http.StatusNotFound, "context resolver not found")
			return
		}
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	resolver, err := a.Services.Store.GetAgentContextResolver(r.Context(), resolverID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, resolver)
}

// handleDeleteAgentContextResolver deletes a resolver by id.
// DELETE /api/agents/{id}/context-resolvers/{resolverId}
func (a *API) handleDeleteAgentContextResolver(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	resolverID := r.PathValue("resolverId")
	resolver, err := a.Services.Store.GetAgentContextResolver(r.Context(), resolverID)
	if err != nil {
		if errors.Is(err, store.ErrAgentContextResolverNotFound) {
			a.errorResp(w, http.StatusNotFound, "context resolver not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resolver.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "cannot delete a different agent's context resolver through this endpoint")
		return
	}
	if err := a.Services.Store.DeleteAgentContextResolver(r.Context(), resolverID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"id": resolverID, "status": "deleted"})
}
