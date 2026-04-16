package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

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
	// slot boundaries (e.g., Anthropic cache_control). The tools slice is
	// stringified into the Tools slot (S3b will replace with a cache pointer).
	// extraSystemPrefix captures dynamic per-turn additions (no-tools warning,
	// progressive discovery catalog, native tool guide) that vary with the
	// tool selection result. The returned SystemPrompt is the LEGACY
	// concatenation of all slots plus the prefix — used for budget enforcement
	// and telemetry. It is NOT the value callers should pass as
	// ChatRequest.SystemPrompt; callers should pass extraSystemPrefix there
	// directly so that static content flows exclusively through SlotBlocks.
	AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, tools []provider.ToolDefinition, extraSystemPrefix string, providerWindowSize int) (*SlotAssemblyResult, error)

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

// AssembleSlots builds a slot-based context window. Each named slot is sourced
// independently from raw inputs (agent profile, workspace, ContextBroker,
// session messages, selected tools) so provider adapters that exploit slot
// boundaries (e.g., Anthropic cache_control) can mark unchanged slots as
// cacheable. The legacy AssembleContext path remains available for callers
// that haven't migrated.
func (s *contextServiceImpl) AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, tools []provider.ToolDefinition, extraSystemPrefix string, providerWindowSize int) (*SlotAssemblyResult, error) {
	sources, err := s.client.AssembleSlotSources(ctx, session, agent, mode, workspace)
	if err != nil {
		return nil, err
	}

	cw := ctxpkg.NewContextWindow(providerWindowSize, s.estimator)

	cw.SetContent(ctxpkg.SlotSystem, sources.System)
	cw.SetContent(ctxpkg.SlotMemory, sources.Memory)
	cw.SetContent(ctxpkg.SlotAgent, sources.Agent)
	cw.SetContent(ctxpkg.SlotRules, sources.Rules)
	toolsContent := serializeToolsForSlot(tools)
	cw.SetContent(ctxpkg.SlotTools, toolsContent)
	cw.SetContent(ctxpkg.SlotSession, sources.Session)
	cw.SetContent(ctxpkg.SlotContext, sources.Context)
	cw.SetContent(ctxpkg.SlotConversation, serializeMessagesForSlot(sources.Messages))

	if sources.EnrichmentActive {
		cw.SetFlags(ctxpkg.SlotContext, ctxpkg.SlotFlags{EnrichmentActive: true})
	}
	if hasToolBlocks(sources.Messages) {
		cw.SetFlags(ctxpkg.SlotConversation, ctxpkg.SlotFlags{UsingTools: true})
	}

	blocks := cw.Assemble()

	// Legacy SystemPrompt: concatenation of every slot in order (including
	// Tools) plus the caller-provided dynamic prefix. Used by
	// EnforceTokenBudget, plugin filters, and telemetry — NOT by
	// ChatRequest.SystemPrompt (which carries only the per-turn prefix).
	systemPrompt := composeLegacySystemPrompt(sources, toolsContent, extraSystemPrefix)

	slog.Debug("context-service: slot assembly",
		"blocks", len(blocks), "used_tokens", cw.UsedTokens(),
		"budget", cw.TotalBudget, "compaction", cw.NeedsCompaction(),
		"tools", len(tools))

	return &SlotAssemblyResult{
		Blocks:          blocks,
		Window:          cw,
		SystemPrompt:    systemPrompt,
		Messages:        sources.Messages,
		NeedsCompaction: cw.NeedsCompaction(),
	}, nil
}

// serializeToolsForSlot stringifies tool definitions into a deterministic JSON
// blob so the Tools slot has stable cache-key behavior. S3b replaces this with
// a cache-pointer scheme that doesn't ship full defs in the prompt.
func serializeToolsForSlot(tools []provider.ToolDefinition) string {
	if len(tools) == 0 {
		return ""
	}
	data, err := json.Marshal(tools)
	if err != nil {
		slog.Warn("context-service: tool slot marshal failed", "err", err)
		return ""
	}
	return string(data)
}

// hasToolBlocks reports whether any conversation message carries tool_use or
// tool_result blocks; used to set the UsingTools flag on the conversation slot.
func hasToolBlocks(msgs []provider.ChatMessage) bool {
	for _, m := range msgs {
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}

// composeLegacySystemPrompt rebuilds the flat system prompt for callers that
// haven't migrated to SlotBlocks (e.g., EnforceTokenBudget, plugin filters,
// debug logging, the EmitContextAssembled event). It must account for all
// slot content that the Anthropic adapter will send in the system payload —
// including the Tools slot — so budget enforcement doesn't undercount. The
// prefix appears first so dynamic per-turn additions lead.
func composeLegacySystemPrompt(sources *chat.SlotSources, toolsContent, prefix string) string {
	parts := make([]string, 0, 8)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	for _, p := range []string{sources.System, sources.Agent, sources.Rules, toolsContent, sources.Session, sources.Memory, sources.Context} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
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
