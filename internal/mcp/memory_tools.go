package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/memory"
)

// MemoryToolsTransport provides built-in memory tools that agents can use
// to explicitly save and recall memories. These are separate from the
// automatic extraction hooks — they give agents direct control over memory.
type MemoryToolsTransport struct {
	Memory    *memory.Service
	UserID    string // default user for namespace scoping
	ProjectID string // current project for namespace scoping
}

// NewMemoryToolsTransport creates a MemoryToolsTransport.
func NewMemoryToolsTransport(svc *memory.Service) *MemoryToolsTransport {
	return &MemoryToolsTransport{
		Memory: svc,
		UserID: "default",
	}
}

// ListTools returns the memory tool definitions.
func (mt *MemoryToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	return memoryToolDefinitions(), nil
}

// CallTool dispatches to the appropriate memory tool handler.
func (mt *MemoryToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	switch name {
	case "nanite_memory_save":
		return mt.callMemorySave(ctx, args)
	case "nanite_memory_recall":
		return mt.callMemoryRecall(ctx, args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

// memoryToolDefinitions returns the tool definitions for memory tools.
func memoryToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "nanite_memory_save",
			Description: "Save a memory for future sessions. Use this to explicitly remember important facts, decisions, preferences, or corrections that should persist across conversations.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "One-sentence summary of what to remember (required)",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Fuller description with details (optional)",
					},
					"origin": map[string]any{
						"type":        "string",
						"description": "Memory origin: user, feedback, project, reference, or observation (default: observation)",
						"enum":        []string{"user", "feedback", "project", "reference", "observation"},
					},
					"confidence": map[string]any{
						"type":        "number",
						"description": "Confidence that this memory is accurate and useful (0.0-1.0, default: 0.8)",
					},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "1-5 relevant tags for categorization",
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Memory scope: session (default), project, or user",
						"enum":        []string{"session", "project", "user"},
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Session ID for scoping (uses current session if omitted)",
					},
				},
				"required": []string{"summary"},
			},
		},
		{
			Name:        "nanite_memory_recall",
			Description: "Recall memories from previous sessions. Use this to retrieve saved facts, decisions, preferences, or corrections relevant to the current conversation.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "What kind of memories to recall (used for relevance matching)",
					},
					"scope": map[string]any{
						"type":        "string",
						"description": "Memory scope: session, project, user, or all (default: all)",
						"enum":        []string{"session", "project", "user", "all"},
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of memories to return (default: 10)",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Session ID for session-scoped recall (uses current session if omitted)",
					},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Filter by tags",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

// MemoryToolProviderDefinitions returns memory tool definitions in provider format.
func MemoryToolProviderDefinitions() []Tool {
	return memoryToolDefinitions()
}

// callMemorySave handles the nanite_memory_save tool call.
func (mt *MemoryToolsTransport) callMemorySave(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if mt.Memory == nil {
		return errorResult("memory service not configured"), nil
	}

	summary, _ := args["summary"].(string)
	if summary == "" {
		return errorResult("summary is required"), nil
	}

	body, _ := args["body"].(string)
	origin, _ := args["origin"].(string)
	if origin == "" {
		origin = "observation"
	}

	confidence, _ := args["confidence"].(float64)
	if confidence <= 0 {
		confidence = 0.8
	}

	var tags []string
	if tagsRaw, ok := args["tags"].([]any); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "session"
	}
	sessionID, _ := args["session_id"].(string)

	// Derive memory key from summary.
	memoryKey := summaryToKey(summary)

	// Determine namespace based on scope.
	var namespace string
	switch scope {
	case "session":
		if sessionID != "" {
			namespace = memory.SessionNamespace(sessionID)
		} else {
			namespace = memory.UserNamespace(mt.UserID)
		}
	case "project":
		if mt.ProjectID != "" {
			namespace = memory.ProjectNamespace(mt.ProjectID)
		} else {
			namespace = memory.UserNamespace(mt.UserID)
		}
	case "user":
		namespace = memory.UserNamespace(mt.UserID)
	default:
		namespace = memory.UserNamespace(mt.UserID)
	}

	m := memory.Memory{
		Namespace:  namespace,
		MemoryKey:  memoryKey,
		Summary:    summary,
		Body:       body,
		Origin:     origin,
		Trigger:    "explicit",
		Confidence: confidence,
		Tags:       tags,
		SessionID:  sessionID,
	}

	if err := mt.Memory.Store(ctx, m); err != nil {
		return errorResult(fmt.Sprintf("failed to save memory: %v", err)), nil
	}

	return textResult(fmt.Sprintf("Memory saved: %s (scope=%s, confidence=%.1f)", summary, scope, confidence)), nil
}

// callMemoryRecall handles the nanite_memory_recall tool call.
func (mt *MemoryToolsTransport) callMemoryRecall(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if mt.Memory == nil {
		return errorResult("memory service not configured"), nil
	}

	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "all"
	}
	sessionID, _ := args["session_id"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	var tags []string
	if tagsRaw, ok := args["tags"].([]any); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	// Build namespaces based on scope.
	var namespaces []string
	switch scope {
	case "session":
		if sessionID != "" {
			namespaces = []string{memory.SessionNamespace(sessionID)}
		}
	case "project":
		if mt.ProjectID != "" {
			namespaces = []string{memory.ProjectNamespace(mt.ProjectID)}
		}
	case "user":
		namespaces = []string{memory.UserNamespace(mt.UserID)}
	case "all":
		// Cascade: session + project + user.
		if sessionID != "" {
			namespaces = append(namespaces, memory.SessionNamespace(sessionID))
		}
		if mt.ProjectID != "" {
			namespaces = append(namespaces, memory.ProjectNamespace(mt.ProjectID))
		}
		namespaces = append(namespaces, memory.UserNamespace(mt.UserID))
	}

	if len(namespaces) == 0 {
		namespaces = memory.AllNaniteNamespaces()
	}

	opts := memory.RecallOpts{
		Namespaces: namespaces,
		Ranking:    "activation",
		Limit:      limit,
		Tags:       tags,
	}

	memories, err := mt.Memory.Recall(ctx, opts)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to recall memories: %v", err)), nil
	}

	if len(memories) == 0 {
		return textResult("No memories found matching the query."), nil
	}

	// Format memories for display.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d memories:\n\n", len(memories)))
	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("%d. **%s** (origin=%s, confidence=%.1f)\n", i+1, m.Summary, m.Origin, m.Confidence))
		if m.Body != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", m.Body))
		}
		if len(m.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("   Tags: %s\n", strings.Join(m.Tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("   Namespace: %s | Key: %s\n\n", m.Namespace, m.MemoryKey))
	}

	return textResult(sb.String()), nil
}

// summaryToKey converts a summary string to a snake_case memory key.
func summaryToKey(summary string) string {
	// Take first 60 chars, lowercase, replace non-alphanumeric with underscore.
	s := strings.ToLower(summary)
	if len(s) > 60 {
		s = s[:60]
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore {
			b.WriteRune('_')
			prevUnderscore = true
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		result = "memory"
	}
	return result
}

// textResult and errorResult are defined in dev_tools.go.
