package chat

import (
	"log"

	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
)

// DefaultBudgetPct is the default fraction of the context window to use.
const DefaultBudgetPct = 0.75

// DefaultContextWindow is the fallback context window size in tokens.
const DefaultContextWindow = 200000

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
// 1. System prompt (from agent + mode + workspace)
// 2. Recent messages (from session history)
// 3. Enforce budget ceiling
func (cb *ContextBroker) AssembleContext(session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (string, []provider.ChatMessage, error) {
	// 1. Build the system prompt.
	systemPrompt := assembleSystemPrompt(agent, mode, workspace)

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
// Tool-role messages older than 3 turns from the end with content longer
// than 500 chars are replaced with a compaction marker.
func (cb *ContextBroker) PruneAfterTurn(sessionID string) error {
	messages, err := cb.Store.ListMessages(sessionID, 1000)
	if err != nil {
		return err
	}

	if len(messages) <= 3 {
		return nil
	}

	// Messages older than the last 3 turns (last 6 messages as a proxy: 3 user + 3 assistant).
	cutoff := len(messages) - 6
	if cutoff < 0 {
		cutoff = 0
	}

	compacted := 0
	for i := 0; i < cutoff; i++ {
		m := messages[i]
		if m.Role == "tool" && !m.IsCompacted && len(m.Content) > 500 {
			if err := cb.Store.UpdateMessageContent(m.ID, "[compacted: tool output truncated]", true); err != nil {
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
