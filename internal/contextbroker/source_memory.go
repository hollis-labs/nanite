package contextbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/memory"
)

// memoryDefaultLimit is the result cap MemorySource uses when the caller
// does not pin Intent.AutoRecallLimit. Matches the prior hardcoded value.
const memoryDefaultLimit = 30

// memoryDefaultMinConfidence is the confidence floor MemorySource uses
// when the caller does not pin Intent.AutoRecallMinConfidence. Matches
// the prior hardcoded value.
const memoryDefaultMinConfidence = 0.4

// memoryDefaultTimeout caps the Tesseract round-trip when Intent.AutoRecallTimeout
// is zero. Memory is enrichment, not identity — the chat loop should not
// stall on a slow recall. Matches the implementer-prompt's 2s budget.
const memoryDefaultTimeout = 2 * time.Second

// MemorySource retrieves memories from Tesseract via the MemoryService.
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
//
// Per-turn auto-recall behavior is gated by Intent.AutoRecall (chat-harness
// reads AgentProfile.Settings.auto_recall and plumbs it here). When set
// explicitly to false, Fetch short-circuits with a debug log so the agent
// sees an empty Memory slot for the turn. Limit, MinConfidence, and Timeout
// fall back to source defaults when the intent leaves them zero.
func (s *MemorySource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.Memory == nil {
		return nil, fmt.Errorf("memory source: no memory service configured")
	}

	// Per-agent disable: chat-harness sets AutoRecall=false when the
	// profile pins it off. nil = use source default (enabled).
	if intent.AutoRecall != nil && !*intent.AutoRecall {
		slog.Debug("contextbroker/memory: auto-recall disabled by intent",
			"session_id", intent.SessionID, "agent_id", intent.AgentID)
		return nil, nil
	}

	// Resolve overrides with source defaults.
	limit := memoryDefaultLimit
	if intent.AutoRecallLimit > 0 {
		limit = intent.AutoRecallLimit
	}
	// AutoRecallMinConfidence override range is [0, 1]. A literal 0 is a
	// valid override (return everything regardless of confidence); the
	// "use source default" sentinel is a negative value or anything > 1.
	minConfidence := memoryDefaultMinConfidence
	if intent.AutoRecallMinConfidence >= 0 && intent.AutoRecallMinConfidence <= 1 {
		minConfidence = intent.AutoRecallMinConfidence
	}
	timeout := memoryDefaultTimeout
	if intent.AutoRecallTimeout > 0 {
		timeout = intent.AutoRecallTimeout
	}

	// Build namespace cascade based on available scope info.
	var namespaces []string

	// Session-scoped memories (most specific).
	if intent.SessionID != "" {
		namespaces = append(namespaces, memory.SessionMemoryPrefix(intent.SessionID))
	}

	// Project-scoped memories.
	projectID := intent.Scope
	if projectID == "" {
		projectID = s.ProjectID
	}
	if projectID != "" {
		namespaces = append(namespaces, memory.ProjectMemoryPrefix(projectID))
	}

	// User-scoped memories (broadest).
	if s.UserID != "" {
		namespaces = append(namespaces, memory.UserMemoryPrefix(s.UserID))
	}

	// If no specific namespaces, use glob for all Nanite memories.
	if len(namespaces) == 0 {
		namespaces = memory.AllNaniteNamespaces()
	}

	opts := memory.RecallOpts{
		Namespaces:    namespaces,
		Ranking:       memory.RankingRelevance,
		SearchMode:    memory.SearchModeHybrid,
		Query:         intent.QueryText,
		Limit:         limit,
		MinConfidence: minConfidence,
	}

	recallCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	memories, err := s.Memory.Recall(recallCtx, opts)
	elapsed := time.Since(start)

	if err != nil {
		// Distinguish timeouts from other errors so operators can spot a
		// slow Tesseract from a misconfigured source.
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Info("contextbroker/memory: auto-recall timed out",
				"session_id", intent.SessionID, "agent_id", intent.AgentID,
				"timeout_ms", timeout.Milliseconds(), "elapsed_ms", elapsed.Milliseconds())
			return nil, nil
		}
		return nil, fmt.Errorf("memory source recall: %w", err)
	}
	if len(memories) == 0 {
		slog.Info("contextbroker/memory: auto-recall hit_count=0",
			"session_id", intent.SessionID, "agent_id", intent.AgentID,
			"limit", limit, "min_confidence", minConfidence,
			"latency_ms", elapsed.Milliseconds(), "query_len", len(intent.QueryText))
		return nil, nil
	}

	// Convert recalled memories to context items, respecting token budget.
	var items []ContextItem
	var usedRevisionIDs []string
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
		usedRevisionIDs = append(usedRevisionIDs, m.RevisionID)
		usedTokens += tokens
	}

	// A recall candidate is not an access signal. Reinforce only the rows the
	// broker actually selected into the assembled context.
	if err := s.Memory.Touch(recallCtx, usedRevisionIDs); err != nil {
		slog.Warn("contextbroker/memory: touch selected memories failed", "err", err)
	}

	// Single structured INFO line per turn — mirrors S3b's tool_cache classify
	// pattern so operators can grep one log shape for memory-recall outcomes.
	slog.Info("contextbroker/memory: auto-recall ok",
		"session_id", intent.SessionID, "agent_id", intent.AgentID,
		"hit_count", len(memories), "items_kept", len(items),
		"tokens_estimate", usedTokens, "limit", limit,
		"min_confidence", minConfidence,
		"latency_ms", elapsed.Milliseconds(), "query_len", len(intent.QueryText))

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
