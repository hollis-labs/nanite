package service

import (
	"context"
	"log"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// ContextService assembles system prompts, message history, and performs
// post-turn pruning. Supports both legacy (flat string) and slot-based
// context assembly.
type ContextService interface {
	// AssembleContext is the legacy path: returns a flat system prompt and messages.
	AssembleContext(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (systemPrompt string, messages []provider.ChatMessage, err error)

	// AssembleSlots returns slot blocks for provider adapters that can exploit
	// slot boundaries (e.g., Anthropic cache_control). Falls back to legacy
	// assembly when the slot window isn't configured.
	AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, providerWindowSize int) (*SlotAssemblyResult, error)

	PruneAfterTurn(ctx context.Context, sessionID string) error
}

// SlotAssemblyResult holds the output of slot-based context assembly.
type SlotAssemblyResult struct {
	Blocks         []ctxpkg.SlotBlock
	Window         *ctxpkg.ContextWindow
	SystemPrompt   string               // convenience: content of the system slot
	Messages       []provider.ChatMessage // convenience: parsed from conversation slot
	NeedsCompaction bool
}

// contextServiceImpl delegates to the existing chat.ContextClient for content
// retrieval, and wraps results in the slot-based ContextWindow for budgeting,
// caching, and compaction.
type contextServiceImpl struct {
	client    *chat.ContextClient
	estimator ctxpkg.TokenEstimator
}

// ContextServiceConfig holds dependencies for constructing a ContextService.
type ContextServiceConfig struct {
	Client    *chat.ContextClient
	Estimator ctxpkg.TokenEstimator // nil = DefaultEstimator
}

// NewContextService wraps an existing ContextClient as a ContextService.
func NewContextService(cfg ContextServiceConfig) ContextService {
	est := cfg.Estimator
	if est == nil {
		est = ctxpkg.DefaultEstimator{}
	}
	return &contextServiceImpl{client: cfg.Client, estimator: est}
}

// AssembleContext is the legacy path — delegates directly to ContextClient.
func (s *contextServiceImpl) AssembleContext(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (string, []provider.ChatMessage, error) {
	return s.client.AssembleContext(ctx, session, agent, mode, workspace)
}

// AssembleSlots builds a slot-based context window. It uses the legacy
// ContextClient to fetch content, then distributes it across named slots
// for budget management and caching.
func (s *contextServiceImpl) AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, providerWindowSize int) (*SlotAssemblyResult, error) {
	// Use the legacy client to get the system prompt and messages.
	systemPrompt, messages, err := s.client.AssembleContext(ctx, session, agent, mode, workspace)
	if err != nil {
		return nil, err
	}

	cw := ctxpkg.NewContextWindow(providerWindowSize, s.estimator)

	// Populate slots from the legacy assembly output.
	// For now, the system prompt goes into the System slot. As we add
	// memory, agent, and rules slots in later phases, we'll split the
	// system prompt into its constituent parts.
	cw.SetContent(ctxpkg.SlotSystem, systemPrompt)

	// Serialize conversation messages for the conversation slot.
	convContent := serializeMessagesForSlot(messages)
	cw.SetContent(ctxpkg.SlotConversation, convContent)

	blocks := cw.Assemble()

	log.Printf("context-service: slot assembly — %d blocks, %d/%d tokens, compaction=%v",
		len(blocks), cw.UsedTokens(), cw.TotalBudget, cw.NeedsCompaction())

	return &SlotAssemblyResult{
		Blocks:          blocks,
		Window:          cw,
		SystemPrompt:    systemPrompt,
		Messages:        messages,
		NeedsCompaction: cw.NeedsCompaction(),
	}, nil
}

func (s *contextServiceImpl) PruneAfterTurn(_ context.Context, sessionID string) error {
	return s.client.PruneAfterTurn(sessionID)
}

// serializeMessagesForSlot converts messages to a string for token estimation
// in the conversation slot.
func serializeMessagesForSlot(msgs []provider.ChatMessage) string {
	total := 0
	for _, m := range msgs {
		total += len(m.Role) + len(m.Content) + 3 // ": " + "\n"
		for _, b := range m.ContentBlocks {
			total += len(b.Text) + len(b.Content)
		}
	}
	buf := make([]byte, 0, total)
	for _, m := range msgs {
		buf = append(buf, m.Role...)
		buf = append(buf, ": "...)
		buf = append(buf, m.Content...)
		for _, b := range m.ContentBlocks {
			buf = append(buf, b.Text...)
			buf = append(buf, b.Content...)
		}
		buf = append(buf, '\n')
	}
	return string(buf)
}
