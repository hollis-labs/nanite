package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListAgentKnownTools(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.Store.ListAgentKnownTools(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
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
	row, err := a.Services.Store.GetAgentKnownTool(r.Context(), agent.ID, toolName)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnownToolNotFound) {
			a.errorResp(w, http.StatusNotFound, "known tool not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
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
	if existing, err := a.Services.Store.GetAgentKnownTool(r.Context(), agent.ID, req.ToolName); err == nil && existing != nil {
		a.errorResp(w, http.StatusConflict, "known tool already exists")
		return
	} else if err != nil && !errors.Is(err, store.ErrAgentKnownToolNotFound) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := store.AgentKnownTool{
		AgentID:    agent.ID,
		ToolName:   req.ToolName,
		Pinned:     req.Pinned,
		SortOrder:  req.SortOrder,
		TTLSeconds: req.TTLSeconds,
		Reason:     req.Reason,
	}
	if err := a.Services.Store.InsertAgentKnownTool(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentKnownTool(r.Context(), agent.ID, req.ToolName)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
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
	current, err := a.Services.Store.GetAgentKnownTool(r.Context(), agent.ID, toolName)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnownToolNotFound) {
			a.errorResp(w, http.StatusNotFound, "known tool not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
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
	row := store.AgentKnownTool{
		AgentID:         agent.ID,
		ToolName:        toolName,
		Pinned:          req.Pinned,
		SortOrder:       req.SortOrder,
		ActivationCount: current.ActivationCount,
		LastUsedAt:      current.LastUsedAt,
		AddedAt:         current.AddedAt,
		TTLSeconds:      req.TTLSeconds,
		Reason:          req.Reason,
	}
	if err := a.Services.Store.InsertAgentKnownTool(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.Services.Store.GetAgentKnownTool(r.Context(), agent.ID, toolName)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, updated)
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
	if err := a.Services.Store.DeleteAgentKnownTool(r.Context(), agent.ID, toolName); err != nil {
		if errors.Is(err, store.ErrAgentKnownToolNotFound) {
			a.errorResp(w, http.StatusNotFound, "known tool not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleListAgentKnownSkills(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.Store.ListAgentKnownSkills(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
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
	row, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, skillName)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnownSkillNotFound) {
			a.errorResp(w, http.StatusNotFound, "known skill not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
}

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
	if existing, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, req.SkillName); err == nil && existing != nil {
		a.errorResp(w, http.StatusConflict, "known skill already exists")
		return
	} else if err != nil && !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := store.AgentKnownSkill{
		AgentID:    agent.ID,
		SkillName:  req.SkillName,
		Pinned:     req.Pinned,
		TTLSeconds: req.TTLSeconds,
		Reason:     req.Reason,
	}
	if err := a.Services.Store.InsertAgentKnownSkill(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, req.SkillName)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
}

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
	current, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, skillName)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnownSkillNotFound) {
			a.errorResp(w, http.StatusNotFound, "known skill not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
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
	row := store.AgentKnownSkill{
		AgentID:         agent.ID,
		SkillName:       skillName,
		Pinned:          req.Pinned,
		ActivationCount: current.ActivationCount,
		LastUsedAt:      current.LastUsedAt,
		AddedAt:         current.AddedAt,
		TTLSeconds:      req.TTLSeconds,
		Reason:          req.Reason,
		// TASKS/skills/02: this handler's request shape (AgentKnownSkillUpsertRequest)
		// has no grant-state fields — carry the current row's values forward
		// the same way ActivationCount/LastUsedAt/AddedAt already are, so a
		// plain Panel field edit (pinned/ttl/reason) can never silently wipe
		// a grant a future task 09 workflow set via InsertAgentKnownSkill
		// directly.
		ApprovedContentHash: current.ApprovedContentHash,
		GrantedAt:           current.GrantedAt,
		GrantedBy:           current.GrantedBy,
		CapabilitiesGranted: current.CapabilitiesGranted,
	}
	if err := a.Services.Store.InsertAgentKnownSkill(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, skillName)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, updated)
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
	if err := a.Services.Store.DeleteAgentKnownSkill(r.Context(), agent.ID, skillName); err != nil {
		if errors.Is(err, store.ErrAgentKnownSkillNotFound) {
			a.errorResp(w, http.StatusNotFound, "known skill not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleListAgentProcedures(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.Store.ListAgentProcedures(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

func (a *API) handleGetAgentProcedure(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	row, err := a.Services.Store.GetAgentProcedure(r.Context(), agent.ID, name)
	if err != nil {
		if errors.Is(err, store.ErrAgentProcedureNotFound) {
			a.errorResp(w, http.StatusNotFound, "procedure not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
}

func (a *API) handleCreateAgentProcedure(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req AgentProcedureUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	if existing, err := a.Services.Store.GetAgentProcedure(r.Context(), agent.ID, req.Name); err == nil && existing != nil {
		a.errorResp(w, http.StatusConflict, "procedure already exists")
		return
	} else if err != nil && !errors.Is(err, store.ErrAgentProcedureNotFound) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := store.AgentProcedure{
		AgentID: agent.ID,
		Name:    req.Name,
		Body:    req.Body,
		Scope:   req.Scope,
	}
	if err := a.Services.Store.InsertAgentProcedure(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentProcedure(r.Context(), agent.ID, req.Name)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
}

func (a *API) handleUpdateAgentProcedure(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	if _, err := a.Services.Store.GetAgentProcedure(r.Context(), agent.ID, name); err != nil {
		if errors.Is(err, store.ErrAgentProcedureNotFound) {
			a.errorResp(w, http.StatusNotFound, "procedure not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req AgentProcedureUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.Name != "" && req.Name != name {
		a.errorResp(w, http.StatusBadRequest, "name in body must match path")
		return
	}
	row := store.AgentProcedure{
		AgentID: agent.ID,
		Name:    name,
		Body:    req.Body,
		Scope:   req.Scope,
	}
	if err := a.Services.Store.InsertAgentProcedure(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.Services.Store.GetAgentProcedure(r.Context(), agent.ID, name)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, updated)
}

func (a *API) handleDeleteAgentProcedure(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := a.Services.Store.DeleteAgentProcedure(r.Context(), agent.ID, name); err != nil {
		if errors.Is(err, store.ErrAgentProcedureNotFound) {
			a.errorResp(w, http.StatusNotFound, "procedure not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleListAgentKnowledgeSeeds(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	rows, err := a.Services.Store.ListAgentKnowledgeSeeds(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, rows)
}

func (a *API) handleGetAgentKnowledgeSeed(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	seedKey := r.PathValue("seedKey")
	if seedKey == "" {
		a.errorResp(w, http.StatusBadRequest, "seedKey is required")
		return
	}
	row, err := a.Services.Store.GetAgentKnowledgeSeed(r.Context(), agent.ID, seedKey)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
			a.errorResp(w, http.StatusNotFound, "knowledge seed not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
}

func (a *API) handleCreateAgentKnowledgeSeed(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var req AgentKnowledgeSeedUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.SeedKey == "" {
		a.errorResp(w, http.StatusBadRequest, "seed_key is required")
		return
	}
	if existing, err := a.Services.Store.GetAgentKnowledgeSeed(r.Context(), agent.ID, req.SeedKey); err == nil && existing != nil {
		a.errorResp(w, http.StatusConflict, "knowledge seed already exists")
		return
	} else if err != nil && !errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	tagsJSON, err := normalizeSeedTags(req.TagsJSON, req.Tags)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	row := store.AgentKnowledgeSeed{
		AgentID:   agent.ID,
		SeedKey:   req.SeedKey,
		Namespace: req.Namespace,
		Body:      req.Body,
		TagsJSON:  tagsJSON,
	}
	if err := a.Services.Store.InsertAgentKnowledgeSeed(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.Services.Store.GetAgentKnowledgeSeed(r.Context(), agent.ID, req.SeedKey)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, created)
}

func (a *API) handleUpdateAgentKnowledgeSeed(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	seedKey := r.PathValue("seedKey")
	if seedKey == "" {
		a.errorResp(w, http.StatusBadRequest, "seedKey is required")
		return
	}
	current, err := a.Services.Store.GetAgentKnowledgeSeed(r.Context(), agent.ID, seedKey)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
			a.errorResp(w, http.StatusNotFound, "knowledge seed not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req AgentKnowledgeSeedUpsertRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID != "" && req.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	if req.SeedKey != "" && req.SeedKey != seedKey {
		a.errorResp(w, http.StatusBadRequest, "seed_key in body must match path")
		return
	}
	tagsJSON, err := normalizeSeedTags(req.TagsJSON, req.Tags)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	row := store.AgentKnowledgeSeed{
		AgentID:   agent.ID,
		SeedKey:   seedKey,
		Namespace: req.Namespace,
		Body:      req.Body,
		TagsJSON:  tagsJSON,
		AppliedAt: current.AppliedAt,
		CreatedAt: current.CreatedAt,
	}
	if err := a.Services.Store.InsertAgentKnowledgeSeed(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.Services.Store.GetAgentKnowledgeSeed(r.Context(), agent.ID, seedKey)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, updated)
}

func (a *API) handleDeleteAgentKnowledgeSeed(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	seedKey := r.PathValue("seedKey")
	if seedKey == "" {
		a.errorResp(w, http.StatusBadRequest, "seedKey is required")
		return
	}
	if err := a.Services.Store.DeleteAgentKnowledgeSeed(r.Context(), agent.ID, seedKey); err != nil {
		if errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
			a.errorResp(w, http.StatusNotFound, "knowledge seed not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleMarkAgentKnowledgeSeedApplied(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	seedKey := r.PathValue("seedKey")
	if seedKey == "" {
		a.errorResp(w, http.StatusBadRequest, "seedKey is required")
		return
	}
	if err := a.Services.Store.MarkAgentKnowledgeSeedApplied(r.Context(), agent.ID, seedKey); err != nil {
		if errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
			a.errorResp(w, http.StatusNotFound, "knowledge seed not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	row, err := a.Services.Store.GetAgentKnowledgeSeed(r.Context(), agent.ID, seedKey)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, row)
}

func (a *API) requireAgent(w http.ResponseWriter, r *http.Request) (*store.AgentProfile, bool) {
	id := r.PathValue("id")
	if id == "" {
		a.errorResp(w, http.StatusBadRequest, "agent id is required")
		return nil, false
	}
	agent, err := a.Services.Agents.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return nil, false
	}
	return agent, true
}

// requireMutableAgent resolves the agent and enforces the managed-editability
// gate used by all capability/reflex write endpoints. Managed file-backed
// agents are now fully writable in place (their reflexes, known tools/skills,
// procedures, and knowledge seeds persist against the DB projection keyed by
// the agent's stamped UUID). Embedded internal and plugin/vendor agents are
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

func normalizeSeedTags(tagsJSON string, tags []string) (string, error) {
	if len(tags) > 0 {
		raw, err := json.Marshal(tags)
		if err != nil {
			return "", errors.New("tags must be JSON-stringifiable")
		}
		return string(raw), nil
	}
	if tagsJSON == "" {
		return "[]", nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(tagsJSON), &parsed); err != nil {
		return "", errors.New("tags_json must be a JSON array of strings")
	}
	return tagsJSON, nil
}
