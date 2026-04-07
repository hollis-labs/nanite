package contextbroker

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/nanite/internal/memory"
)

// MemorySource retrieves memories from Vanta Conduit via the MemoryService.
// It cascades through session, project, and user namespaces to assemble
// relevant memories for the current context.
type MemorySource struct {
	Memory    *memory.Service
	UserID    string // default user for user-scoped namespace
	ProjectID string // current project for project-scoped namespace
}

// NewMemorySource creates a MemorySource backed by the given MemoryService.
func NewMemorySource(svc *memory.Service) *MemorySource {
	return &MemorySource{
		Memory: svc,
		UserID: "default",
	}
}

func (s *MemorySource) Name() string { return "memory" }

// Fetch recalls memories relevant to the current intent, cascading through
// namespace scopes: session -> project -> user.
func (s *MemorySource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.Memory == nil {
		return nil, fmt.Errorf("memory source: no memory service configured")
	}

	// Build namespace cascade based on available scope info.
	var namespaces []string

	// Session-scoped memories (most specific).
	if intent.SessionID != "" {
		namespaces = append(namespaces, memory.SessionNamespace(intent.SessionID))
	}

	// Project-scoped memories.
	projectID := intent.Scope
	if projectID == "" {
		projectID = s.ProjectID
	}
	if projectID != "" {
		namespaces = append(namespaces, memory.ProjectNamespace(projectID))
	}

	// User-scoped memories (broadest).
	if s.UserID != "" {
		namespaces = append(namespaces, memory.UserNamespace(s.UserID))
	}

	// If no specific namespaces, use glob for all Nanite memories.
	if len(namespaces) == 0 {
		namespaces = memory.AllNaniteNamespaces()
	}

	opts := memory.RecallOpts{
		Namespaces:    namespaces,
		Ranking:       "activation",
		Limit:         30,
		MinConfidence: 0.4,
	}

	memories, err := s.Memory.Recall(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("memory source recall: %w", err)
	}

	if len(memories) == 0 {
		return nil, nil
	}

	// Convert recalled memories to context items, respecting token budget.
	var items []ContextItem
	usedTokens := 0

	for _, m := range memories {
		// Format memory content.
		content := formatMemory(m)
		tokens := EstimateTokens(content)

		if usedTokens+tokens > budget {
			break
		}

		// Relevance based on confidence + scope proximity.
		relevance := m.Confidence
		if relevance == 0 {
			relevance = 0.6
		}
		// Boost user and feedback memories slightly.
		if m.Origin == "feedback" || m.Origin == "user" {
			relevance = min(relevance+0.1, 1.0)
		}

		items = append(items, ContextItem{
			Source:        "memory",
			Key:           fmt.Sprintf("%s/%s", m.Namespace, m.MemoryKey),
			Content:       content,
			TokenEstimate: tokens,
			Relevance:     relevance,
			Metadata: map[string]string{
				"namespace":  m.Namespace,
				"memory_key": m.MemoryKey,
				"origin":     m.Origin,
			},
		})
		usedTokens += tokens
	}

	log.Printf("contextbroker/memory: recalled %d memories (%d tokens)", len(items), usedTokens)
	return items, nil
}

// formatMemory renders a memory item as a readable context block.
func formatMemory(m memory.Memory) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%s** (%s)", m.Summary, m.Origin))
	if m.Body != "" {
		sb.WriteString("\n")
		sb.WriteString(m.Body)
	}
	if len(m.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("\nTags: %s", strings.Join(m.Tags, ", ")))
	}
	return sb.String()
}

// min is a builtin in Go 1.21+, used for float64 clamping above.
