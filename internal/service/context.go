package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tool/intent"
	"github.com/hollis-labs/nanite/internal/tool/stash"
)

// ContextService assembles system prompts, message history, and performs
// post-turn pruning. Supports both legacy (flat string) and slot-based
// context assembly.
type ContextService interface {
	// AssembleContext is the legacy path: returns a flat system prompt and messages.
	AssembleContext(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (systemPrompt string, messages []llmtypes.ChatMessage, err error)

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
	//
	// sessionMode (B1, CW-20260428-0009) is the resolved session-level
	// *store.Mode — pass nil when no session-mode pointer is set. Distinct
	// from the legacy `mode *store.AgentMode` argument which still feeds the
	// Agent slot.
	//
	// toolsLazyHint (G-HOT-SWAP-DEAD activation) is the LoadHint string
	// surfaced after S3b's tools-slot output when the caller's
	// chat.PartitionTools step produced a non-empty lazy set. Empty string
	// disables the hint append — preserves legacy behavior.
	AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, tools []llmtypes.ToolDefinition, extraSystemPrefix string, providerWindowSize int, sessionMode *store.Mode, toolsLazyHint string) (*SlotAssemblyResult, error)

	PruneAfterTurn(ctx context.Context, sessionID string) error
}

// SlotAssemblyResult holds the output of slot-based context assembly.
type SlotAssemblyResult struct {
	Blocks          []ctxpkg.SlotBlock
	Window          *ctxpkg.ContextWindow
	SystemPrompt    string                 // convenience: content of the system slot
	Messages        []llmtypes.ChatMessage // convenience: parsed from conversation slot
	NeedsCompaction bool
	// ToolCache describes this turn's tool-slot outcome. Nil when the S3b
	// tool-cache pipeline is inactive (deps missing or setting disabled).
	ToolCache *ToolCacheOutcome
	// Plan is the Context Broker's per-turn assembly plan: ordered slot
	// decisions (ship/skip/pointer) plus a stash of unshipped slot
	// content. SP-20260512-0008 W1A (CW-20260512-0104). Always populated;
	// callers can introspect for telemetry or use plan.Stash to recover
	// content the broker decided not to ship this turn.
	Plan contextbroker.AssemblyPlan
}

// HydrationState is the tool slot's content mode for a turn.
type HydrationState int

const (
	// StatePointer means the slot carries the compact pointer-summary.
	StatePointer HydrationState = iota
	// StateFull means the slot carries full defs for every in-scope tool.
	StateFull
	// StatePartial means the slot carries full defs for a subset of
	// categories + pointer-summary lines for the rest.
	StatePartial
)

// String renders a HydrationState for envelopes and telemetry.
func (h HydrationState) String() string {
	switch h {
	case StateFull:
		return "hydrated"
	case StatePartial:
		return "partial_hydrated"
	default:
		return "dehydrated"
	}
}

// ToolCacheOutcome describes what happened to the Tools slot this turn.
// Consumed by chat_generate to emit the slot_changed envelope variant (T5).
type ToolCacheOutcome struct {
	Prev            HydrationState
	Next            HydrationState
	Categories      []string // subset hydrated when Next == StatePartial; empty when StateFull or StatePointer
	Source          string   // intent.Source*
	Reasoning       string
	TokensBefore    int
	TokensAfter     int
	SelectionHash   string
	ClassifierLatMS int64 // milliseconds spent in the classifier
}

// ToolCacheOverrideStore looks up the session's /tools on|off pin. T6
// implements this; tests supply a stub.
type ToolCacheOverrideStore interface {
	Get(sessionID string) intent.Override
}

// toolCacheSessionState is the per-session memo the ContextService keeps so
// the next turn can report accurate prev→next transitions and token deltas.
type toolCacheSessionState struct {
	State      HydrationState
	SlotTokens int
}

// contextServiceImpl delegates to the existing chat.ContextClient for content
// retrieval, and wraps results in the slot-based ContextWindow for budgeting,
// caching, and compaction.
type contextServiceImpl struct {
	client    *chat.ContextClient
	estimator ctxpkg.TokenEstimator
	// S3b tool-cache pipeline deps — all optional. When any is nil or the
	// user setting is disabled, AssembleSlots falls back to S3a behavior
	// (full tool defs in the Tools slot every turn).
	stashManager *stash.Manager
	classifier   intent.Classifier
	overrides    ToolCacheOverrideStore
	settingsFunc func() *store.UserSettings
	// lastToolCache tracks the last-emitted hydration state and the token
	// count of the previous Tools slot per session. Used for T5 transition
	// detection and to report accurate before/after deltas across all
	// transitions — not just pointer→x — Copilot review #3095049986.
	lastToolCache sync.Map // map[sessionID]toolCacheSessionState
}

// ContextServiceConfig holds dependencies for constructing a ContextService.
type ContextServiceConfig struct {
	Client    *chat.ContextClient
	Estimator ctxpkg.TokenEstimator // nil = DefaultEstimator

	// S3b — optional. Together these enable the tool-slot cache-and-pointer
	// path. If any is nil, AssembleSlots uses the S3a path (always hydrate).
	StashManager *stash.Manager
	Classifier   intent.Classifier
	Overrides    ToolCacheOverrideStore
	SettingsFunc func() *store.UserSettings
}

// NewContextService wraps an existing ContextClient as a ContextService.
func NewContextService(cfg ContextServiceConfig) ContextService {
	est := cfg.Estimator
	if est == nil {
		est = ctxpkg.DefaultEstimator{}
	}
	return &contextServiceImpl{
		client:       cfg.Client,
		estimator:    est,
		stashManager: cfg.StashManager,
		classifier:   cfg.Classifier,
		overrides:    cfg.Overrides,
		settingsFunc: cfg.SettingsFunc,
	}
}

// AssembleContext is the legacy path — delegates directly to ContextClient.
func (s *contextServiceImpl) AssembleContext(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) (string, []llmtypes.ChatMessage, error) {
	return s.client.AssembleContext(ctx, session, agent, mode, workspace)
}

// AssembleSlots builds a slot-based context window. Each named slot is sourced
// independently from raw inputs (agent profile, workspace, ContextBroker,
// session messages, selected tools) so provider adapters that exploit slot
// boundaries (e.g., Anthropic cache_control) can mark unchanged slots as
// cacheable. The legacy AssembleContext path remains available for callers
// that haven't migrated.
//
// When the S3b tool-cache pipeline is active (ToolCacheEnabled=true and the
// stash + classifier deps are wired), the Tools slot carries a compact
// pointer-summary by default and hydrates full defs on detected intent.
// Otherwise it carries the full JSON-serialized defs every turn (S3a).
//
// toolsLazyHint, when non-empty, is appended after the S3b/S3a output —
// the G-HOT-SWAP-DEAD layer's pointer at the lazy partition. The caller
// (chat-service) decides whether the partition is active and renders the
// hint via chat.RenderToolLazyHint; AssembleSlots only attaches it.
func (s *contextServiceImpl) AssembleSlots(ctx context.Context, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, tools []llmtypes.ToolDefinition, extraSystemPrefix string, providerWindowSize int, sessionMode *store.Mode, toolsLazyHint string) (*SlotAssemblyResult, error) {
	sources, err := s.client.AssembleSlotSources(ctx, session, agent, mode, workspace, sessionMode)
	if err != nil {
		return nil, err
	}

	// Tools slot content is built here (not in AssembleSlotSources) because
	// the classifier needs the resolved tool definitions and the messages.
	// G-HOT-SWAP-DEAD lazy hint is appended so the agent sees the inline
	// (essential) surface plus a pointer at the lazy remainder.
	toolsContent, outcome := s.buildToolsSlot(ctx, session, tools, sources.Messages)
	if toolsLazyHint != "" {
		if toolsContent != "" {
			toolsContent = toolsContent + "\n\n" + toolsLazyHint
		} else {
			toolsContent = toolsLazyHint
		}
	}

	// SP-20260512-0008 W1A (CW-20260512-0104): the Context Broker is the
	// slot-assembly DECIDER, not a content enricher. Build the slot store
	// (slot_name → content), pass it to DecideAssembly along with the
	// resolved intent + mode, and apply the returned plan to the
	// ContextWindow. Skipped slots are stashed in plan.Stash; pointer
	// substitutions get a synthetic marker. The Universal slot sits at
	// position 0 (per ctxpkg.SlotOrder) with empty content today — Sprint 2 /
	// T2.4 wires the content. The decision is deterministic and free of I/O.
	store := slotSourceMap(sources, toolsContent)
	modeSlug := ""
	if sessionMode != nil {
		modeSlug = sessionMode.Slug
	} else if mode != nil {
		modeSlug = mode.Slug
	}
	plan := contextbroker.DecideAssembly(contextbroker.AssemblyInput{
		Intent:    sources.Intent,
		ModeSlug:  modeSlug,
		SlotOrder: ctxpkg.SlotOrder,
		Sources:   store,
		Budgets:   ctxpkg.DefaultBudgets(),
		AgentID:   agent.ID,
		SessionID: session.ID,
	})

	cw := ctxpkg.NewContextWindow(providerWindowSize, s.estimator)
	for _, d := range plan.Decisions {
		// Empty content is a no-op for SetContent (Assemble drops empty
		// slots from the wire), so ActionSkip with empty content is
		// already the desired behavior — but we set explicitly so the
		// per-slot CacheKey/TokenCount get computed (defensive against
		// future SetContent semantics changes).
		cw.SetContent(d.SlotName, d.Content)
	}
	// SlotConversation is independent of the decider's content map —
	// messages are always serialized and shipped. The decider doesn't
	// have authority over conversation history.
	cw.SetContent(ctxpkg.SlotConversation, serializeMessagesForSlot(sources.Messages))

	if sources.EnrichmentActive {
		cw.SetFlags(ctxpkg.SlotContext, ctxpkg.SlotFlags{EnrichmentActive: true})
	}
	if hasToolBlocks(sources.Messages) {
		cw.SetFlags(ctxpkg.SlotConversation, ctxpkg.SlotFlags{UsingTools: true})
	}
	if outcome == nil || outcome.Next != StatePointer {
		// Pointer-only tools slot leaves UsingTools=false; a full-or-partial
		// hydrated slot marks UsingTools=true so downstream budget/plugin
		// filters can react.
		cw.SetFlags(ctxpkg.SlotTools, ctxpkg.SlotFlags{UsingTools: true})
	}

	blocks := cw.Assemble()

	// Legacy SystemPrompt: concatenation of every slot in order (including
	// Tools) plus the caller-provided dynamic prefix. Used by
	// EnforceTokenBudget, plugin filters, and telemetry — NOT by
	// ChatRequest.SystemPrompt (which carries only the per-turn prefix).
	// Now walks the plan so skipped/pointer slots are reflected accurately
	// in the budget enforcer's view of the prompt.
	systemPrompt := composeLegacySystemPrompt(plan, extraSystemPrefix)

	slog.Debug("context-service: slot assembly",
		"blocks", len(blocks), "used_tokens", cw.UsedTokens(),
		"budget", cw.TotalBudget, "compaction", cw.NeedsCompaction(),
		"tools", len(tools), "tool_cache_state", toolCacheStateForLog(outcome),
		"plan", contextbroker.DecisionSummary(plan))

	return &SlotAssemblyResult{
		Blocks:          blocks,
		Window:          cw,
		SystemPrompt:    systemPrompt,
		Messages:        sources.Messages,
		NeedsCompaction: cw.NeedsCompaction(),
		ToolCache:       outcome,
		Plan:            plan,
	}, nil
}

// slotSourceMap projects SlotSources (+ the freshly built tools content)
// into the slot_name → content map the assembly decider consumes. The
// SlotConversation entry is intentionally absent — conversation history
// is set on the window after the decider runs (the decider has no
// authority over conversation messages).
func slotSourceMap(sources *chat.SlotSources, toolsContent string) map[string]string {
	return map[string]string{
		ctxpkg.SlotUniversal:   sources.Universal,
		ctxpkg.SlotSystem:      sources.System,
		ctxpkg.SlotMemory:      sources.Memory,
		ctxpkg.SlotAgent:       sources.Agent,
		ctxpkg.SlotMode:        sources.Mode,
		ctxpkg.SlotRules:       sources.Rules,
		ctxpkg.SlotTools:       toolsContent,
		ctxpkg.SlotSession:     sources.Session,
		ctxpkg.SlotContext:     sources.Context,
		ctxpkg.SlotUserContext: sources.UserContext,
		// SlotHandoff is auto-populated post-compaction by the harness
		// (Glass-3 / -4); the decider treats it as ambient — when content
		// arrives, the slot ships. Source is empty here; the actual
		// content lives in the ContextWindow after compaction wires it.
		ctxpkg.SlotHandoff: "",
	}
}

// toolCacheActive returns true when every S3b dep is wired and the user has
// not disabled the feature.
func (s *contextServiceImpl) toolCacheActive() bool {
	if s.stashManager == nil || s.classifier == nil {
		return false
	}
	if s.settingsFunc == nil {
		// Deps are wired but caller didn't give us a settings reader — treat
		// as enabled (defaults in migration 012 say enabled=true).
		return true
	}
	us := s.settingsFunc()
	if us == nil {
		return true
	}
	return us.ToolCacheEnabled
}

// buildToolsSlot produces the content for the Tools slot plus (when the S3b
// pipeline runs) an outcome for the caller to surface in telemetry and the
// slot_changed envelope.
func (s *contextServiceImpl) buildToolsSlot(ctx context.Context, session *store.Session, tools []llmtypes.ToolDefinition, msgs []llmtypes.ChatMessage) (string, *ToolCacheOutcome) {
	if !s.toolCacheActive() {
		return serializeToolsForSlot(tools), nil
	}

	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}

	st := s.stashManager.GetOrBuild(sessionID, tools)

	override := intent.OverrideNone
	if s.overrides != nil && sessionID != "" {
		override = s.overrides.Get(sessionID)
	}

	input := intent.Input{
		UserTurn:            lastUserTurn(msgs),
		AvailableCategories: st.CategoriesList(),
		ToolNames:           toolNames(tools),
		Override:            override,
	}

	start := nowFunc()
	result, _ := s.classifier.Classify(ctx, input)
	latency := nowFunc().Sub(start).Milliseconds()

	content, next, cats := renderToolsSlot(st, result)

	prevMemo := s.loadLastToolCache(sessionID)
	after := s.estimator.Estimate(content)
	s.storeLastToolCache(sessionID, toolCacheSessionState{State: next, SlotTokens: after})

	// TokensBefore is the actual prior Tools-slot size when we have a memo
	// for this session, so full→pointer and partial→pointer transitions
	// report a real reduction. Fresh sessions with no memo default to the
	// pointer-summary size — that matches the "what we would have used"
	// baseline from S3a→S3b plus a first-turn inherits-pointer convention.
	before := prevMemo.SlotTokens
	if before == 0 {
		before = s.estimator.Estimate(st.SummaryText)
	}

	// Approximate tokens saved vs. S3a: the per-turn delta between "if we had
	// shipped the full defs" and what we actually shipped. When hydrated, this
	// is zero or near-zero; when pointer, this captures the win.
	fullTokens := s.estimator.Estimate(serializeToolsForSlot(defsFromStash(st)))
	tokensSaved := fullTokens - after
	if tokensSaved < 0 {
		tokensSaved = 0
	}

	// T8 — single structured INFO line per turn. Tail-readable; no metrics
	// SDK dependency. Fields mirror the telemetry counters/histogram the
	// plan describes (nanite.tool_cache.classify.source, .hydration, etc.).
	llmErrs := 0
	if result.Source == intent.SourceFallback {
		llmErrs = 1
	}
	slog.Info("context-service: tool_cache classify",
		"session_id", sessionID,
		"source", result.Source,
		"hydration", next.String(),
		"prev_hydration", prevMemo.State.String(),
		"categories", cats,
		"confidence", result.Confidence,
		"latency_ms", latency,
		"tokens_before", before,
		"tokens_after", after,
		"tokens_saved", tokensSaved,
		"llm_errors", llmErrs,
		"selection_hash", st.SelectionHash,
	)

	return content, &ToolCacheOutcome{
		Prev:            prevMemo.State,
		Next:            next,
		Categories:      cats,
		Source:          result.Source,
		Reasoning:       result.Reasoning,
		TokensBefore:    before,
		TokensAfter:     after,
		SelectionHash:   st.SelectionHash,
		ClassifierLatMS: latency,
	}
}

// defsFromStash returns every def in the stash, sorted by category then name —
// matches the shape renderToolsSlot produces when fully hydrating.
func defsFromStash(st *stash.Stash) []llmtypes.ToolDefinition {
	if st == nil {
		return nil
	}
	cats := st.CategoriesList()
	out := make([]llmtypes.ToolDefinition, 0, len(st.FullDefs))
	for _, c := range cats {
		for _, name := range st.Categories[c] {
			if d, ok := st.FullDefs[name]; ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// renderToolsSlot produces the serialized content for the Tools slot plus the
// resulting hydration state. When the classifier returns hydrate=true with an
// empty Categories list (fail-open or confident "hydrate all"), every def is
// written out. Otherwise only the named categories' defs are written, followed
// by the stash's compact summary for the remaining categories (D4 partial).
func renderToolsSlot(st *stash.Stash, r intent.Result) (content string, state HydrationState, cats []string) {
	if st == nil || len(st.FullDefs) == 0 {
		return "", StatePointer, nil
	}
	if !r.Hydrate {
		return st.SummaryText, StatePointer, nil
	}

	allCats := st.CategoriesList()
	picked := r.Categories
	if len(picked) == 0 || containsAll(picked, allCats) {
		// Hydrate everything.
		all := make([]llmtypes.ToolDefinition, 0, len(st.FullDefs))
		for _, c := range allCats {
			for _, name := range st.Categories[c] {
				if d, ok := st.FullDefs[name]; ok {
					all = append(all, d)
				}
			}
		}
		raw, err := json.Marshal(all)
		if err != nil {
			slog.Warn("context-service: full-hydrate marshal failed", "err", err)
			return st.SummaryText, StatePointer, nil
		}
		return string(raw), StateFull, nil
	}

	// Partial: serialize picked defs; append summary lines for the rest.
	defs := st.DefsForCategories(picked)
	raw, err := json.Marshal(defs)
	if err != nil {
		slog.Warn("context-service: partial-hydrate marshal failed", "err", err)
		return st.SummaryText, StatePointer, nil
	}

	var b strings.Builder
	b.WriteString(string(raw))
	remaining := diffCategories(allCats, picked)
	if len(remaining) > 0 {
		b.WriteString("\n\n")
		b.WriteString(partialSummary(st, remaining))
	}
	return b.String(), StatePartial, picked
}

// partialSummary emits a short multi-line summary covering the categories not
// hydrated this turn. It's purposely thinner than the stash's full pointer-
// summary so the partial hydration stays compact.
func partialSummary(st *stash.Stash, cats []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%d categor%s held as pointer — call `/tools on` to force-load]\n", len(cats), pluralCat(len(cats)))
	for _, c := range cats {
		fmt.Fprintf(&b, "- %s(%d)\n", c, len(st.Categories[c]))
	}
	return strings.TrimRight(b.String(), "\n")
}

func pluralCat(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// containsAll returns true when every element of inner is present in outer.
func containsAll(inner, outer []string) bool {
	set := make(map[string]struct{}, len(outer))
	for _, s := range outer {
		set[s] = struct{}{}
	}
	for _, s := range inner {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return len(inner) == len(outer) // same cardinality required for "all"
}

// diffCategories returns elements of all not present in picked, preserving
// order.
func diffCategories(all, picked []string) []string {
	pset := make(map[string]struct{}, len(picked))
	for _, p := range picked {
		pset[p] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, c := range all {
		if _, ok := pset[c]; !ok {
			out = append(out, c)
		}
	}
	return out
}

func (s *contextServiceImpl) loadLastToolCache(sessionID string) toolCacheSessionState {
	if sessionID == "" {
		return toolCacheSessionState{}
	}
	if v, ok := s.lastToolCache.Load(sessionID); ok {
		return v.(toolCacheSessionState)
	}
	return toolCacheSessionState{}
}

func (s *contextServiceImpl) storeLastToolCache(sessionID string, st toolCacheSessionState) {
	if sessionID == "" {
		return
	}
	s.lastToolCache.Store(sessionID, st)
}

// lastUserTurn returns the content of the most recent user-role message, or
// empty string when there is none.
func lastUserTurn(msgs []llmtypes.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			if msgs[i].Content != "" {
				return msgs[i].Content
			}
			// Block-form message — concat text blocks.
			var b strings.Builder
			for _, blk := range msgs[i].ContentBlocks {
				if blk.Type == "text" && blk.Text != "" {
					if b.Len() > 0 {
						b.WriteString(" ")
					}
					b.WriteString(blk.Text)
				}
			}
			if b.Len() > 0 {
				return b.String()
			}
		}
	}
	return ""
}

func toolNames(tools []llmtypes.ToolDefinition) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func toolCacheStateForLog(o *ToolCacheOutcome) string {
	if o == nil {
		return "s3a-passthrough"
	}
	return o.Next.String()
}

// nowFunc is overridable for tests that need deterministic classifier latency.
var nowFunc = time.Now

// serializeToolsForSlot stringifies tool definitions into a deterministic JSON
// blob so the Tools slot has stable cache-key behavior. S3b replaces this with
// a cache-pointer scheme that doesn't ship full defs in the prompt.
func serializeToolsForSlot(tools []llmtypes.ToolDefinition) string {
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
func hasToolBlocks(msgs []llmtypes.ChatMessage) bool {
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
// debug logging, the EmitContextAssembled event). It must account for the
// slot content the Anthropic adapter will send in the system payload — so
// budget enforcement doesn't undercount.
//
// SP-20260512-0008 W1A (CW-20260512-0104): walks the broker's assembly plan
// (in SlotOrder) so skipped slots are absent from the budget view and
// pointer slots reflect their substituted size. The dynamic prefix appears
// first so per-turn additions lead.
func composeLegacySystemPrompt(plan contextbroker.AssemblyPlan, prefix string) string {
	parts := make([]string, 0, len(plan.Decisions)+1)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	for _, d := range plan.Decisions {
		if d.SlotName == ctxpkg.SlotConversation {
			// Conversation lives in the messages payload, not the system
			// prompt; excluded from the legacy system-prompt view as it
			// always was.
			continue
		}
		if d.Content == "" {
			continue
		}
		parts = append(parts, d.Content)
	}
	return strings.Join(parts, "\n\n")
}

func (s *contextServiceImpl) PruneAfterTurn(_ context.Context, sessionID string) error {
	return s.client.PruneAfterTurn(sessionID)
}

// serializeMessagesForSlot converts messages to a string for token estimation
// in the conversation slot.
func serializeMessagesForSlot(msgs []llmtypes.ChatMessage) string {
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
