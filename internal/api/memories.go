package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/nanite/internal/memory"
)

// memoryKeyEncode encodes namespace+memoryKey into a single path-safe token.
// Format: base64url(namespace + "\x00" + memoryKey)
func memoryKeyEncode(namespace, memoryKey string) string {
	raw := namespace + "\x00" + memoryKey
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// memoryKeyDecode decodes a path key back to namespace and memoryKey.
func memoryKeyDecode(key string) (namespace, memoryKey string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(b), "\x00", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// handleListMemories handles GET /api/memories
// Query params: scope, status, q (search/tag filter), tags (comma-separated), limit (default 50), offset (default 0)
func (a *API) handleListMemories(w http.ResponseWriter, r *http.Request) {
	if a.Services.Memory == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "memory service not available")
		return
	}

	q := r.URL.Query()

	// Build namespaces from scope param.
	var namespaces []string
	scope := q.Get("scope")
	switch scope {
	case "session":
		sid := q.Get("session_id")
		if sid != "" {
			namespaces = append(namespaces, memory.SessionMemoryPrefix(sid))
		} else {
			namespaces = memory.AllNaniteNamespaces()
		}
	case "project":
		pid := q.Get("project_id")
		if pid != "" {
			namespaces = append(namespaces, memory.ProjectMemoryPrefix(pid))
		} else {
			namespaces = memory.AllNaniteNamespaces()
		}
	case "user":
		namespaces = []string{memory.UserMemoryPrefix("default")}
	default:
		namespaces = memory.AllNaniteNamespaces()
	}

	// Parse tags — comma-separated or repeated param.
	var tags []string
	if raw := q.Get("tags"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}
	tags = append(tags, q["tags"]...)

	// Parse limit/offset.
	limit := 50
	if lStr := q.Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	// Recall applies offset only after status/text filters.
	offset := 0
	if oStr := q.Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	opts := memory.RecallOpts{
		Namespaces:  namespaces,
		Ranking:     memory.RankingActivation,
		Limit:       limit,
		Offset:      offset,
		Tags:        tags,
		Search:      q.Get("q"),
		PayloadMode: memory.PayloadModeFull,
	}
	if status := q.Get("status"); status != "" {
		opts.Statuses = []string{status}
	}

	memories, total, err := a.Services.Memory.List(r.Context(), opts)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "recall failed: "+err.Error())
		return
	}

	// Add computed key to each memory for client use.
	type memoryWithKey struct {
		memory.Memory
		Key string `json:"key"`
	}
	out := make([]memoryWithKey, len(memories))
	for i, m := range memories {
		out[i] = memoryWithKey{Memory: m, Key: memoryKeyEncode(m.Namespace, m.MemoryKey)}
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"memories": out,
		"total":    total,
	})
}

// createMemoryRequest is the request body for POST /api/memories.
type createMemoryRequest struct {
	Summary    string   `json:"summary"`
	Body       string   `json:"body"`
	Origin     string   `json:"origin"`
	Confidence float64  `json:"confidence"`
	Scope      string   `json:"scope"`
	Namespace  string   `json:"namespace"`  // optional override
	MemoryKey  string   `json:"memory_key"` // optional override
	Tags       []string `json:"tags"`
	SessionID  string   `json:"session_id"`
}

// handleCreateMemory handles POST /api/memories
func (a *API) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	if a.Services.Memory == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "memory service not available")
		return
	}

	var req createMemoryRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Summary == "" {
		a.errorResp(w, http.StatusBadRequest, "summary is required")
		return
	}

	// Resolve namespace from scope or explicit override.
	ns := req.Namespace
	if ns == "" {
		switch req.Scope {
		case "session":
			if req.SessionID != "" {
				ns = memory.SessionNamespace(req.SessionID)
			} else {
				ns = memory.UserNamespace("default")
			}
		case "project":
			ns = memory.UserNamespace("default")
		case "user", "":
			ns = memory.UserNamespace("default")
		default:
			ns = memory.UserNamespace("default")
		}
	}

	// Generate a memory key if not provided.
	memKey := req.MemoryKey
	if memKey == "" {
		// Derive a stable v0.9-valid key from the summary.
		memKey = derivedKey(req.Summary)
	}

	origin := req.Origin
	if origin == "" {
		origin = "user"
	}

	confidence := req.Confidence
	if confidence <= 0 {
		confidence = 0.8
	}

	m := memory.Memory{
		Namespace:  ns,
		MemoryKey:  memKey,
		Summary:    req.Summary,
		Body:       req.Body,
		Origin:     origin,
		Trigger:    "manual",
		Confidence: confidence,
		Tags:       req.Tags,
		SessionID:  req.SessionID,
		Status:     "draft",
	}

	if err := a.Services.Memory.Store(r.Context(), m); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "store failed: "+err.Error())
		return
	}

	// Fetch the stored memory to return the revision ID.
	stored, err := a.Services.Memory.Get(r.Context(), ns, memKey)
	if err != nil {
		// Return partial response if Get fails.
		a.jsonResp(w, http.StatusCreated, map[string]any{
			"memory": m,
			"key":    memoryKeyEncode(ns, memKey),
		})
		return
	}

	a.jsonResp(w, http.StatusCreated, map[string]any{
		"memory": stored,
		"key":    memoryKeyEncode(ns, memKey),
	})
}

// updateMemoryRequest is the request body for PUT /api/memories/{key}.
type updateMemoryRequest struct {
	Summary    string   `json:"summary"`
	Body       *string  `json:"body"`
	Origin     string   `json:"origin"`
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags"`
}

// handleUpdateMemory handles PUT /api/memories/{key}
func (a *API) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	if a.Services.Memory == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "memory service not available")
		return
	}

	ns, memKey, ok := memoryKeyDecode(r.PathValue("key"))
	if !ok {
		a.errorResp(w, http.StatusBadRequest, "invalid memory key")
		return
	}

	// Fetch the current memory to get its scope/session context.
	current, err := a.Services.Memory.Get(r.Context(), ns, memKey)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "memory not found: "+err.Error())
		return
	}

	var req updateMemoryRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Apply updates — scope (namespace) is immutable.
	updated := memory.Memory{
		Namespace:  ns,
		MemoryKey:  memKey,
		Summary:    req.Summary,
		Body:       current.Body,
		Origin:     req.Origin,
		Trigger:    "manual",
		Confidence: req.Confidence,
		Tags:       req.Tags,
		SessionID:  current.SessionID,
		Status:     current.Status,
	}
	if req.Body != nil {
		updated.Body = *req.Body
	}

	// Fall back to current values for empty fields.
	if updated.Summary == "" {
		updated.Summary = current.Summary
	}
	if updated.Origin == "" {
		updated.Origin = current.Origin
	}
	if updated.Confidence <= 0 {
		updated.Confidence = current.Confidence
	}
	if updated.Tags == nil {
		updated.Tags = current.Tags
	}

	if err := a.Services.Memory.Store(r.Context(), updated); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "update failed: "+err.Error())
		return
	}

	stored, err := a.Services.Memory.Get(r.Context(), ns, memKey)
	if err != nil {
		a.jsonResp(w, http.StatusOK, map[string]any{
			"memory": updated,
			"key":    r.PathValue("key"),
		})
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"memory": stored,
		"key":    r.PathValue("key"),
	})
}

// handleDeleteMemory handles DELETE /api/memories/{key}
func (a *API) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if a.Services.Memory == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "memory service not available")
		return
	}

	ns, memKey, ok := memoryKeyDecode(r.PathValue("key"))
	if !ok {
		a.errorResp(w, http.StatusBadRequest, "invalid memory key")
		return
	}

	// Get the current revision to retrieve its revision ID for deprecation.
	current, err := a.Services.Memory.Get(r.Context(), ns, memKey)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "memory not found: "+err.Error())
		return
	}

	if current.RevisionID == "" {
		a.errorResp(w, http.StatusUnprocessableEntity, "memory has no revision ID; cannot delete")
		return
	}

	if err := a.Services.Memory.Deprecate(r.Context(), current.RevisionID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "delete failed: "+err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]bool{"deleted": true})
}

// statusTransitionRequest is the request body for PUT /api/memories/{key}/status.
type statusTransitionRequest struct {
	Status string `json:"status"`
}

var validMemoryStatuses = map[string]bool{
	"draft":      true,
	"reviewed":   true,
	"canonical":  true,
	"deprecated": true,
}

// handleUpdateMemoryStatus handles PUT /api/memories/{key}/status
func (a *API) handleUpdateMemoryStatus(w http.ResponseWriter, r *http.Request) {
	if a.Services.Memory == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "memory service not available")
		return
	}

	ns, memKey, ok := memoryKeyDecode(r.PathValue("key"))
	if !ok {
		a.errorResp(w, http.StatusBadRequest, "invalid memory key")
		return
	}

	var req statusTransitionRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if !validMemoryStatuses[req.Status] {
		a.errorResp(w, http.StatusBadRequest, "invalid status: must be one of draft, reviewed, canonical, deprecated")
		return
	}

	// Get current memory to carry over all fields.
	current, err := a.Services.Memory.Get(r.Context(), ns, memKey)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "memory not found: "+err.Error())
		return
	}

	// If transitioning to deprecated, use Deprecate() directly.
	if req.Status == "deprecated" {
		if current.RevisionID == "" {
			a.errorResp(w, http.StatusUnprocessableEntity, "memory has no revision ID; cannot deprecate")
			return
		}
		if err := a.Services.Memory.Deprecate(r.Context(), current.RevisionID); err != nil {
			a.errorResp(w, http.StatusInternalServerError, "deprecate failed: "+err.Error())
			return
		}
		current.Status = "deprecated"
		a.jsonResp(w, http.StatusOK, map[string]any{
			"memory": current,
			"key":    r.PathValue("key"),
		})
		return
	}

	// For other status transitions, write a new revision with the updated status.
	updated := memory.Memory{
		Namespace:  ns,
		MemoryKey:  memKey,
		Summary:    current.Summary,
		Body:       current.Body,
		Origin:     current.Origin,
		Trigger:    "manual",
		Confidence: current.Confidence,
		Tags:       current.Tags,
		SessionID:  current.SessionID,
		Status:     req.Status,
	}

	if err := a.Services.Memory.Store(r.Context(), updated); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "status update failed: "+err.Error())
		return
	}

	stored, err := a.Services.Memory.Get(r.Context(), ns, memKey)
	if err != nil {
		updated.Status = req.Status
		a.jsonResp(w, http.StatusOK, map[string]any{
			"memory": updated,
			"key":    r.PathValue("key"),
		})
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"memory": stored,
		"key":    r.PathValue("key"),
	})
}

// derivedKey generates a stable memory key from a summary string.
// Lowercases, replaces separators with underscores, strips invalid chars, and
// truncates to Tesseract v0.9's [a-z0-9_] key contract.
func derivedKey(summary string) string {
	s := strings.ToLower(summary)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	key := strings.Trim(b.String(), "_")
	if key == "" {
		key = "memory"
	}
	return key
}
