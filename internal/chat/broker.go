package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	tiamatotel "github.com/hollis-labs/tiamat-otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/store"
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

// ContextBroker assembles and manages context for chat turns.
type ContextBroker struct {
	Store     *store.Store
	BudgetPct float64 // fraction of context window to use (default 0.75)
}

// NewContextBroker creates a new ContextBroker with default settings.
func NewContextBroker(s *store.Store) *ContextBroker {
	return &ContextBroker{
		Store:     s,
		BudgetPct: DefaultBudgetPct,
	}
}

// AssembleContext builds the full context for a turn:
// 1. System prompt (from prompt templates or legacy agent + mode + workspace)
// 2. Recent messages (from session history)
// 3. Enforce budget ceiling
func (cb *ContextBroker) AssembleContext(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (string, []provider.ChatMessage, error) {
	_, span := tiamatotel.StartSpan(ctx, "conduit.broker.assembleContext")
	defer span.End()

	span.SetAttributes(
		attribute.String("conduit.session.id", session.ID),
		attribute.String("conduit.agent.id", agent.ID),
	)

	// 1. Build the system prompt using prompt templates.
	skillList := buildSkillList(cb.Store, agent.ID)
	systemPrompt := assembleSystemPromptFromTemplates(cb.Store, agent, mode, workspace, skillList)

	// 2. Load messages from DB. Start with a generous limit.
	messages, err := cb.Store.ListMessages(session.ID, 200)
	if err != nil {
		return "", nil, err
	}

	// 3. Convert to provider messages.
	chatMessages := make([]provider.ChatMessage, len(messages))
	for i, m := range messages {
		role := m.Role
		if role == "system" || role == "tool" {
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
		attribute.Int("conduit.broker.system_tokens", systemTokens),
		attribute.Int("conduit.broker.message_count", len(chatMessages)),
		attribute.Int("conduit.broker.total_tokens", totalTokens),
		attribute.Int("conduit.broker.budget", budget),
	)

	log.Printf("broker: assembled context — system=%d tokens, messages=%d, total=%d tokens (budget=%d)",
		systemTokens, len(chatMessages), totalTokens, budget)

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
func (cb *ContextBroker) PruneAfterTurn(sessionID string) error {
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
				log.Printf("broker: failed to compact message %s: %v", m.ID, err)
				continue
			}
			compacted++
		}
	}

	if compacted > 0 {
		log.Printf("broker: compacted %d tool messages in session %s", compacted, sessionID)
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

	log.Printf("broker: token gate — system=%d messages=%d tools=%d total=%d ceiling=%d",
		systemTokens, msgTokens, toolTokens, total, ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 1: Prune old tool results from messages (keep last 2 tool-use pairs).
	messages = pruneToolResultsInMemory(messages)
	msgTokens = EstimateMessagesTokens(messages)
	total = systemTokens + msgTokens + toolTokens
	breakdown.Messages = msgTokens
	breakdown.Total = total
	log.Printf("broker: after tool-result pruning — messages=%d total=%d ceiling=%d", msgTokens, total, ceiling)

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
	log.Printf("broker: after tool reduction — tools=%d total=%d ceiling=%d", len(tools), total, ceiling)

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
	log.Printf("broker: after message drop — messages=%d total=%d ceiling=%d", len(messages), total, ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 4: Still over — refuse to send.
	return messages, tools, breakdown, fmt.Errorf(
		"context exceeds hard ceiling after all reductions: %d tokens > %d ceiling", total, ceiling)
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
		log.Printf("broker: pruned %d tool results in-memory (before idx %d)", pruned, cutoffIdx)
	}
	return result
}
