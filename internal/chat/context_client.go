package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	Store          *store.Store
	BudgetPct      float64               // fraction of context window to use (default 0.75)
	ContextBroker  *contextbroker.Broker // universal context retrieval (nil = disabled)
	// HintDispatcher, when set, enables v2 dynamic hint selection via the
	// hint-selector peer agent (F5 / CW-20260420-0022). nil means the assembler
	// falls through to the v0/v1 static ThinkToolBlock path. Also requires
	// NANITE_THINK_BLOCK_V2_ENABLED=true in the environment.
	HintDispatcher HintDispatcher
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
	// hintOpts enables v2 dynamic hint selection when the ContextClient has a
	// HintDispatcher wired and NANITE_THINK_BLOCK_V2_ENABLED=true. nil means
	// the assembler falls back to the v0/v1 static ThinkToolBlock path.
	skillList := buildSkillList(cb.Store, agent.ID)
	var hintOpts *HintSelectOpts
	if cb.HintDispatcher != nil {
		hintOpts = &HintSelectOpts{
			Ctx:        ctx,
			Dispatcher: cb.HintDispatcher,
		}
	}
	systemPrompt := assembleSystemPromptFromTemplates(cb.Store, agent, mode, workspace, skillList, session.ID, hintOpts)

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

// SlotSources carries the raw, per-slot strings sourced for slot-based
// context assembly. The service layer composes these into a ContextWindow.
// Tools content is filled by the service layer after tool selection.
type SlotSources struct {
	System           string                 // think-tool block + workspace identity (no agent-specific text)
	Memory           string                 // formatted ContextBroker items where Source == "memory"
	Agent            string                 // agent.SystemPrompt + mode.PromptAddendum + skill list
	Rules            string                 // agent tags + tool allowlist (S4a expands)
	Session          string                 // session name, mode label, workspace name
	Context          string                 // formatted ContextBroker items where Source != "memory"
	UserContext      string                 // J10 (CW-20260426-0008): user-authored session context prompt + included docs.
	Messages         []provider.ChatMessage // conversation slot messages
	EnrichmentActive bool                   // true when Context slot was populated by the broker
}

// AssembleSlotSources builds the raw per-slot content for slot-based assembly.
// Memory and Context are split from the ContextBroker fetch by item.Source.
// The Tools slot is intentionally not populated here — the service layer
// fills it from the selected tool definitions after calling this method.
func (cb *ContextClient) AssembleSlotSources(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (*SlotSources, error) {
	_, span := feotel.StartSpan(ctx, "nanite.broker.assembleSlotSources")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.session.id", session.ID),
		attribute.String("nanite.agent.id", agent.ID),
	)

	// System slot — think-tool block + workspace identity. Agent-specific
	// content moves to the Agent slot. v0/v1/v2 selected by feature flags.
	var sysB strings.Builder
	var thinkBlock string
	if cb.HintDispatcher != nil && IsThinkBlockV2Enabled() {
		thinkBlock = ThinkToolBlockWithDispatch(ctx, cb.HintDispatcher, "", "", "")
	} else {
		thinkBlock = ThinkToolBlock()
	}
	sysB.WriteString(strings.TrimLeft(thinkBlock, "\n"))
	if workspace != nil && workspace.Name != "" {
		sysB.WriteString("\n\nWorkspace: ")
		sysB.WriteString(workspace.Name)
		if workspace.Description != "" {
			sysB.WriteString(" - ")
			sysB.WriteString(workspace.Description)
		}
	}

	// Agent slot — composed via prompt templates with skills, falling back to
	// raw agent + mode strings when no template is assigned. The agent slot
	// also carries the post-compaction disclosure (P8A) when one is fresh
	// for this session.
	skillList := buildSkillList(cb.Store, agent.ID)
	agentPrompt := assembleAgentSlotContent(cb.Store, agent, mode, skillList, session.ID)

	// Rules slot — agent tags + tool allowlist. S4a expands this.
	rules := buildRulesSlotContent(agent)

	// Session slot — small, stable identifiers.
	sessionContent := buildSessionSlotContent(session, mode, workspace)

	// Memory + Context — both sourced from ContextBroker; split by item.Source.
	var memoryContent, contextContent string
	enrichmentActive := false
	if cb.ContextBroker != nil {
		intent := cb.deriveIntent(session, agent)
		packet, err := cb.ContextBroker.Fetch(ctx, intent)
		if err != nil {
			slog.Warn("broker: slot enrichment failed", "err", err)
		} else if packet != nil && len(packet.Items) > 0 {
			memoryContent = formatPacketItemsBySource(packet, true)
			contextContent = formatPacketItemsBySource(packet, false)
			if contextContent != "" {
				enrichmentActive = true
			}
		}
	}

	// Conversation messages.
	messages, err := cb.Store.ListMessages(session.ID, 200)
	if err != nil {
		return nil, err
	}
	chatMessages := make([]provider.ChatMessage, len(messages))
	for i, m := range messages {
		role := m.Role
		if role == "system" || role == "tool" || role == RoleEnvelopeResponse {
			role = "user"
		}
		chatMessages[i] = provider.ChatMessage{Role: role, Content: m.Content}
	}

	// J10 (CW-20260426-0008): user context prompt + included documents.
	// Both are pinned and NOT compactable (SlotUserContext). The user context
	// prompt is authored in the bottom drawer. Included documents are injected
	// as pointers (name + summary) by default, or full content when
	// full_content=true. This slot composes with HandoffStash (CW-20260420-0024)
	// for compaction-survival — both are non-compactable pinned slots.
	// J11 (CW-20260426-0009) pin tool will extend this same pattern.
	userContextContent := buildUserContextSlot(cb.Store, session.ID)

	return &SlotSources{
		System:           sysB.String(),
		Memory:           memoryContent,
		Agent:            agentPrompt,
		Rules:            rules,
		Session:          sessionContent,
		Context:          contextContent,
		UserContext:      userContextContent,
		Messages:         chatMessages,
		EnrichmentActive: enrichmentActive,
	}, nil
}

// buildUserContextSlot assembles the SlotUserContext content from:
//  1. The session-scoped user context prompt (sessions.context_prompt).
//  2. Any included documents (documents.included=true), injected as pointer
//     (name + summary) or full content based on documents.full_content.
//
// Returns an empty string when neither is set, leaving the slot empty (no
// contribution to the system prompt for that turn). This is the desired default:
// documents and the context prompt are excluded unless explicitly included.
func buildUserContextSlot(s *store.Store, sessionID string) string {
	var parts []string

	// Session context prompt.
	if prompt, err := s.GetSessionContextPrompt(sessionID); err == nil && strings.TrimSpace(prompt) != "" {
		parts = append(parts, "## Session Context\n"+strings.TrimSpace(prompt))
	}

	// Included documents.
	if docs, err := s.GetIncludedDocuments(sessionID); err == nil && len(docs) > 0 {
		var docParts []string
		for _, doc := range docs {
			if doc.FullContent {
				docParts = append(docParts, fmt.Sprintf("### Document: %s\n%s", doc.Name, doc.Content))
			} else {
				// Pointer mode: name + summary only.
				summary := doc.Summary
				if summary == "" {
					summary = fmt.Sprintf("(document ID: %s, size: %d bytes)", doc.ID, doc.SizeBytes)
				}
				docParts = append(docParts, fmt.Sprintf("### Document: %s (pointer)\n%s", doc.Name, summary))
			}
		}
		if len(docParts) > 0 {
			parts = append(parts, "## Session Documents\n"+strings.Join(docParts, "\n\n"))
		}
	}

	return strings.Join(parts, "\n\n")
}

// deriveIntent extracts the broker intent from the session's recent user turn.
// Mirrors the logic from enrichWithContextBroker so slot- and legacy-paths
// produce identical broker queries.
func (cb *ContextClient) deriveIntent(session *store.Session, agent *store.AgentProfile) contextbroker.Intent {
	intentType := contextbroker.IntentCustom
	var keywords []string
	var queryText string
	messages, err := cb.Store.ListMessages(session.ID, 5)
	if err == nil && len(messages) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				_, keywords = ExtractIntent(messages[i].Content)
				intentType = classifyContextIntent(messages[i].Content)
				queryText = messages[i].Content
				break
			}
		}
	}
	return contextbroker.Intent{
		Type:      intentType,
		Keywords:  keywords,
		QueryText: queryText,
		Scope:     session.ProjectID,
		SessionID: session.ID,
		AgentID:   agent.ID,
	}
}

// formatPacketItemsBySource filters the packet to items where Source == "memory"
// (when memoryOnly is true) or Source != "memory" (when false), then formats
// the filtered subset using the same renderer as the legacy path.
func formatPacketItemsBySource(packet *contextbroker.ContextPacket, memoryOnly bool) string {
	if packet == nil {
		return ""
	}
	filtered := make([]contextbroker.ContextItem, 0, len(packet.Items))
	for _, it := range packet.Items {
		isMemory := it.Source == "memory"
		if memoryOnly == isMemory {
			filtered = append(filtered, it)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	sub := &contextbroker.ContextPacket{
		Items:    filtered,
		Manifest: packet.Manifest,
	}
	return contextbroker.FormatPacket(sub)
}

// assembleAgentSlotContent composes the agent-specific portion of the prompt
// (agent.SystemPrompt, mode addendum, skill list) without the workspace or
// think-tool sections that live in the System slot.
//
// When sessionID is non-empty and a fresh CompactionContract event exists for
// the session, the appropriate mode-anchored disclosure is appended (P8A,
// CW-20260420-0025). Disclosure lands in the agent slot because (a) the
// agent slot already carries mode-specific content, and (b) every system
// prompt assembly path passes through this function or its sibling
// assembleSystemPromptFromTemplates.
func assembleAgentSlotContent(s *store.Store, agent *store.AgentProfile, mode *store.AgentMode, skillList, sessionID string) string {
	vars := map[string]string{
		"agent_name":        agent.Name,
		"agent_description": agent.Description,
	}
	if mode != nil {
		vars["mode_addendum"] = mode.PromptAddendum
	}
	if skillList != "" {
		vars["skill_list"] = skillList
	}
	if agent.Tools != "" && agent.Tools != "[]" {
		vars["tools_allowlist"] = agent.Tools
	}
	if agent.Tags != "" && agent.Tags != "[]" {
		vars["agent_tags"] = agent.Tags
	}

	composed, err := s.ComposePromptForAgent(agent.ID, vars)
	if err != nil {
		slog.Warn("chat: agent slot ComposePromptForAgent failed — falling back", "err", err)
		composed = ""
	}
	if composed == "" {
		// Legacy fallback: agent prompt + mode addendum only (workspace lives in System slot).
		var b strings.Builder
		b.WriteString(agent.SystemPrompt)
		if mode != nil && mode.PromptAddendum != "" {
			b.WriteString("\n\n")
			b.WriteString(mode.PromptAddendum)
		}
		composed = b.String()
	}
	if skillList != "" {
		composed += "\n\nAvailable skills:\n" + skillList
	}
	if sessionID != "" {
		if disclosure := renderCompactionDisclosure(s, sessionID); disclosure != "" {
			composed += "\n\n" + disclosure
		}
	}
	return composed
}

// buildRulesSlotContent renders the Rules slot from the agent profile. S4a
// expands this with policy-layer rules; for now it surfaces tags + allowlist.
//
// Tags and tools are stored as JSON arrays in the agent profile; rendering
// them as raw JSON forces the LLM to parse — a Markdown bulleted list is
// cheaper to consume and more robust to surrounding-prose pattern-matching.
func buildRulesSlotContent(agent *store.AgentProfile) string {
	var b strings.Builder
	if items := parseJSONStringArray(agent.Tags); len(items) > 0 {
		b.WriteString("Agent tags:\n")
		for _, t := range items {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	if items := parseJSONStringArray(agent.Tools); len(items) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Tool allowlist:\n")
		for _, t := range items {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	return b.String()
}

// parseJSONStringArray parses a JSON string array like `["foo","bar"]` into
// a Go slice. Empty / null / parse-error input returns an empty slice so the
// caller can render nothing without branching on shape.
func parseJSONStringArray(raw string) []string {
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Defensive: a malformed tags/tools column shouldn't break the slot
		// render; log once at debug and return empty so the slot shows no
		// rules rather than raw JSON garbage.
		slog.Debug("chat: parseJSONStringArray failed", "raw", raw, "err", err)
		return nil
	}
	return out
}

// buildSessionSlotContent renders the Session slot — small, stable identifiers
// the model uses to anchor itself to the active session. Today's date anchors
// the LLM against drift toward training-cutoff dates in its outputs.
// Workspace is intentionally omitted here because SlotSystem already carries
// it — we don't want to waste tokens on a duplicate.
func buildSessionSlotContent(session *store.Session, mode *store.AgentMode, _ *store.Workspace) string {
	var b strings.Builder
	now := time.Now()
	fmt.Fprintf(&b, "Today: %s (%s)\n", now.Format("2006-01-02"), now.Format("Monday"))
	// Auto-generated chat titles (c17, c18) add no signal — only surface a
	// title if it looks user-assigned. Heuristic: > 4 chars or contains
	// a space is treated as intentional.
	if t := session.Title; t != "" && (len(t) > 4 || strings.Contains(t, " ")) {
		fmt.Fprintf(&b, "Session: %s\n", t)
	}
	if mode != nil && mode.Slug != "" && mode.Slug != "default" {
		fmt.Fprintf(&b, "Mode: %s\n", mode.Slug)
	}
	return b.String()
}

// EstimateTokens does a rough chars/4 estimation.
func EstimateTokens(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}

// PruneAfterTurn is retired in Phase 3 S3a. The destructive per-turn DB
// rewrite has been superseded by the in-memory slot compaction pipeline at
// internal/context/CompactionPipeline. Callers should no longer rely on
// database-level compaction; message.content rows are the append-only
// source of truth going forward. This method is preserved as a no-op with
// a deprecation log for one release cycle and will be deleted next.
func (cb *ContextClient) PruneAfterTurn(sessionID string) error {
	slog.Warn("broker: PruneAfterTurn is deprecated (slot compaction supersedes); no-op",
		"session_id", sessionID)
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
