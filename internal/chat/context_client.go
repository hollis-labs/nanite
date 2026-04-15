package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultBudgetPct is the default fraction of the context window to use.
const DefaultBudgetPct = 0.75

// DefaultContextWindow is the fallback context window size in tokens.
const DefaultContextWindow = 200000

// HardCeilingPct is the absolute maximum fraction of context window allowed
// before refusing to send. This is the unified budget that includes system
// prompt + messages + tool definitions.
const HardCeilingPct = 0.80

// ToolResultPruneAge is the number of tool-use iterations after which old
// tool results are replaced with compact references.
const ToolResultPruneAge = 2

// ContextClient assembles and manages context for chat turns.
type ContextClient struct {
	Store         *store.Store
	BudgetPct     float64                // fraction of context window to use (default 0.75)
	ContextBroker *contextbroker.Broker  // universal context retrieval (nil = disabled)
}

// NewContextClient creates a new ContextClient with default settings.
func NewContextClient(s *store.Store) *ContextClient {
	return &ContextClient{
		Store:     s,
		BudgetPct: DefaultBudgetPct,
	}
}

// AssembleContext builds the full context for a turn:
// 1. System prompt (from prompt templates or legacy agent + mode + workspace)
// 2. Recent messages (from session history)
// 3. Enforce budget ceiling
func (cb *ContextClient) AssembleContext(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (string, []provider.ChatMessage, error) {
	_, span := feotel.StartSpan(ctx, "nanite.broker.assembleContext")
	defer span.End()

	span.SetAttributes(
		attribute.String("nanite.session.id", session.ID),
		attribute.String("nanite.agent.id", agent.ID),
	)

	// 1. Build the system prompt using prompt templates.
	skillList := buildSkillList(cb.Store, agent.ID)
	systemPrompt := assembleSystemPromptFromTemplates(cb.Store, agent, mode, workspace, skillList)

	// 1b. Enrich system prompt with universal context retrieval.
	if cb.ContextBroker != nil {
		systemPrompt = cb.enrichWithContextBroker(ctx, systemPrompt, session, agent)
	}

	// 2. Load messages from DB. Start with a generous limit.
	messages, err := cb.Store.ListMessages(session.ID, 200)
	if err != nil {
		return "", nil, err
	}

	// 3. Convert to provider messages.
	chatMessages := make([]provider.ChatMessage, len(messages))
	for i, m := range messages {
		role := m.Role
		if role == "system" || role == "tool" || role == RoleEnvelopeResponse {
			role = "user" // Anthropic API only accepts user/assistant
		}
		chatMessages[i] = provider.ChatMessage{Role: role, Content: m.Content}
	}

	// 4. Estimate total tokens and enforce budget.
	budgetPct := cb.BudgetPct
	if budgetPct <= 0 {
		budgetPct = DefaultBudgetPct
	}
	budget := int(float64(DefaultContextWindow) * budgetPct)

	systemTokens := EstimateTokens(systemPrompt)
	totalTokens := systemTokens
	for _, cm := range chatMessages {
		totalTokens += EstimateTokens(cm.Content)
	}

	// If over budget, drop oldest messages until under budget.
	for totalTokens > budget && len(chatMessages) > 1 {
		totalTokens -= EstimateTokens(chatMessages[0].Content)
		chatMessages = chatMessages[1:]
	}

	span.SetAttributes(
		attribute.Int("nanite.broker.system_tokens", systemTokens),
		attribute.Int("nanite.broker.message_count", len(chatMessages)),
		attribute.Int("nanite.broker.total_tokens", totalTokens),
		attribute.Int("nanite.broker.budget", budget),
	)

	slog.Info("broker: assembled context",
		"system_tokens", systemTokens, "messages", len(chatMessages),
		"total_tokens", totalTokens, "budget", budget)

	return systemPrompt, chatMessages, nil
}

// EstimateTokens does a rough chars/4 estimation.
func EstimateTokens(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}

// PruneAfterTurn compacts old tool results in the message history.
// Tool-role messages older than 2 turns from the end with content longer
// than 500 chars are replaced with a structured compaction marker.
func (cb *ContextClient) PruneAfterTurn(sessionID string) error {
	messages, err := cb.Store.ListMessages(sessionID, 1000)
	if err != nil {
		return err
	}

	if len(messages) <= 3 {
		return nil
	}

	// Keep last 2 turns (4 messages: 2 user + 2 assistant).
	cutoff := len(messages) - 4
	if cutoff < 0 {
		cutoff = 0
	}

	compacted := 0
	for i := 0; i < cutoff; i++ {
		m := messages[i]
		if m.Role == "tool" && !m.IsCompacted && len(m.Content) > 500 {
			marker := fmt.Sprintf("[compacted: tool output, %d chars]", len(m.Content))
			if err := cb.Store.UpdateMessageContent(m.ID, marker, true); err != nil {
				slog.Warn("broker: failed to compact message", "id", m.ID, "err", err)
				continue
			}
			compacted++
		}
	}

	if compacted > 0 {
		slog.Info("broker: compacted tool messages", "count", compacted, "session_id", sessionID)
	}
	return nil
}

// TokenBreakdown holds the token accounting for a prompt before sending.
type TokenBreakdown struct {
	System   int `json:"system"`
	Messages int `json:"messages"`
	Tools    int `json:"tools"`
	Total    int `json:"total"`
	Ceiling  int `json:"ceiling"`
}

// EstimateToolDefTokens estimates total tokens for provider tool definitions.
func EstimateToolDefTokens(tools []provider.ToolDefinition) int {
	total := 0
	for _, t := range tools {
		data, err := json.Marshal(t)
		if err != nil {
			n := len(t.Name) + len(t.Description)
			if n == 0 {
				n = 4
			}
			total += n / 4
			continue
		}
		n := len(data) / 4
		if n == 0 {
			n = 1
		}
		total += n
	}
	return total
}

// EstimateMessagesTokens estimates total tokens for a message slice, including
// both simple content and content blocks (tool_use/tool_result).
func EstimateMessagesTokens(messages []provider.ChatMessage) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.Content)
		for _, b := range m.ContentBlocks {
			total += EstimateTokens(b.Text) + EstimateTokens(b.Content)
			if b.Input != nil {
				data, _ := json.Marshal(b.Input)
				total += len(data) / 4
			}
		}
	}
	return total
}

// EnforceTokenBudget is the unified enforcement gate that runs before every
// provider call. It checks the total estimated tokens (system + messages + tools)
// against the hard ceiling and applies a reduction cascade if over budget:
//  1. Prune old tool results from messages (replace with compact references)
//  2. Reduce tool count (drop from end)
//  3. Drop oldest messages
//  4. If still over: return error (refuse to send)
//
// The ceilingOverride parameter allows tighter budgets on retries (pass 0 for default).
// Returns the (possibly modified) messages, tools, and a token breakdown.
func EnforceTokenBudget(
	systemPrompt string,
	messages []provider.ChatMessage,
	tools []provider.ToolDefinition,
	ceilingOverride int,
) ([]provider.ChatMessage, []provider.ToolDefinition, *TokenBreakdown, error) {
	ceiling := int(float64(DefaultContextWindow) * HardCeilingPct)
	if ceilingOverride > 0 {
		ceiling = ceilingOverride
	}

	systemTokens := EstimateTokens(systemPrompt)
	msgTokens := EstimateMessagesTokens(messages)
	toolTokens := EstimateToolDefTokens(tools)
	total := systemTokens + msgTokens + toolTokens

	breakdown := &TokenBreakdown{
		System:   systemTokens,
		Messages: msgTokens,
		Tools:    toolTokens,
		Total:    total,
		Ceiling:  ceiling,
	}

	slog.Debug("broker: token gate",
		"system", systemTokens, "messages", msgTokens, "tools", toolTokens,
		"total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 1: Prune old tool results from messages (keep last 2 tool-use pairs).
	messages = pruneToolResultsInMemory(messages)
	msgTokens = EstimateMessagesTokens(messages)
	total = systemTokens + msgTokens + toolTokens
	breakdown.Messages = msgTokens
	breakdown.Total = total
	slog.Debug("broker: after tool-result pruning", "messages", msgTokens, "total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 2: Reduce tool count — drop from end until under budget or 1 tool left.
	for len(tools) > 1 && total > ceiling {
		tools = tools[:len(tools)-1]
		toolTokens = EstimateToolDefTokens(tools)
		total = systemTokens + msgTokens + toolTokens
	}
	breakdown.Tools = toolTokens
	breakdown.Total = total
	slog.Debug("broker: after tool reduction", "tools", len(tools), "total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 3: Drop oldest messages until under budget or only 1 left.
	for len(messages) > 1 && total > ceiling {
		total -= EstimateTokens(messages[0].Content)
		for _, b := range messages[0].ContentBlocks {
			total -= EstimateTokens(b.Text) + EstimateTokens(b.Content)
		}
		messages = messages[1:]
	}
	msgTokens = EstimateMessagesTokens(messages)
	total = systemTokens + msgTokens + toolTokens
	breakdown.Messages = msgTokens
	breakdown.Total = total
	slog.Debug("broker: after message drop", "messages", len(messages), "total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 4: Still over — refuse to send.
	return messages, tools, breakdown, fmt.Errorf(
		"context exceeds hard ceiling after all reductions: %d tokens > %d ceiling", total, ceiling)
}

// enrichWithContextBroker calls the universal ContextBroker to fetch
// multi-source context and appends it to the system prompt.
func (cb *ContextClient) enrichWithContextBroker(ctx context.Context, systemPrompt string, session *store.Session, agent *store.AgentProfile) string {
	// Derive intent from the session's most recent user message.
	intentType := contextbroker.IntentCustom
	var keywords []string
	var queryText string
	messages, err := cb.Store.ListMessages(session.ID, 5)
	if err == nil && len(messages) > 0 {
		// Find last user message.
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				_, keywords = ExtractIntent(messages[i].Content)
				intentType = classifyContextIntent(messages[i].Content)
				queryText = messages[i].Content
				break
			}
		}
	}

	intent := contextbroker.Intent{
		Type:      intentType,
		Keywords:  keywords,
		QueryText: queryText,
		Scope:     session.ProjectID,
		SessionID: session.ID,
		AgentID:   agent.ID,
	}

	packet, err := cb.ContextBroker.Fetch(ctx, intent)
	if err != nil {
		slog.Warn("broker: context enrichment failed", "err", err)
		return systemPrompt
	}

	if packet == nil || len(packet.Items) == 0 {
		return systemPrompt
	}

	formatted := contextbroker.FormatPacket(packet)
	if formatted == "" {
		return systemPrompt
	}

	slog.Info("broker: enriched system prompt with context",
		"items", packet.Manifest.ItemCount, "tokens", packet.TokenEstimate)

	return systemPrompt + "\n\n" + formatted
}

// classifyContextIntent maps user message keywords to a ContextBroker intent type.
func classifyContextIntent(userMessage string) string {
	lower := userMessage
	if len(lower) > 500 {
		lower = lower[:500]
	}

	// Simple keyword-based classification.
	switch {
	case matchesAny(lower, "debug", "error", "bug", "fix", "broken", "crash", "fail"):
		return contextbroker.IntentDebugIssue
	case matchesAny(lower, "write", "implement", "add", "create", "build", "code"):
		return contextbroker.IntentWriteCode
	case matchesAny(lower, "plan", "design", "feature", "epic", "roadmap"):
		return contextbroker.IntentPlanFeature
	case matchesAny(lower, "why", "decision", "adr", "chose", "rationale"):
		return contextbroker.IntentRecallDecision
	case matchesAny(lower, "resume", "continue", "pick up", "where we left"):
		return contextbroker.IntentResumeTask
	case matchesAny(lower, "boot", "start", "init", "setup", "project"):
		return contextbroker.IntentBootProject
	case matchesAny(lower, "review", "session", "history", "what happened"):
		return contextbroker.IntentReviewSession
	default:
		return contextbroker.IntentCustom
	}
}

// matchesAny returns true if the text contains any of the given substrings.
func matchesAny(text string, subs ...string) bool {
	lower := strings.ToLower(text)
	for _, sub := range subs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// pruneToolResultsInMemory replaces tool_result content blocks older than the
// last 2 tool-use rounds with compact references. This operates on the in-memory
// message slice without touching the DB.
func pruneToolResultsInMemory(messages []provider.ChatMessage) []provider.ChatMessage {
	// Count tool-use rounds from the end to find the cutoff.
	toolRounds := 0
	cutoffIdx := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_result" {
				toolRounds++
				break
			}
		}
		if toolRounds > ToolResultPruneAge {
			cutoffIdx = i
			break
		}
	}

	if cutoffIdx >= len(messages) {
		return messages // nothing to prune
	}

	pruned := 0
	result := make([]provider.ChatMessage, len(messages))
	copy(result, messages)

	for i := 0; i <= cutoffIdx; i++ {
		m := &result[i]
		if len(m.ContentBlocks) == 0 {
			continue
		}
		newBlocks := make([]provider.ContentBlock, len(m.ContentBlocks))
		copy(newBlocks, m.ContentBlocks)
		for j := range newBlocks {
			b := &newBlocks[j]
			if b.Type == "tool_result" && len(b.Content) > 200 {
				b.Content = fmt.Sprintf("[pruned: tool result, %d chars]", len(b.Content))
				pruned++
			}
		}
		m.ContentBlocks = newBlocks
	}

	if pruned > 0 {
		slog.Debug("broker: pruned tool results in-memory", "count", pruned, "cutoff_idx", cutoffIdx)
	}
	return result
}
