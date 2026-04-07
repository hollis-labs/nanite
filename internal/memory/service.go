// Package memory provides persistent memory storage, recall, and extraction
// backed by Vanta Conduit's memory MCP tools. Memories survive session boundaries
// and are surfaced during context assembly via the MemorySource.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// MCPCaller is the interface for calling MCP tools. This matches the
// subset of mcp.Manager that MemoryService needs, avoiding a direct import.
type MCPCaller interface {
	ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error)
}

// Memory represents a memory item to store or recalled from Vanta Conduit.
type Memory struct {
	Namespace  string   `json:"namespace"`
	MemoryKey  string   `json:"memory_key"`
	Summary    string   `json:"summary"`
	Body       string   `json:"body"`
	Origin     string   `json:"origin"`     // user, feedback, project, reference, observation
	Trigger    string   `json:"trigger"`    // explicit, post_compact, per_turn, promotion, manual
	Confidence float64  `json:"confidence"` // 0.0-1.0
	Tags       []string `json:"tags"`
	SessionID  string   `json:"session_id"`
	RevisionID string   `json:"revision_id,omitempty"` // set on recall
	Status     string   `json:"status,omitempty"`      // draft, reviewed, canonical, deprecated
}

// RecallOpts configures memory recall.
type RecallOpts struct {
	Namespaces    []string // supports globs like "app/nanite/user/*"
	Ranking       string   // "activation" (default), "chronological", "similarity"
	Limit         int      // max results (default 20)
	MinConfidence float64  // minimum confidence threshold
	Origins       []string // filter by origin
	Tags          []string // filter by tags
}

// Service provides memory storage and recall via Vanta Conduit MCP tools.
type Service struct {
	mcp        MCPCaller
	serverName string // MCP server name for Vanta Conduit (default: "conduit")
}

// NewService creates a MemoryService backed by the given MCP caller.
func NewService(mcp MCPCaller) *Service {
	return &Service{
		mcp:        mcp,
		serverName: "conduit",
	}
}

// Store writes a memory revision to Vanta Conduit via the memory_write MCP tool.
func (s *Service) Store(ctx context.Context, m Memory) error {
	if s.mcp == nil {
		return fmt.Errorf("memory service: no MCP caller configured")
	}

	toolName := fmt.Sprintf("mcp__%s__memory_write", s.serverName)

	input := map[string]any{
		"namespace":  m.Namespace,
		"memory_key": m.MemoryKey,
		"summary":    m.Summary,
		"status":     "draft",
	}
	if m.Body != "" {
		input["body"] = m.Body
	}
	if m.Origin != "" {
		input["origin"] = m.Origin
	}
	if m.Trigger != "" {
		input["trigger"] = m.Trigger
	}
	if m.Confidence > 0 {
		input["confidence"] = m.Confidence
	}
	if len(m.Tags) > 0 {
		input["tags"] = m.Tags
	}
	if m.SessionID != "" {
		input["session_id"] = m.SessionID
	}

	_, err := s.mcp.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return fmt.Errorf("memory_write: %w", err)
	}

	log.Printf("memory: stored %s/%s (origin=%s, trigger=%s, confidence=%.1f)",
		m.Namespace, m.MemoryKey, m.Origin, m.Trigger, m.Confidence)
	return nil
}

// Recall fetches memories from Vanta Conduit via the memory_recall MCP tool.
func (s *Service) Recall(ctx context.Context, opts RecallOpts) ([]Memory, error) {
	if s.mcp == nil {
		return nil, fmt.Errorf("memory service: no MCP caller configured")
	}

	toolName := fmt.Sprintf("mcp__%s__memory_recall", s.serverName)

	input := map[string]any{}

	if len(opts.Namespaces) > 0 {
		input["namespaces"] = opts.Namespaces
	}
	ranking := opts.Ranking
	if ranking == "" {
		ranking = "activation"
	}
	input["ranking"] = ranking

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	input["limit"] = limit

	// Build filters.
	filters := map[string]any{}
	if opts.MinConfidence > 0 {
		filters["min_confidence"] = opts.MinConfidence
	}
	if len(opts.Origins) > 0 {
		filters["origins"] = opts.Origins
	}
	if len(opts.Tags) > 0 {
		filters["tags"] = opts.Tags
	}
	// Only include statuses that are active.
	filters["statuses"] = []string{"draft", "reviewed", "canonical"}

	if len(filters) > 0 {
		input["filters"] = filters
	}

	result, err := s.mcp.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("memory_recall: %w", err)
	}

	return parseRecallResult(result)
}

// Get fetches a single memory by namespace and key via the memory_get MCP tool.
func (s *Service) Get(ctx context.Context, namespace, memoryKey string) (*Memory, error) {
	if s.mcp == nil {
		return nil, fmt.Errorf("memory service: no MCP caller configured")
	}

	toolName := fmt.Sprintf("mcp__%s__memory_get", s.serverName)

	input := map[string]any{
		"namespace":  namespace,
		"memory_key": memoryKey,
	}

	result, err := s.mcp.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("memory_get: %w", err)
	}

	var m Memory
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		return nil, fmt.Errorf("memory_get: parse response: %w", err)
	}
	return &m, nil
}

// Promote moves a memory to a broader scope via the memory_promote MCP tool.
func (s *Service) Promote(ctx context.Context, revisionID, targetNamespace string) error {
	if s.mcp == nil {
		return fmt.Errorf("memory service: no MCP caller configured")
	}

	toolName := fmt.Sprintf("mcp__%s__memory_promote", s.serverName)

	input := map[string]any{
		"revision_id":      revisionID,
		"target_namespace": targetNamespace,
	}

	_, err := s.mcp.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return fmt.Errorf("memory_promote: %w", err)
	}

	log.Printf("memory: promoted revision %s to %s", revisionID, targetNamespace)
	return nil
}

// Deprecate marks a memory revision as deprecated via the memory_deprecate MCP tool.
func (s *Service) Deprecate(ctx context.Context, revisionID string) error {
	if s.mcp == nil {
		return fmt.Errorf("memory service: no MCP caller configured")
	}

	toolName := fmt.Sprintf("mcp__%s__memory_deprecate", s.serverName)

	input := map[string]any{
		"revision_id": revisionID,
	}

	_, err := s.mcp.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return fmt.Errorf("memory_deprecate: %w", err)
	}

	log.Printf("memory: deprecated revision %s", revisionID)
	return nil
}

// SessionNamespace returns the memory namespace for a session.
func SessionNamespace(sessionID string) string {
	return "app/nanite/session/" + sessionID
}

// ProjectNamespace returns the memory namespace for a project.
func ProjectNamespace(projectID string) string {
	return "app/nanite/project/" + projectID
}

// UserNamespace returns the memory namespace for a user.
func UserNamespace(userID string) string {
	return "app/nanite/user/" + userID
}

// AllNaniteNamespaces returns glob patterns that cover all Nanite memory namespaces.
func AllNaniteNamespaces() []string {
	return []string{"app/nanite/*"}
}

// parseRecallResult parses the JSON response from memory_recall into Memory structs.
func parseRecallResult(raw string) ([]Memory, error) {
	// Try array format first.
	var memories []Memory
	if err := json.Unmarshal([]byte(raw), &memories); err == nil {
		return memories, nil
	}

	// Try wrapped format with a "memories" or "results" key.
	var wrapped struct {
		Memories []Memory `json:"memories"`
		Results  []Memory `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
		// If not JSON, return empty.
		if strings.TrimSpace(raw) == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("parse recall result: %w", err)
	}

	if len(wrapped.Memories) > 0 {
		return wrapped.Memories, nil
	}
	return wrapped.Results, nil
}
