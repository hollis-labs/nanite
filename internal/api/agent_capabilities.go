package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// capabilityError writes the response for an AgentCapabilitiesService error.
// notFound is the store sentinel that maps to 404 for this call, or nil where
// a missing row is not a client error (the re-read after a create). A store
// write rejection is 400 with the store's message, a duplicate create 409,
// anything else 500.
func (a *API) capabilityError(w http.ResponseWriter, err, notFound error, notFoundMsg, existsMsg string) {
	var writeErr *service.CapabilityWriteError
	switch {
	case notFound != nil && errors.Is(err, notFound):
		a.errorResp(w, http.StatusNotFound, notFoundMsg)
	case errors.Is(err, service.ErrCapabilityExists):
		a.errorResp(w, http.StatusConflict, existsMsg)
	case errors.As(err, &writeErr):
		a.errorResp(w, http.StatusBadRequest, err.Error())
	default:
		a.errorResp(w, http.StatusInternalServerError, err.Error())
	}
}

func (a *API) handleListAgentKnownTools(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.AgentCapabilities.ListKnownTools(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, knownToolsToView(rows))
}

func (a *API) handleGetAgentKnownTool(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	toolName := r.PathValue("toolName")
	if toolName == "" {
		a.errorResp(w, http.StatusBadRequest, "toolName is required")
		return
	}
	row, err := a.Services.AgentCapabilities.GetKnownTool(r.Context(), agent.ID, toolName)
	if err != nil {
		a.capabilityError(w, err, store.ErrAgentKnownToolNotFound, "known tool not found", "")
		return
	}
	a.jsonResp(w, http.StatusOK, knownToolToView(row))
}

func (a *API) handleCreateAgentKnownTool(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req AgentKnownToolUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.ToolName == "" {
		a.errorResp(w, http.StatusBadRequest, "tool_name is required")
		return
	}
	created, err := a.Services.AgentCapabilities.CreateKnownTool(r.Context(), agent.ID, req.ToolName, service.KnownToolInput{
		Pinned:     req.Pinned,
		SortOrder:  req.SortOrder,
		TTLSeconds: req.TTLSeconds,
		Reason:     req.Reason,
	})
	if err != nil {
		a.capabilityError(w, err, nil, "", "known tool already exists")
		return
	}
	a.jsonResp(w, http.StatusCreated, knownToolToView(created))
}

func (a *API) handleUpdateAgentKnownTool(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	toolName := r.PathValue("toolName")
	if toolName == "" {
		a.errorResp(w, http.StatusBadRequest, "toolName is required")
		return
	}
	// A missing row is reported before the body is read.
	if _, err := a.Services.AgentCapabilities.GetKnownTool(r.Context(), agent.ID, toolName); err != nil {
		a.capabilityError(w, err, store.ErrAgentKnownToolNotFound, "known tool not found", "")
		return
	}
	var req AgentKnownToolUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.ToolName != "" && req.ToolName != toolName {
		a.errorResp(w, http.StatusBadRequest, "tool_name in body must match path")
		return
	}
	updated, err := a.Services.AgentCapabilities.UpdateKnownTool(r.Context(), agent.ID, toolName, service.KnownToolInput{
		Pinned:     req.Pinned,
		SortOrder:  req.SortOrder,
		TTLSeconds: req.TTLSeconds,
		Reason:     req.Reason,
	})
	if err != nil {
		a.capabilityError(w, err, nil, "", "")
		return
	}
	a.jsonResp(w, http.StatusOK, knownToolToView(updated))
}

func (a *API) handleDeleteAgentKnownTool(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	toolName := r.PathValue("toolName")
	if toolName == "" {
		a.errorResp(w, http.StatusBadRequest, "toolName is required")
		return
	}
	if err := a.Services.AgentCapabilities.DeleteKnownTool(r.Context(), agent.ID, toolName); err != nil {
		a.capabilityError(w, err, store.ErrAgentKnownToolNotFound, "known tool not found", "")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleListAgentKnownSkills(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.AgentCapabilities.ListKnownSkills(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, knownSkillsToView(rows))
}

func (a *API) handleGetAgentKnownSkill(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	skillName := r.PathValue("skillName")
	if skillName == "" {
		a.errorResp(w, http.StatusBadRequest, "skillName is required")
		return
	}
	row, err := a.Services.AgentCapabilities.GetKnownSkill(r.Context(), agent.ID, skillName)
	if err != nil {
		a.capabilityError(w, err, store.ErrAgentKnownSkillNotFound, "known skill not found", "")
		return
	}
	a.jsonResp(w, http.StatusOK, knownSkillToView(row))
}

// handleCreateAgentKnownSkill upserts onto a bare assignment row left by
// POST /api/agents/{id}/skills rather than conflicting with it; see
// AgentCapabilitiesService.CreateKnownSkill.
func (a *API) handleCreateAgentKnownSkill(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req AgentKnownSkillUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.SkillName == "" {
		a.errorResp(w, http.StatusBadRequest, "skill_name is required")
		return
	}
	created, err := a.Services.AgentCapabilities.CreateKnownSkill(r.Context(), agent.ID, req.SkillName, service.KnownSkillInput{
		Pinned:     req.Pinned,
		TTLSeconds: req.TTLSeconds,
		Reason:     req.Reason,
	})
	if err != nil {
		a.capabilityError(w, err, nil, "", "known skill already exists")
		return
	}
	a.jsonResp(w, http.StatusCreated, knownSkillToView(created))
}

// handleUpdateAgentKnownSkill edits pinned/ttl/reason only; the service
// carries the grant state forward so a field edit cannot revoke a grant.
func (a *API) handleUpdateAgentKnownSkill(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	skillName := r.PathValue("skillName")
	if skillName == "" {
		a.errorResp(w, http.StatusBadRequest, "skillName is required")
		return
	}
	// A missing row is reported before the body is read.
	if _, err := a.Services.AgentCapabilities.GetKnownSkill(r.Context(), agent.ID, skillName); err != nil {
		a.capabilityError(w, err, store.ErrAgentKnownSkillNotFound, "known skill not found", "")
		return
	}
	var req AgentKnownSkillUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.SkillName != "" && req.SkillName != skillName {
		a.errorResp(w, http.StatusBadRequest, "skill_name in body must match path")
		return
	}
	updated, err := a.Services.AgentCapabilities.UpdateKnownSkill(r.Context(), agent.ID, skillName, service.KnownSkillInput{
		Pinned:     req.Pinned,
		TTLSeconds: req.TTLSeconds,
		Reason:     req.Reason,
	})
	if err != nil {
		a.capabilityError(w, err, nil, "", "")
		return
	}
	a.jsonResp(w, http.StatusOK, knownSkillToView(updated))
}

func (a *API) handleDeleteAgentKnownSkill(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	skillName := r.PathValue("skillName")
	if skillName == "" {
		a.errorResp(w, http.StatusBadRequest, "skillName is required")
		return
	}
	if err := a.Services.AgentCapabilities.DeleteKnownSkill(r.Context(), agent.ID, skillName); err != nil {
		a.capabilityError(w, err, store.ErrAgentKnownSkillNotFound, "known skill not found", "")
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) requireAgent(w http.ResponseWriter, r *http.Request) (*store.AgentProfile, bool) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "agent id is required")
		return nil, false
	}
	agent, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.serviceError(w, r, err)
		return nil, false
	}
	return agent, true
}

// requireMutableAgent resolves the agent and enforces the managed-editability
// gate used by all capability/reflex write endpoints. Operator-owned database
// agents are writable in place (their reflexes, known tools/skills,
// procedures, and knowledge seeds persist against the profile UUID).
// Embedded internal and plugin/vendor agents are
// rejected with a copy-to-managed affordance rather than a dead-end.
func (a *API) requireMutableAgent(w http.ResponseWriter, r *http.Request) (*store.AgentProfile, bool) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return nil, false
	}
	if class := a.Services.AgentConfig.Classify(agent); !class.Editable() {
		a.writeNotManaged(w, agent, class)
		return nil, false
	}
	return agent, true
}
