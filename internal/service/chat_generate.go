package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/go-providers/provider"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/store"
)

// noToolsWarningPrefix is prepended to the per-turn system prefix when the
// agent has no MCP tools and progressive discovery is not active. Kept as a
// package-level const so composeExtraSystemPrefix stays a pure function of its
// inputs.
const noToolsWarningPrefix = "IMPORTANT: You have no tools available in this session. Do NOT attempt to call any tools — all tool calls will fail. Respond with text only. If the user's request requires tools (data lookup, task management, code execution, etc.), clearly explain that this agent is not configured with the necessary tools and suggest they switch to an agent that has tools configured.\n\n"

// composeConfig captures the per-turn flags that shape the system prefix
// composed by composeExtraSystemPrefix. All fields are set by the caller in
// generateResponse; the helper itself performs no side effects.
type composeConfig struct {
	// noTools is true when the agent has zero MCP tools and progressive
	// discovery is not active. Triggers the no-tools warning prefix.
	noTools bool
	// progressiveActive is true when progressive tool discovery is in effect
	// for this turn.
	progressiveActive bool
	// progressiveCatalog is the catalog string to inject when progressiveActive
	// is true. Empty string is tolerated (skipped).
	progressiveCatalog string
}

// composeExtraSystemPrefix builds the per-turn system prompt prefix: optional
// no-tools warning, optional progressive discovery catalog, then the native
// tool guide.
//
// Phase 0 item 22: this used to also append a per-tool "## Tool Overrides"
// markdown block (composed via go-toolbroker's enricher/WithEnricher) after
// the native guide. Cut entirely per the operator's 2026-08-18 resolution —
// no port-forward — because the tool_enrichments write path was already
// dead (18a-cut-dead-storage-and-config), making the override block
// structurally inert. See decision log §11.
func composeExtraSystemPrefix(cfg composeConfig) string {
	var b strings.Builder
	if cfg.noTools {
		b.WriteString(noToolsWarningPrefix)
	}
	if cfg.progressiveActive && cfg.progressiveCatalog != "" {
		b.WriteString(cfg.progressiveCatalog)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimLeft(nativeToolGuide, "\n"))
	return b.String()
}

// hasUsableTools reports whether the turn exposes any tools to the LLM.
// Built-in tools (for example dev_* and self-service tools) count — the
// "no tools" warning should only fire when the final selection is empty.
func hasUsableTools(tools []llmtypes.ToolDefinition) bool {
	return len(tools) > 0
}

// nativeToolGuide is injected into every system prompt so the LLM correctly
// uses native dev/general tools.
//
// CW-20260512-0100 (R3): the "card_show with type=report-card/document-viewer
// requires sources — if you didn't fetch the data this turn, render plain
// text" rule was removed from this guide and relocated into card_show's own
// tool description (internal/mcp/self_tools.go). Agents reading card_show
// right before invocation now see the rule in the authoritative location,
// not buried in a generic-tools guide.
const nativeToolGuide = `

## Native Tool Usage

Parameter shape:
- **All paths must be absolute** (start with /). Never use ~ or relative paths.
- **dev_glob** takes TWO separate params: pattern (relative glob like **/*.md) and directory (absolute path to the project root). Do NOT put the full path in the pattern.
- **dev_grep** takes TWO separate params: pattern (regex) and directory (absolute path). Same rule — keep them separate.
- **dev_read/dev_write/dev_edit**: path must be absolute.
- **web_fetch**: many news/social sites block automated requests. Works best with APIs, docs sites, and raw content URLs.
- **Allowed directories**: configured per workspace. Files outside the configured allowed paths will be rejected.

Workflow:
- **Discover before read.** Use dev_glob or dev_grep first if you aren't already sure the path exists. Running dev_read on a speculative path wastes a tool call.
- **Cache pointer pattern.** When a tool result ends with a footer like ` + "`[TRUNCATED — full result cached as tool_result://<ULID> ...]`" + `, treat the preview as incomplete evidence. Read relevant omitted sections before claiming a complete review or current-state conclusion. Don't re-invoke the source tool to get more. Call ` + "`fetch_tool_result`" + ` with the ULID to retrieve slices, or ` + "`search_tool_result`" + ` to regex-match across the full cached body. Use json_pointer to select JSON fields (including /stdout for Python output); follow has_more/next_offset for paging. Read large source collections in bounded sections rather than concatenating entire corpora.
- **Parallelize independent calls.** If two lookups don't depend on each other, request them in the same assistant turn — the harness executes tool blocks in parallel.
- **Stop when done.** Extra tool calls don't add trust; they just dilute the grounding.

Tool contract discovery:
- If you're unsure about a tool's input shape, call ` + "`tool_describe(name=\"<tool>\")`" + ` first. It returns the schema plus 1-3 golden examples — cheaper than failing the real call repeatedly.`

// generateResponse loads context, calls the provider, streams events, and saves
// the result. This is the refactored version of Engine.generateResponse — it
// uses service interfaces instead of direct Store/sync.Map access, and unifies
// the duplicated ToolClient/MCPManager execution into ToolService.Execute.
func (s *chatServiceImpl) generateResponse(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	lifecycle := &generationLifecycle{startTime: time.Now()}

	// CW-20260512-0006: removed the 5-minute wall-clock deadline that used to
	// wrap ctx via context.WithTimeout(ctx, generateResponseTimeout). The
	// deadline was bad agent DX (interrupted long-running work that was
	// progressing normally — c160 turn 8) and bad user DX (no way to
	// distinguish "stuck" from "still working"). Cancellation now comes
	// from three intentional sources only, all flowing through the same
	// `ctx`:
	//   1. Takeover — a new HandleMessage/RetryLastMessage call for the same
	//      session cancels the prior generation via launchGeneration's
	//      registerGeneration takeover semantics.
	//   2. Lifecycle shutdown — process-wide drain bridges bgCtx to cancel.
	//   3. User-initiated stop — POST /api/sessions/{id}/chat/cancel resolves
	//      the registered cancel via CancelActiveGeneration.
	// Hung sync subagents are bounded by the per-row timeout_seconds reaper
	// (CW-20260512-0002), not by a parent wall-clock deadline.

	ctx, span := feotel.StartSpan(ctx, "nanite.service.generateResponse")
	span.SetAttributes(
		attribute.String("nanite.session.id", sessionID),
		attribute.String("nanite.message.id", assistantMsgID),
	)
	defer span.End()

	// CW-20260420-0032: PTY observability — track whether a pty_turn_start
	// was emitted so the deferred closer can emit the matching terminal event
	// (pty_turn_complete or pty_turn_failed). ptyTurnStarted is set to true
	// once we emit pty_turn_start; ptyTurnSucceeded is set to true only when
	// we reach the stream_end path. The defer emits pty_turn_failed for all
	// other exits (early return, context cancellation, error).
	defer func() {
		if !lifecycle.ptyTurnStarted || s.sessionEventWriter == nil {
			return
		}
		eventType := EventPTYTurnFailed
		if lifecycle.ptyTurnSucceeded {
			eventType = EventPTYTurnComplete
		}
		payload := fmt.Sprintf(`{"message_id":%q,"provider":%q,"duration_ms":%d}`,
			assistantMsgID, lifecycle.ptyProviderName, time.Since(lifecycle.startTime).Milliseconds())
		s.sessionEventWriter.WriteSessionEvent(
			context.Background(), sessionID, eventType, lifecycle.ptyProviderName, payload)
	}()

	// CW-20260418-0043 diagnostic — track iteration reached for the defer log.
	var diagCurrentIter = -1

	defer func() {
		diagLogDeferReached(sessionID, assistantMsgID, diagCurrentIter, "")
		close(ch)
		// CW-20260418-0100: hold the stream's ring buffer for a grace
		// window after completion so an SSE client that was disconnected
		// across the final events (tab backgrounded, proxy idle-close)
		// can reconnect with its EventID cursor and replay — instead of
		// silently losing stream_end. PR #66 review #7.
		s.streams.ScheduleCleanup(assistantMsgID, 60*time.Second)

		// Broadcast presence: stream ended.
		s.streams.ClearActivePresence(sessionID)
		s.streams.BroadcastPresence(chat.PresenceEvent{
			Type:      "stream_end",
			SessionID: sessionID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}()

	// --- Load session ---
	prepareResult := s.prepareTurn(ctx, sessionID, userContent, ch)
	if prepareResult.directive == generationTerminate {
		return
	}
	setup := prepareResult.setup

	initializeResult := s.initializeRun(ctx, sessionID, assistantMsgID, setup, lifecycle, ch)
	if initializeResult.directive == generationTerminate {
		return
	}
	run := initializeResult.run
	if run.startCancel != nil {
		defer run.startCancel()
	}

generationLoop:
	for run.loop.iteration = 0; ; run.loop.iteration++ {
		// CW-20260418-0043 diagnostic.
		diagCurrentIter = run.loop.iteration
		diagLogIterStart(ctx, sessionID, assistantMsgID, run.loop.iteration, ch)

		requestResult := s.requestProviderIteration(ctx, sessionID, assistantMsgID, setup, run, ch)
		switch requestResult.directive {
		case generationRetryIteration:
			run.loop.iteration-- // the retry isn't a fresh turn
			continue
		case generationFinishRun:
			break generationLoop
		case generationTerminate:
			return
		}

		consumeResult := s.consumeProviderIteration(ctx, sessionID, assistantMsgID, setup, run, requestResult.attempt, ch)
		switch consumeResult.directive {
		case generationRetryIteration:
			run.loop.iteration-- // the retry isn't a fresh turn
			continue
		case generationContinueIteration:
			continue
		case generationFinishRun:
			break generationLoop
		case generationTerminate:
			return
		}

		settleResult := s.settleToolTurn(ctx, sessionID, assistantMsgID, setup, run, consumeResult.turn, ch)
		switch settleResult.directive {
		case generationContinueIteration:
			continue
		case generationFinishRun:
			break generationLoop
		case generationTerminate:
			return
		}
	}

	finalizeResult := s.finalizeRun(ctx, sessionID, assistantMsgID, setup, run, lifecycle, ch)
	if finalizeResult.directive == generationTerminate {
		return
	}

}

// ---------------------------------------------------------------------------
// Private helper methods
// ---------------------------------------------------------------------------

// persistPartialAssistant saves a partial or interrupted assistant message row
// so the turn survives a page refresh when an early-return error path fires
// before the canonical post-loop CreateMessage call (CW-20260419-0019).
//
// content is the streamed text accumulated so far; if empty the placeholder
// "[generation interrupted]" is stored so the row always exists.  The message
// is saved with metadata `{"had_error":true}` so the frontend rehydration path
// (loadPersistedErrorState / useChat.ts) can distinguish it from a normal turn.
//
// Errors are logged but not propagated — this is a best-effort persistence call
// on the way out of an error path; the caller is already returning an error to
// the client.
//
// CW-20260512-0002 subtodo (d): when the early-return is caused by a hung
// subagent (parent has an active subagent_runs row at this moment), SKIP the
// placeholder row entirely. Refresh-survivability isn't load-bearing for the
// subagent-caused branch because the parent's next user turn dispatches a
// fresh subagent — the user pings again and the chat history stays clean.
// A structured slog.Warn line is emitted so the suppression remains
// observable / alertable. Genuine parent-stream failures (provider timeout,
// budget refusal, panic) flow through the normal path.
//
// PR #138 review #2: callers that have already run a suppression classification
// upstream (via suppressSurfaceIfSubagentCaused or surfaceErrorOrSuppress and
// observed `false`) should call persistPartialAssistantPreClassified instead
// to avoid a redundant ActiveSubagentRunForParent query on the hot error path.
// This wrapper performs the lookup for sites that haven't classified yet
// (e.g. the post-pause rate-budget paths at lines 1065/1123 that end the turn
// without an error surface).
func (s *chatServiceImpl) persistPartialAssistant(sessionID, assistantMsgID, agentID, content string) {
	if s.suppressSurfaceIfSubagentCaused(sessionID, "persistPartialAssistant", content) {
		return
	}
	s.persistPartialAssistantPreClassified(sessionID, assistantMsgID, agentID, content)
}

// persistPartialAssistantPreClassified is the lower-level persistence path
// for callers that have already classified the suppression state upstream
// (via suppressSurfaceIfSubagentCaused or surfaceErrorOrSuppress returning
// false). It writes the placeholder row without re-querying ActiveSubagentRunForParent,
// halving the DB hits on the deadline / surfaceErrorOrSuppress error paths.
//
// Contract: callers MUST have observed a `false` suppression decision for
// this sessionID earlier in the same call stack — otherwise a subagent-caused
// failure may surface as a `[generation interrupted]` row, violating the
// CW-20260512-0002 subtodo (d) suppression contract.
//
// Use persistPartialAssistant when no prior classification exists.
func (s *chatServiceImpl) persistPartialAssistantPreClassified(sessionID, assistantMsgID, agentID, content string) {
	if content == "" {
		content = "[generation interrupted]"
	}
	structured := chat.WrapResponse(content, "default", nil, nil, false, true)
	structuredJSON := structured.MarshalContent()
	msg := &store.Message{
		ID:        assistantMsgID,
		SessionID: sessionID,
		AgentID:   agentID,
		Role:      "assistant",
		Content:   structuredJSON,
		Metadata:  `{"had_error":true}`,
	}
	if err := s.store.CreateMessage(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, msg); err != nil {
		slog.Warn("chat-service: persistPartialAssistant: failed to save partial message",
			"session_id", sessionID, "msg_id", assistantMsgID, "err", err)
	}
}

// persistPartialAssistantCanceled saves a partial assistant message for a
// CLEAN cancellation path (intentional stop: takeover / shutdown /
// user-initiated cancel). The persisted row has StructuredMessage.Flags.HasError=false
// and the metadata column does NOT carry `had_error:true`, so FE rehydration
// (loadPersistedErrorState / useChat.ts) does not render the turn as an error.
//
// PR #139 review (Copilot): the cancel path previously routed through
// persistPartialAssistantPreClassified, which always writes HasError=true +
// `had_error:true` metadata. That mislabels intentional stops as error turns
// and risks triggering "error turn" UI/analytics behavior. This helper is the
// cancel-specific persistence path; all other partial-persist call sites
// continue to use the error-flag-true variants.
//
// Like the other persist-partial helpers, the call is best-effort — errors
// are logged but not propagated.
func (s *chatServiceImpl) persistPartialAssistantCanceled(sessionID, assistantMsgID, agentID, content string) {
	if content == "" {
		content = "[generation interrupted]"
	}
	// hasError=false: clean stop, not an error turn.
	structured := chat.WrapResponse(content, "default", nil, nil, false, false)
	structuredJSON := structured.MarshalContent()
	msg := &store.Message{
		ID:        assistantMsgID,
		SessionID: sessionID,
		AgentID:   agentID,
		Role:      "assistant",
		Content:   structuredJSON,
		// Metadata intentionally omitted (empty) — no `had_error` flag for
		// an intentional cancel.
	}
	if err := s.store.CreateMessage(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, msg); err != nil {
		slog.Warn("chat-service: persistPartialAssistantCanceled: failed to save partial message",
			"session_id", sessionID, "msg_id", assistantMsgID, "err", err)
	}
}

// surfaceErrorOrSuppress emits a chat.ErrorEnvelopeDelta + chat.ErrorEvent
// pair on ch unless the parent session is currently waiting on a hung
// subagent — in which case both surfaces are suppressed and the
// suppression is logged in structured form. callers MUST still skip
// any subsequent persistPartialAssistant call when this returns true;
// persistPartialAssistant has the same gate so a stray call is no-op,
// but skipping it early keeps the early-return path symmetric.
//
// Returns true when the surfaces were suppressed (caller should
// short-circuit to its return). Returns false when the surfaces were
// emitted normally.
//
// CW-20260512-0002 subtodo (d) audit-each: applies the same suppression
// rule the deadline_5min site uses to every ErrorCodeInternal early-return
// inside generateResponse's tool-use loop so the FE never sees a
// subagent-caused internal_error event.
func (s *chatServiceImpl) surfaceErrorOrSuppress(
	ch chan chat.StreamEvent,
	sessionID, callSite, message string,
	details map[string]interface{},
	partialContent string,
) bool {
	if s.suppressSurfaceIfSubagentCaused(sessionID, callSite, partialContent) {
		return true
	}
	ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, message, details)
	ch <- chat.ErrorEvent(chat.ErrorCodeInternal, message, details)
	return false
}

// suppressSurfaceIfSubagentCaused returns true when the parent session has an
// active subagent_runs row (status='running') AT THIS MOMENT — the strong
// signal that the early-return / deadline / stream-error path the caller is
// on was caused by a hung subagent rather than a real parent-side failure.
//
// When true, the caller MUST skip whatever FE-visible artifact it was about
// to emit (placeholder message, ErrorCodeInternal envelope, ErrorEvent). A
// structured slog.Warn line is emitted in lieu of those artifacts so the
// suppression remains observable: greppable on `event=subagent_timeout_suppressed_surface`.
//
// When false (no active subagent OR the lookup itself errors — fail open),
// the caller proceeds with its normal artifact emission.
//
// Parameters:
//   - parentSessionID: the chat session whose loop is about to early-return.
//   - callSite: short identifier (e.g. "persistPartialAssistant",
//     "deadline_5min") used in the structured log so multiple call sites
//     remain distinguishable.
//   - partialContent: streamed bytes accumulated by the parent so far; logged
//     for diagnostics so post-hoc audits know whether anything reached the
//     client before suppression.
//
// CW-20260512-0002 subtodo (d).
func (s *chatServiceImpl) suppressSurfaceIfSubagentCaused(parentSessionID, callSite, partialContent string) bool {
	if s.store == nil || parentSessionID == "" {
		return false
	}
	runID, role, childSessionID, ok, err := s.store.ActiveSubagentRunForParent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, parentSessionID)
	if err != nil {
		// Fail open: a DB hiccup here must not silently drop a real
		// parent-side error surface. Log it and let the caller emit
		// normally.
		slog.Warn("chat-service: subagent classification lookup failed; surfacing normally",
			"session_id", parentSessionID, "call_site", callSite, "err", err)
		return false
	}
	if !ok {
		return false
	}
	slog.Warn("subagent_timeout_suppressed_surface",
		"session_id", parentSessionID,
		"subagent_run_id", runID,
		"role", role,
		"child_session_id", childSessionID,
		"call_site", callSite,
		"partial_bytes", len(partialContent),
	)
	return true
}

// Compact-recovery trigger kinds. CW-20260418-0099: the pipeline serves two
// distinct failure modes that share the same remedy; the kind is threaded
// through so logs / slot_changed reasoning reflect the actual trigger
// instead of hardcoding "context-overflow" for rate-budget failures too.
const (
	compactTriggerContextOverflow = "context_overflow"
	compactTriggerRateBudget      = "rate_budget_exceeded"
	compactTriggerBudgetGate      = "budget_gate" // pre-loop budget ceiling (enforceBudgetOrCompact)
)

// recoverFromContextOverflow runs the CompactionPipeline synchronously.
// Returns the rebuilt chat messages and tools plus true on success;
// false when the feature flag is off, the summarizer isn't available, or
// the pipeline produced no new stages.
//
// Stage gating depends on triggerKind (PR #68 review #3):
//   - compactTriggerContextOverflow: uses pipeline.Run(), which respects
//     Window.NeedsCompaction() — the model-context-window gate. The
//     normal case; a context-overflow error implies the window is full.
//   - compactTriggerRateBudget: uses pipeline.RunForce(), which bypasses
//     the gate. Rate-budget overflow fires even when the model context
//     window is well under capacity, so gating on NeedsCompaction would
//     skip every stage (observed in c9 UAT — "no stages" in ~1ms).
//
// triggerKind is surfaced in logs and the slot_changed "reasoning"
// string so an operator reading journal output or the dev panel can
// tell which failure mode fired the compaction.
//
// Caller is expected to have already classified the incoming error via
// ctxpkg.IsCompactRecoverable. ls.compactRecoverableAttempts is incremented
// by the caller only after this function returns ok=true so a refused
// recovery surfaces a distinct error message instead of masquerading as
// "even after compaction". On refused recovery the caller saturates
// compactRecoverableAttempts at maxCompactRecoverableAttempts so the same
// request cannot re-enter this path. PR #68 review #1 +
// CW-20260419-0018 (one-shot → multi-attempt cap).
//
// Folded from BLG-20260410-003 — plan §T9.
func (s *chatServiceImpl) recoverFromContextOverflow(
	ctx context.Context,
	sessionID string,
	result *SlotAssemblyResult,
	agent *store.AgentProfile,
	chatMessages []llmtypes.ChatMessage,
	tools []llmtypes.ToolDefinition,
	ch chan chat.StreamEvent,
	triggerMsg string,
	triggerKind string,
) ([]llmtypes.ChatMessage, []llmtypes.ToolDefinition, bool) {
	if triggerKind == "" {
		triggerKind = compactTriggerContextOverflow
	}
	if result == nil || result.Window == nil {
		return chatMessages, tools, false
	}
	settings, _ := s.store.GetUserSettings(ctx)
	if settings == nil || !settings.ContextOverflowRecovery {
		slog.Info("chat-service: compact-recoverable recovery disabled by user setting",
			"session_id", sessionID, "trigger_kind", triggerKind, "trigger", triggerMsg)
		return chatMessages, tools, false
	}
	summarizer := s.buildSummarizer(settings)
	if summarizer == nil {
		slog.Warn("chat-service: compact-recoverable recovery skipped; no summarizer available",
			"session_id", sessionID, "trigger_kind", triggerKind)
		return chatMessages, tools, false
	}

	// Glass-4 (CW-20260502-0015): universal for every session (28-cut-session-intent-classifier)
	// — ensure a self-authored Glass-4 handoff exists before compaction runs.
	// Glass-4 stash itself is written proactively by the agent's
	// `handoff_stash` self-tool during normal turns, OR (fallback) at
	// compaction time below before stages run.
	sess, _ := s.store.GetSession(ctx, sessionID)

	if _, err := s.ensureGlass4HandoffPreCompact(ctx, sess, chatMessages, ch); err != nil {
		slog.Warn("chat-service: glass-4 pre-compaction handoff fallback failed (non-fatal)",
			"session_id", sessionID, "err", err)
	}

	pipeline := &ctxpkg.CompactionPipeline{
		Window:                result.Window,
		Estimator:             ctxpkg.DefaultEstimator{},
		Summarizer:            summarizer,
		Mode:                  classifyModeFromAgentTags(agent),
		ConversationMessages:  chatMessages,
		SessionID:             sessionID,
		CompactionEventWriter: NewCompactionEventWriter(s.store),
	}

	tokensBefore := result.Window.UsedTokens()
	if s.events != nil {
		s.events.EmitPreCompact(ctx, sessionID, len(chatMessages), triggerKind)
	}

	// CW-20260418-0099 bugfix: Run() short-circuits when Window.NeedsCompaction()
	// is false — and NeedsCompaction evaluates against the MODEL's context
	// window (e.g. 200K for Sonnet), not the per-minute rate budget (30–40K).
	// A request that fits in the context window but exceeds the rate budget
	// would get zero stages applied and the caller would bail with "failed
	// after retry" without ever actually compacting. For rate-budget
	// triggers we use RunForce, which runs every stage unconditionally —
	// the shrinkage is the whole point of the recovery at that point.
	var cr *ctxpkg.CompactionResult
	var err error
	if triggerKind == compactTriggerRateBudget {
		cr, err = pipeline.RunForce(ctx)
	} else {
		cr, err = pipeline.Run(ctx)
	}
	if err != nil {
		slog.Warn("chat-service: compact-recoverable recovery pipeline failed",
			"err", err, "session_id", sessionID, "trigger_kind", triggerKind)
		return chatMessages, tools, false
	}
	if cr == nil || len(cr.StagesApplied) == 0 {
		slog.Warn("chat-service: compact-recoverable recovery produced no stages",
			"session_id", sessionID, "trigger_kind", triggerKind, "trigger", triggerMsg)
		return chatMessages, tools, false
	}

	tokensAfter := result.Window.UsedTokens()
	chatMessages = pipeline.ConversationMessages
	result.Messages = chatMessages

	// Glass-4 (CW-20260502-0015): post-compaction handoff inject, universal
	// for every session (28-cut-session-intent-classifier). Reads the latest
	// Glass-4 envelope for this session and populates SlotHandoff
	// (AutoInject=true). No-op for sessions without a Glass-4 stash. Runs
	// before Assemble() so SlotHandoff content is in the freshly-built block
	// list.
	if _, err := InjectGlass4HandoffSlot(s.store, result.Window, sessionID, ch); err != nil {
		slog.Warn("chat-service: glass-4 handoff inject failed (non-fatal)",
			"session_id", sessionID, "err", err)
	}

	result.Blocks = result.Window.Assemble()
	result.NeedsCompaction = result.Window.NeedsCompaction()
	result.SystemPrompt = rebuildLegacySystemPrompt(result.Window)

	reasoningPrefix := "Provider returned context-overflow"
	if triggerKind == compactTriggerRateBudget {
		reasoningPrefix = "Provider request exceeded per-minute rate budget"
	}
	if emitErr := chat.EmitSlotChangedEvent(ch, chat.SlotChangedV1{
		Slot:         ctxpkg.SlotConversation,
		Change:       chat.SlotChangeSummarized,
		Reasoning:    fmt.Sprintf("%s; synchronously compacted %d stage(s).", reasoningPrefix, len(cr.StagesApplied)),
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}); emitErr != nil {
		slog.Warn("chat-service: slot_changed emit failed during compact-recoverable recovery",
			"err", emitErr, "trigger_kind", triggerKind)
	}
	if s.events != nil {
		s.events.EmitPostCompact(ctx, sessionID, tokensBefore-tokensAfter, cr.StagesApplied)
	}

	slog.Info("chat-service: compact-recoverable recovery ran",
		"session_id", sessionID,
		"tokens_before", tokensBefore,
		"tokens_after", tokensAfter,
		"stages", cr.StagesApplied,
		"trigger_kind", triggerKind,
		"trigger", triggerMsg,
	)

	return chatMessages, tools, true
}

// emitToolSlotChangedIfNeeded translates a ToolCacheOutcome into a
// slot_changed envelope when the Tools slot's hydration state flipped. The
// zero-transition case (e.g., pointer → pointer across two ambient chat
// turns) produces no event so the user sees a card only when something
// actually changed.
func emitToolSlotChangedIfNeeded(ch chan chat.StreamEvent, outcome *ToolCacheOutcome) {
	if outcome == nil || ch == nil {
		return
	}
	if outcome.Prev == outcome.Next {
		return
	}
	payload := chat.SlotChangedV1{
		Slot:         ctxpkg.SlotTools,
		Change:       toolSlotChangeKindFor(outcome.Next),
		Reasoning:    outcome.Reasoning,
		TokensBefore: outcome.TokensBefore,
		TokensAfter:  outcome.TokensAfter,
		Categories:   append([]string(nil), outcome.Categories...),
		HelpLink:     chat.SlotChangedHelpLinkTools,
	}
	if err := chat.EmitSlotChangedEvent(ch, payload); err != nil {
		slog.Warn("chat-service: tool slot_changed emit failed", "err", err)
	}
}

// toolSlotChangeKindFor maps a HydrationState to the matching envelope
// change kind.
func toolSlotChangeKindFor(s HydrationState) string {
	switch s {
	case StateFull:
		return chat.SlotChangeHydrated
	case StatePartial:
		return chat.SlotChangePartialHydrated
	default:
		return chat.SlotChangeDehydrated
	}
}

// assembleTurnContext drives slot-based context assembly for the current turn.
// It builds a *SlotAssemblyResult that carries the slot blocks (for
// SlotBlocks-aware providers), the legacy concatenated SystemPrompt (for
// telemetry / budget enforcement), the conversation messages, and the
// ContextWindow for the compaction pipeline. On error it writes an error
// event to the stream and returns; callers should just `return` on non-nil
// err without emitting again.
//
// Phase 0 item 21 ("Cut Modes, in full") removed the `mode *store.AgentMode`
// and `sessionMode *store.Mode` parameters this used to take. Phase 0 item
// 20 (retire workspaces) removed the `workspace *store.Workspace` parameter
// — the in-app `workspaces` table it sourced is retired in full.
func (s *chatServiceImpl) assembleTurnContext(
	ctx context.Context,
	session *store.Session,
	agent *store.AgentProfile,
	tools []llmtypes.ToolDefinition,
	extraSystemPrefix string,
	providerName, model string,
	ch chan chat.StreamEvent,
	toolsLazyHint string,
) (*SlotAssemblyResult, error) {
	windowSize := s.contextWindowSize(providerName, model)
	result, err := s.context.AssembleSlots(ctx, session, agent, tools, extraSystemPrefix, windowSize, toolsLazyHint)
	if err != nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to assemble context",
			map[string]interface{}{"raw": err.Error()})
		return nil, err
	}
	return result, nil
}

// rebuildLegacySystemPrompt recomputes the flat system prompt from the
// ContextWindow's current slot contents. Called after compaction stages
// may have modified slot content (e.g., dropping enrichment) so that
// downstream consumers (EnforceTokenBudget, telemetry) see accurate sizes.
func rebuildLegacySystemPrompt(cw *ctxpkg.ContextWindow) string {
	parts := make([]string, 0, len(ctxpkg.SlotOrder))
	for _, name := range ctxpkg.SlotOrder {
		if name == ctxpkg.SlotConversation {
			continue
		}
		if s := cw.Slot(name); s != nil && s.Content != "" {
			parts = append(parts, s.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// slotBlocksFor projects the context package's SlotBlock onto the
// provider package's mirror type. Forwards b.Changed from context
// tracking so the Anthropic adapter (internal/llm/anthropic) can
// emit cache_control on the last unchanged slot block when its
// budget permits. The adapter's cachePlan enforces Anthropic's
// 4-marker cap; see internal/llm/anthropic/cache_plan.go.
//
// CW-20260419-0007 (closed): the original Changed=true workaround
// here predated the in-tree Anthropic adapter and was made obsolete
// when adapter-side budgeting landed alongside this commit.
func slotBlocksFor(result *SlotAssemblyResult) []llmtypes.SlotBlock {
	if result == nil || len(result.Blocks) == 0 {
		return nil
	}
	out := make([]llmtypes.SlotBlock, 0, len(result.Blocks))
	for _, b := range result.Blocks {
		if b.Content == "" {
			continue
		}
		out = append(out, llmtypes.SlotBlock{
			Name:     b.SlotName,
			Content:  b.Content,
			CacheKey: b.CacheKey,
			Changed:  b.Changed,
		})
	}
	return out
}

// contextWindowSize returns the token budget for the context window.
// Priority: user_settings override → models.dev catalog → DefaultContextWindowSize.
func (s *chatServiceImpl) contextWindowSize(providerName, model string) int {
	if s.store != nil {
		settings, err := s.store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
		if err == nil && settings != nil && settings.ContextWindowTokens > 0 {
			return settings.ContextWindowTokens
		}
	}
	if s.modelCatalog != nil && providerName != "" && model != "" {
		if m, ok := s.modelCatalog.Get(providerName, model); ok && m.Limit.ContextWindow > 0 {
			return m.Limit.ContextWindow
		}
	}
	return 0
}

// enforceBudgetOrCompact is the pre-loop budget gate. When the conversation
// slot exceeds its budget it runs the compaction pipeline (drop enrichment →
// summarize oldest → strip tool blocks) and emits a slot_changed envelope so
// the frontend can surface the rewrite. The per-iteration EnforceTokenBudget
// call inside the tool loop remains as the hard-ceiling safety net.
func (s *chatServiceImpl) enforceBudgetOrCompact(
	ctx context.Context,
	sessionID string,
	result *SlotAssemblyResult,
	agent *store.AgentProfile,
	chatMessages []llmtypes.ChatMessage,
	tools []llmtypes.ToolDefinition,
	ch chan chat.StreamEvent,
) ([]llmtypes.ChatMessage, []llmtypes.ToolDefinition) {
	if result == nil || result.Window == nil || !result.NeedsCompaction {
		return chatMessages, tools
	}

	settings, _ := s.store.GetUserSettings(ctx)
	summarizer := s.buildSummarizer(settings)

	// Glass-4 (CW-20260502-0015): universal for every session
	// (28-cut-session-intent-classifier) — ensure a self-authored Glass-4
	// handoff exists before this pre-loop compaction runs.
	sess, _ := s.store.GetSession(ctx, sessionID)

	if _, err := s.ensureGlass4HandoffPreCompact(ctx, sess, chatMessages, ch); err != nil {
		slog.Warn("chat-service: glass-4 pre-compaction handoff fallback failed (non-fatal)",
			"session_id", sessionID, "err", err)
	}

	pipeline := &ctxpkg.CompactionPipeline{
		Window:                result.Window,
		Estimator:             ctxpkg.DefaultEstimator{},
		Summarizer:            summarizer,
		Mode:                  classifyModeFromAgentTags(agent),
		ConversationMessages:  chatMessages,
		SessionID:             sessionID,
		CompactionEventWriter: NewCompactionEventWriter(s.store),
	}

	tokensBefore := result.Window.UsedTokens()
	if s.events != nil {
		s.events.EmitPreCompact(ctx, sessionID, len(chatMessages), compactTriggerBudgetGate)
	}
	cr, err := pipeline.Run(ctx)
	if err != nil {
		slog.Warn("chat-service: compaction pipeline failed; ceiling enforcer will catch", "err", err, "session_id", sessionID)
		return chatMessages, tools
	}
	if cr == nil || len(cr.StagesApplied) == 0 {
		return chatMessages, tools
	}

	tokensAfter := result.Window.UsedTokens()
	chatMessages = pipeline.ConversationMessages
	result.Messages = chatMessages

	// Glass-4 (CW-20260502-0015): post-compaction handoff inject, universal
	// for every session — same flow as recoverFromContextOverflow. See
	// InjectGlass4HandoffSlot for the no-op gates.
	if _, err := InjectGlass4HandoffSlot(s.store, result.Window, sessionID, ch); err != nil {
		slog.Warn("chat-service: glass-4 handoff inject failed (non-fatal)",
			"session_id", sessionID, "err", err)
	}

	result.Blocks = result.Window.Assemble()
	result.NeedsCompaction = result.Window.NeedsCompaction()
	result.SystemPrompt = rebuildLegacySystemPrompt(result.Window)

	if emitErr := chat.EmitSlotChangedEvent(ch, chat.SlotChangedV1{
		Slot:         ctxpkg.SlotConversation,
		Change:       chat.SlotChangeSummarized,
		Reasoning:    fmt.Sprintf("Conversation slot exceeded budget; %d compaction stage(s) applied.", len(cr.StagesApplied)),
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}); emitErr != nil {
		slog.Warn("chat-service: slot_changed emit failed", "err", emitErr)
	}
	if s.events != nil {
		s.events.EmitPostCompact(ctx, sessionID, tokensBefore-tokensAfter, cr.StagesApplied)
	}
	if s.pluginHost != nil {
		stages := append([]string(nil), cr.StagesApplied...)
		s.goTracked("emit.context-compacted", func(context.Context) {
			s.pluginHost.EmitContextCompacted(sessionID, tokensBefore-tokensAfter, stages)
		})
	}

	return chatMessages, tools
}

// buildSummarizer is the chat-service-bound form of BuildSummarizer.
func (s *chatServiceImpl) buildSummarizer(settings *store.UserSettings) ctxpkg.Summarizer {
	return BuildSummarizer(s.providers, s.store, settings)
}

// storeCompactionEventReader bridges CompactionEventStore to
// ctxpkg.CompactionEventReader (P8A, CW-20260420-0025). The chat assembly path
// uses this reader to decide whether to inject a CompactionContract disclosure
// for the current turn.
type storeCompactionEventReader struct {
	s CompactionEventStore
}

func (r storeCompactionEventReader) GetLatestCompactionEvent(ctx context.Context, sessionID string) (*ctxpkg.CompactionEvent, error) {
	evt, err := r.s.GetLatestCompactionEvent(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if evt == nil {
		return nil, nil
	}
	out := ctxpkg.CompactionEvent{
		ID:                   evt.ID,
		SessionID:            evt.SessionID,
		CoverageWindowStart:  evt.CoverageWindowStart,
		CoverageWindowEnd:    evt.CoverageWindowEnd,
		EvictedCachePointers: append([]string(nil), evt.EvictedCachePointers...),
		PreservedSources:     append([]string(nil), evt.PreservedSources...),
		SummaryMode:          evt.SummaryMode,
		SummaryTokenCount:    evt.SummaryTokenCount,
		OriginalTokenCount:   evt.OriginalTokenCount,
		HandoffStashID:       evt.HandoffStashID,
		StagesApplied:        append([]string(nil), evt.StagesApplied...),
		CreatedAt:            evt.CreatedAt,
	}
	return &out, nil
}

// NewCompactionEventReader returns a ctxpkg.CompactionEventReader backed by the
// given store. Exported so api / chat / context packages can share the bridge
// without importing the unexported adapter directly.
func NewCompactionEventReader(s CompactionEventStore) ctxpkg.CompactionEventReader {
	return storeCompactionEventReader{s: s}
}

// storeCompactionEventWriter bridges CompactionEventStore to
// ctxpkg.CompactionEventWriter (P8 CompactionContract — write side,
// CW-20260420-0027). All three production CompactionPipeline{} construction
// sites (internal/api/sessions.go's manual /compact endpoint,
// assembleTurnContext's compact-recoverable path, and the pre-loop
// enforceBudgetOrCompact gate) assign this adapter so the pipeline's
// post-stage event emission (compaction.go's runStages) actually persists a
// compaction_events row, which storeCompactionEventReader / the
// CompactionContract disclosure (internal/chat/context.go) reads back on the
// next turn. Mirrors storeCompactionEventReader's field-by-field translation
// above, just in the opposite direction.
type storeCompactionEventWriter struct {
	s CompactionEventStore
}

func (w storeCompactionEventWriter) WriteCompactionEvent(ctx context.Context, event ctxpkg.CompactionEvent) error {
	out := store.CompactionEvent{
		ID:                   event.ID,
		SessionID:            event.SessionID,
		CoverageWindowStart:  event.CoverageWindowStart,
		CoverageWindowEnd:    event.CoverageWindowEnd,
		EvictedCachePointers: append([]string(nil), event.EvictedCachePointers...),
		PreservedSources:     append([]string(nil), event.PreservedSources...),
		SummaryMode:          event.SummaryMode,
		SummaryTokenCount:    event.SummaryTokenCount,
		OriginalTokenCount:   event.OriginalTokenCount,
		HandoffStashID:       event.HandoffStashID,
		StagesApplied:        append([]string(nil), event.StagesApplied...),
		CreatedAt:            event.CreatedAt,
	}
	return w.s.WriteCompactionEvent(ctx, out)
}

// NewCompactionEventWriter returns a ctxpkg.CompactionEventWriter backed by the
// given store. Exported so api / chat / context packages can share the bridge
// without importing the unexported adapter directly.
func NewCompactionEventWriter(s CompactionEventStore) ctxpkg.CompactionEventWriter {
	return storeCompactionEventWriter{s: s}
}

// BuildSummarizer resolves the provider+model used to summarize compacted
// conversation spans. UserSettings.summarizer_* take precedence; gaps are
// filled by the default-resolver (user_settings.default_* → providers.
// default_model). Returns nil when the chosen provider is not registered
// OR when the resolver chain is dry — Stage 2 of the compaction pipeline
// is nil-safe and skips. CW-20260526-0003 removed the Go-literal terminal
// fallback; an operator who hasn't configured a default sees compaction
// skip (with a log line) instead of routing to a stale hardcoded model.
func BuildSummarizer(registry *provider.Registry, resolver DefaultResolver, settings *store.UserSettings) ctxpkg.Summarizer {
	provName := ""
	model := ""
	if settings != nil {
		provName = settings.SummarizerProvider
		model = settings.SummarizerModel
	}
	if (provName == "" || model == "") && resolver != nil {
		if rp, rm, err := resolver.ResolveProviderAndModel(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, provName, model); err == nil {
			provName = rp
			model = rm
		}
	}
	if provName == "" || model == "" {
		slog.Warn("summarizer provider/model not configured; compaction Stage 2 will skip")
		return nil
	}
	if registry == nil {
		return nil
	}
	prov, ok := registry.Get(provName)
	if !ok || prov == nil {
		slog.Warn("summarizer provider not registered; compaction Stage 2 will skip", "provider", provName)
		return nil
	}
	return ctxpkg.NewProviderSummarizer(prov, model)
}

// ClassifyCompactionMode maps an agent profile to a CompactionPipeline mode by
// inspecting the agent's tags. Default is "general" when no recognized tag is
// present. Exported so out-of-package callers (e.g. /compact handler) reuse the
// same classification heuristic as the chat hot path.
func ClassifyCompactionMode(agent *store.AgentProfile) string {
	return classifyModeFromAgentTags(agent)
}

// classifyFn is the package-level indirection for classify.Classify.
// Tests override this to record inputs or force outputs without spinning
// up the full classifier path. Production code path stays direct.
var classifyFn = classify.Classify

// classifyRouteFn is the package-level indirection for classify.ClassifyRoute
// (B2 — CW-20260429-0031). Same role as classifyFn: tests override to
// force routes without exercising the full keyword rubric.
var classifyRouteFn = classify.ClassifyRoute

// classifyAndAttach runs the P3 pre-loop classifier for a generation,
// logs the result, and attaches it to the loop state. Extracted from
// generateResponse so TestClassifyAndAttach_AttachesClassification can
// exercise the wire itself rather than reconstructing it (CW-20260420-0013).
//
// B2 (CW-20260429-0031) extension: also runs ClassifyRoute and stores
// the route hint on the loop state. The route is informative — see
// docs/architecture/classifier-routing.md §1 — and the dispatch seam
// (chat_generate.go) consults it to decide whether to attempt an
// executor handoff before the chat-direct loop runs.
func classifyAndAttach(ls *loopState, sessionID, userContent string, toolNames []string) {
	intent := buildIntentSignals(userContent, toolNames, false /* attachments currently not tracked in pre-loop intent signals */)
	scopeTier, executionPattern := classifyFn(intent)
	routeDecision := classifyRouteFn(intent)
	slog.Info("chat-service: pre-loop classification",
		"session_id", sessionID,
		"scope_tier", scopeTier.String(),
		"execution_pattern", executionPattern.String(),
		"route", routeDecision.Route.String(),
		"target_envelope_type", routeDecision.TargetEnvelopeType,
		"synthetic_allowed", routeDecision.SyntheticAllowed,
		"message_token_est", intent.MessageTokenEst,
		"tools_available", intent.ToolsAvailable,
	)
	ls.SetClassification(scopeTier, executionPattern)
	ls.SetRouteDecision(routeDecision)
}

// buildIntentSignals assembles the pre-loop signal struct consumed by
// classify.Classify. Pulled out as a helper for unit-testability; the
// full generation wires this plus the Classify call at the pre-loop
// boundary of generateResponse (CW-20260420-0013, P3).
func buildIntentSignals(userContent string, tools []string, hasAttachments bool) classify.IntentSignals {
	return classify.IntentSignals{
		Message:         userContent,
		MessageTokenEst: ctxpkg.DefaultEstimator{}.Estimate(userContent), // floor-of-1 for non-empty strings matters: classifyTier branches require est>0
		HasAttachments:  hasAttachments,
		ToolsAvailable:  len(tools),
	}
}

// classifyModeFromAgentTags maps agent tags to a CompactionPipeline mode.
// Default is "general" when no recognized tag is present.
func classifyModeFromAgentTags(agent *store.AgentProfile) string {
	if agent == nil || agent.Tags == "" || agent.Tags == "[]" {
		return ctxpkg.CompactionModeGeneral
	}
	tags := strings.ToLower(agent.Tags)
	switch {
	case strings.Contains(tags, "code") || strings.Contains(tags, "engineer"):
		return ctxpkg.CompactionModeCode
	case strings.Contains(tags, "plan") || strings.Contains(tags, "planner"):
		return ctxpkg.CompactionModePlan
	case strings.Contains(tags, "research"):
		return ctxpkg.CompactionModeResearch
	default:
		return ctxpkg.CompactionModeGeneral
	}
}

// handleRequestTools processes a request_tools meta-tool call within the tool loop.
//
// Phase 5 / D3 (CW-20260419-0011) — reasoning-augmented broker:
//
//   - The first time the per-turn cap trips, the broker emits a REFLECTION
//     prompt instead of a hard halt. It lists what's loaded and asks the
//     LLM to restate the underlying goal in one sentence. The next
//     request_tools call uses that restatement as a fresh query.
//   - If the cap trips a SECOND time within the same turn (i.e. the LLM
//     reflected once already and is still asking), we fall back to the
//     pre-Phase-5 hard halt — we don't reflect repeatedly.
//   - Every call is persisted to the inspector ring buffer with intent +
//     outcome + consecutive_empty + total_calls (TASKS/phase-0/23-export-
//     and-drop-decision-tables.md retired the SQL-backed broker_decisions
//     table this used to also write to — see persistBrokerCallEx).
func (s *chatServiceImpl) handleRequestTools(
	ctx context.Context,
	agentID string,
	tu llmtypes.ToolUseBlock,
	ch chan chat.StreamEvent,
	tools []llmtypes.ToolDefinition,
	loadedTools map[string]bool,
	consecutiveEmpty *int,
	totalCalls *int,
	maxCalls int,
	resultBlocks []llmtypes.ContentBlock,
	toolCallRefs []chat.ToolCallRef,
	sessionID string,
	reflectionFired *bool,
	inspectorTurnID string, // I1 (CW-20260426-0004): "" when inspector is disabled
) ([]llmtypes.ContentBlock, []chat.ToolCallRef, []llmtypes.ToolDefinition) {
	ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID}
	*totalCalls++

	// Pull the LLM-supplied intent up front so it ends up in every
	// inspector broker-decision record (selected, loaded, empty, halted,
	// reflected).
	requestedIntent := ""
	if tu.Input != nil {
		if v, ok := tu.Input["intent"]; ok {
			if s, ok := v.(string); ok {
				requestedIntent = s
			}
		}
	}

	capTripped := *totalCalls > maxCalls || *consecutiveEmpty >= 2

	// First cap-trip → reflection. Only if reflectionFired is non-nil and
	// hasn't fired yet for this turn. This is the Phase 5 D3 entry point.
	if capTripped && reflectionFired != nil && !*reflectionFired {
		*reflectionFired = true
		loadedList := sortedKeys(loadedTools)
		reflection := fmt.Sprintf(
			"Tool discovery soft cap reached (consecutive_empty=%d, total_calls=%d). "+
				"Currently loaded:\n\n  %s\n\n"+
				"Before asking for more tools, REFLECT and answer in one sentence: "+
				"What is the underlying goal the user asked you to accomplish? "+
				"State the goal directly — not the keyword you'd search with. "+
				"Then either (a) call request_tools ONE more time using that goal as the intent — "+
				"the broker will use your restated goal as a fresh query against the available tool catalog — "+
				"OR (b) use a tool above that gets you closer to the goal, OR "+
				"(c) describe to the user what specific capability you need so they can guide you. "+
				"Repeated request_tools calls after this point will be hard-halted.",
			*consecutiveEmpty, *totalCalls,
			strings.Join(loadedList, ", "),
		)
		slog.Info("chat-service: request_tools reflected (D3)",
			"session_id", sessionID, "consecutive_empty", *consecutiveEmpty, "total_calls", *totalCalls)
		s.persistBrokerCallEx(sessionID, inspectorTurnID, requestedIntent, "reflected", *consecutiveEmpty, *totalCalls, 0, reflection, sortedKeys(loadedTools), "")

		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: reflection}
		resultBlocks = append(resultBlocks, llmtypes.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: reflection,
		})
		toolCallRefs = append(toolCallRefs, chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})
		return resultBlocks, toolCallRefs, tools
	}

	// Hard cap. CW-20260419-0012: friendlier halt message that actually
	// helps the LLM decide what to do instead of just telling it "no."
	// Phase 5 D3 retains this as the second-strike fallback after a single
	// reflection round.
	if capTripped {
		reason := fmt.Sprintf("consecutive_empty=%d, total_calls=%d", *consecutiveEmpty, *totalCalls)
		loadedList := sortedKeys(loadedTools)
		rtResult := fmt.Sprintf(
			"Tool discovery cap reached (%s). The tools currently loaded for this turn are:\n\n  %s\n\n"+
				"Before asking for more tools, consider:\n"+
				"1. What is the underlying goal the user asked you to accomplish? State it in one sentence.\n"+
				"2. Can any tool above make partial progress toward that goal? Try it, then re-evaluate.\n"+
				"3. If no loaded tool fits, describe the specific capability you need to the user so they can guide you or load more tools manually.\n\n"+
				"Further request_tools calls this turn will be ignored; use a loaded tool or respond to the user.",
			reason, strings.Join(loadedList, ", "),
		)
		slog.Warn("chat-service: request_tools halted", "reason", reason, "session_id", sessionID)
		s.persistBrokerCallEx(sessionID, inspectorTurnID, requestedIntent, "halted", *consecutiveEmpty, *totalCalls, 0, "", sortedKeys(loadedTools), "")

		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: rtResult}
		resultBlocks = append(resultBlocks, llmtypes.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: rtResult,
		})
		toolCallRefs = append(toolCallRefs, chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})
		return resultBlocks, toolCallRefs, tools
	}

	newTools, rtResult, err := s.tools.HandleRequestTools(ctx, agentID, tu.Input)
	if err != nil {
		message := fmt.Sprintf("Tool discovery failed: %v", err)
		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: message, IsError: true}
		resultBlocks = append(resultBlocks, llmtypes.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: message, IsError: true,
		})
		toolCallRefs = append(toolCallRefs, chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "error"})
		return resultBlocks, toolCallRefs, tools
	}

	var loaded []string
	for _, nt := range newTools {
		if !loadedTools[nt.Name] {
			loadedTools[nt.Name] = true
			tools = append(tools, nt)
			loaded = append(loaded, nt.Name)
		}
	}

	outcome := "loaded"
	if len(loaded) == 0 {
		*consecutiveEmpty++
		outcome = "empty"
		if *consecutiveEmpty == 1 {
			rtResult += "\n\nNo new tools were loaded for this request. If you believe the right tools exist, try rephrasing your intent with different keywords. Otherwise, proceed with the tools you have."
		}
	} else {
		*consecutiveEmpty = 0
	}

	slog.Info("chat-service: request_tools loaded",
		"count", len(loaded), "consecutive_empty", *consecutiveEmpty,
		"tools", loaded, "session_id", sessionID, "outcome", outcome)
	s.persistBrokerCallEx(sessionID, inspectorTurnID, requestedIntent, outcome, *consecutiveEmpty, *totalCalls, len(loaded), "", loaded, "")

	ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: rtResult}
	resultBlocks = append(resultBlocks, llmtypes.ContentBlock{
		Type: "tool_result", ToolUseID: tu.ID, Content: rtResult,
	})
	toolCallRefs = append(toolCallRefs, chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})

	return resultBlocks, toolCallRefs, tools
}

// sortedKeys returns the keys of a map[string]bool in alphabetical order.
// Tiny helper used by the request_tools reflection / halt formatters so the
// loaded-tools listing is deterministic across turns.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// persistBrokerCallEx records one request_tools meta-tool call into the
// inspector ring buffer (when inspector is wired). No-op when the
// inspector is disabled, sessionID is empty, or no turn ID is available —
// dev-mode telemetry only, never gates the loop.
//
// Historical note (TASKS/phase-0/23-export-and-drop-decision-tables.md):
// this used to also persist every call into the `broker_decisions` SQL
// table via toolServiceImpl's BrokerDecisionLogger/LogRequestToolsCall.
// That table (and its writer) was retired in full as part of the same
// task — its historical rows were exported to event_log
// (event_type="broker_decision_export") before the table was dropped. The
// inspector ring buffer is now the only live per-turn broker-decision
// telemetry; the old SQL-backed debug panel (BrokerDecisionsPanel/Widget,
// GET /api/broker/decisions) was removed alongside it — see
// ui/src/components/settings/inspector/InspectorPanel.tsx for its
// replacement.
func (s *chatServiceImpl) persistBrokerCallEx(
	sessionID, inspectorTurnID, intent, outcome string,
	consecutiveEmpty, totalCalls, loadedCount int,
	reflectionQuery string,
	selectedTools []string,
	layerReached string,
) {
	if sessionID == "" || s.inspector == nil || inspectorTurnID == "" {
		return
	}
	if intent == "" {
		intent = "(no intent supplied)"
	}
	d := inspectsvc.BrokerDecision{
		Intent:           intent,
		Outcome:          outcome,
		SelectedTools:    selectedTools,
		LayerReached:     layerReached,
		ConsecutiveEmpty: consecutiveEmpty,
		TotalCalls:       totalCalls,
		LoadedCount:      loadedCount,
		ReflectionQuery:  reflectionQuery,
	}
	s.inspector.RecordBrokerDecision(sessionID, inspectorTurnID, d)
}

// detectStuckLoop checks for repeated identical tool results and returns
// modified result text with warnings or blocks as needed.
func (s *chatServiceImpl) detectStuckLoop(
	toolName, resultText string,
	lastResults map[string]string,
	repeatCount map[string]int,
	blocked map[string]bool,
) string {
	if prev, ok := lastResults[toolName]; ok && prev == resultText {
		repeatCount[toolName]++
		repeats := repeatCount[toolName]
		if repeats >= 2 {
			blocked[toolName] = true
			resultText = fmt.Sprintf("Tool %q returned the same result %d times in a row, so the harness is holding further calls for this turn. "+
				"The result you already have is the tool's answer — re-running it won't produce new data. "+
				"Pivot: try different arguments, a different tool, or summarize what you have and tell the user what's missing.",
				toolName, repeats+1)
			slog.Warn("chat-service: tool BLOCKED after identical results", "tool", toolName, "count", repeats+1)
		} else {
			resultText += fmt.Sprintf(
				"\n\nNote: this tool has returned the same result %d times in a row. "+
					"Re-calling it with the same arguments won't add new data. If you need something different, change the arguments or switch approach.",
				repeats+1)
			slog.Warn("chat-service: tool repeat detected", "tool", toolName, "count", repeats+1)
		}
	} else {
		repeatCount[toolName] = 0
	}
	lastResults[toolName] = resultText
	return resultText
}

// captureEnvelopeData extracts envelope data markers from a tool result.
//
// Used to carry the payload of a __search_kb tool call through
// chat.BuildKBEnvelope into a kb-result card — the support-ticket-specific
// caller was removed in Phase 0 (15c-cut-support-ticket) alongside the rest
// of that plugin's frontend and backend footprint. The generic
// ENVELOPE_DATA marker extraction below is shared infrastructure used by
// card_show and other tools and stays in place.
//
// The delimiter scan itself is not implemented here — it delegates to
// chat.ExtractEnvelopeMarker, the shared marker-extraction function three
// independent hand-scans (this one, internal/api/tools_call.go's
// extractEnvelopeMarker, internal/mcpserver/handlers.go's
// convertEnvelopeMarkers) collapsed onto
// (TASKS/harness-reactive-self-tools/06-collapse-envelope-marker-consumers.md).
// This wrapper's own remaining job — appending to the caller's pending
// slice — is call-site-specific, not duplicated scan logic.
func captureEnvelopeData(result string, pending []string) []string {
	if payload, ok := chat.ExtractEnvelopeMarker(result); ok {
		pending = append(pending, payload)
	}
	return pending
}

// Phase 4c.6 (CW-20260508-0002): setupCLIContext + persistCLISessionID
// deleted. CLI agents now spawn through internal/runtime/agent.Boot via
// driveBootSession (chat_boot_drive.go); sandbox planting moved to the
// per-provider bootdir layouts in internal/runtime/agent/bootdir_*.go,
// process tracking moved into the lib's PTY supervisor (IdleKill /
// RestartOnCrash), --resume threading moved into Boot.OnSessionID +
// store.SetAgentRuntimeProviderSessionID, and the cli_session_id
// session-metadata field is no longer written. retryEnvelopeCorrection
// (line ~2700) still threads provider.WithCLISessionID for the HTTP
// envelope-retry path, but that path is unreachable for CLI sessions
// post-Phase 4c.4 (driveBootSession's chan never surfaces envelope-retry
// triggers).

// maybeCreateAutoArtifact checks if a tool call wrote a file and auto-creates
// an artifact record.
func (s *chatServiceImpl) maybeCreateAutoArtifact(sessionID, messageID, agentID string, tu llmtypes.ToolUseBlock) {
	if s.appConfig == nil {
		return
	}
	artCfg := &s.appConfig.Artifacts
	if !artCfg.IsAutoDetectTool(tu.Name) {
		return
	}
	filePath := artCfg.ExtractFilePath(tu.Input)
	if filePath == "" {
		return
	}

	name := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		name = filePath[idx+1:]
	}

	artifact := &store.Artifact{
		SessionID: sessionID, MessageID: messageID,
		Name: name, MimeType: "application/octet-stream",
		StoragePath: filePath, Origin: store.ArtifactOriginAuto,
		SourceToolCallID: tu.ID, SourceAgentID: agentID,
	}
	if err := s.store.CreateArtifact(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, artifact); err != nil {
		slog.Warn("chat-service: auto-artifact creation failed", "path", filePath, "err", err)
	}
}

// retryEnvelopeCorrection sends a correction prompt for malformed envelopes.
func (s *chatServiceImpl) retryEnvelopeCorrection(
	ctx context.Context,
	sessionID string,
	session *store.Session,
	prov llmcontracts.Provider,
	model string,
	envErrors []chat.EnvelopeError,
	ch chan chat.StreamEvent,
) []chat.Envelope {
	var errDetail chat.EnvelopeError
	for _, ee := range envErrors {
		if ee.Reason == "invalid_json" {
			errDetail = ee
			break
		}
	}

	correction := fmt.Sprintf(
		"Your previous response contained a malformed envelope block that could not be parsed.\n\n"+
			"Raw content:\n```\n%s\n```\n\n"+
			"Error: %s\n\n"+
			"Please re-emit the envelope as a valid JSON object inside a ```nanite-envelope fenced block "+
			"with kind, version (1), and type fields.",
		chat.TruncateStr(errDetail.Raw, 1000), errDetail.Reason,
	)

	slog.Info("chat-service: envelope retry", "session_id", sessionID)
	s.store.LogEvent(ctx, sessionID, "envelope_retry", "info",
		"sending correction prompt", fmt.Sprintf(`{"reason":%q}`, errDetail.Reason))

	retryCtx := ctx
	var meta map[string]any
	if err := json.Unmarshal([]byte(session.Metadata), &meta); err == nil {
		if cliSID, ok := meta["cli_session_id"].(string); ok && cliSID != "" {
			retryCtx = provider.WithCLISessionID(retryCtx, cliSID)
		}
	}
	if sbDir, err := sandbox.Dir(sessionID); err == nil {
		retryCtx = provider.WithSandboxDir(retryCtx, sbDir)
	}

	correctionMsgs := []llmtypes.ChatMessage{{Role: "user", Content: correction}}
	retryCh, err := prov.StreamChat(retryCtx, llmtypes.ChatRequest{
		Messages:   correctionMsgs,
		Model:      model,
		CacheHints: llmcontracts.DefaultCacheStrategy(),
	})
	if err != nil {
		slog.Warn("chat-service: envelope retry stream error", "err", err)
		return nil
	}

	var retryContent strings.Builder
	for evt := range retryCh {
		switch evt.Type {
		case "delta":
			retryContent.WriteString(evt.Content)
			// Envelope corrections are post-loop responses; always final.
			ch <- chat.StreamEvent{Type: "delta", Content: evt.Content, Phase: chat.PhaseFinal}
		case "error":
			slog.Warn("chat-service: envelope retry error", "err", evt.Error)
			return nil
		}
	}

	retryEnvelopes, _, retryErrors := chat.ParseEnvelopes(retryContent.String())
	if len(retryErrors) > 0 {
		slog.Warn("chat-service: envelope retry still had errors — giving up", "count", len(retryErrors))
	}
	if len(retryEnvelopes) > 0 {
		slog.Info("chat-service: envelope retry recovered envelopes", "count", len(retryEnvelopes))
	}
	return retryEnvelopes
}

// autoTitleMaxLen is the hard cap on stored session titles (CW-20260512-0004).
// Sidebar layout tolerates this length without truncation.
const autoTitleMaxLen = 60

// autoTitleSystemPrompt instructs the utility model to emit a short label only.
// The server-side guard in sanitizeAutoTitle is load-bearing — this prompt is
// best-effort because small utility models often ignore length/format hints.
const autoTitleSystemPrompt = "Generate a short conversation label (2-5 words, max 60 characters). " +
	"Respond with ONLY the label — no quotes, no punctuation, no markdown, no greetings, no caveats, no explanation. " +
	"If the user message is too thin to summarize, return a 2-4 word topic guess based on the words present. " +
	"Do not refuse. Do not apologize. Do not write a sentence."

// autoTitle generates a title for a session from the first user message.
func (s *chatServiceImpl) autoTitle(ctx context.Context, sessionID, userContent string) {
	prov, ok := s.providers.Get(s.utilityProvider)
	if !ok {
		return
	}

	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: fmt.Sprintf("First message: %s", userContent)},
	}

	start := time.Now()
	raw, err := prov.Complete(ctx, llmtypes.ChatRequest{
		SystemPrompt: autoTitleSystemPrompt,
		Messages:     msgs,
		Model:        s.utilityModel,
		CacheHints:   llmcontracts.DefaultCacheStrategy(),
	})
	duration := time.Since(start)
	s.recordUtilityMetrics(sessionID, "autoTitle", duration, err)

	if err != nil {
		slog.Warn("chat-service: auto-title failed", "err", err)
		return
	}

	// One retry if the first response is refusal-shaped or empty after
	// sanitization. autoTitle runs on the chat lifecycle so the extra round-trip is off
	// the chat hot path.
	var retryRaw string
	if sanitizeAutoTitle(raw) == "" || looksLikeRefusal(raw) {
		retryStart := time.Now()
		var retryErr error
		retryRaw, retryErr = prov.Complete(ctx, llmtypes.ChatRequest{
			SystemPrompt: autoTitleSystemPrompt,
			Messages:     msgs,
			Model:        s.utilityModel,
			CacheHints:   llmcontracts.DefaultCacheStrategy(),
		})
		s.recordUtilityMetrics(sessionID, "autoTitle.retry", time.Since(retryStart), retryErr)
		if retryErr != nil {
			retryRaw = ""
		}
	}
	title := pickAutoTitle(raw, retryRaw, userContent)
	if title == "" {
		return
	}

	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return
	}
	sess.Title = title
	_ = s.store.UpdateSession(ctx, sess)
}

// sanitizeAutoTitle normalises a raw utility-model response into a stored
// session title (CW-20260512-0004). Strips newlines, collapses whitespace,
// trims wrapping quotes/punctuation, and hard-caps length at autoTitleMaxLen.
// Returns "" when the input is empty after cleanup so the caller can fall
// back. This is the load-bearing guard — the prompt is best-effort.
func sanitizeAutoTitle(raw string) string {
	if raw == "" {
		return ""
	}
	// Replace any newline / tab / CR with a single space, then collapse runs.
	s := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(raw)
	s = strings.Join(strings.Fields(s), " ")
	// Trim wrapping quotes and surrounding punctuation the model often adds.
	s = strings.Trim(s, " \t\"'`")
	s = strings.TrimRight(s, ".,;:!?")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > autoTitleMaxLen {
		s = truncateToRune(s, autoTitleMaxLen)
	}
	return s
}

// pickAutoTitle is the pure decision logic that chooses a session title from
// (up to) two raw utility-model responses plus the original user message
// (CW-20260512-0004). Extracted so the both-refusal fallback path is unit-
// testable without spinning up a provider. Contract:
//
//   - If the retry raw is a usable label (non-empty after sanitize AND not
//     refusal-shaped), it wins.
//   - Else if the first raw is a usable label, it wins.
//   - Else fall back to fallbackTitleFromUser(userContent).
//   - retryRaw == "" means "no retry was attempted or it errored" and is
//     treated as unusable.
//
// This closes the gap where both attempts were refusal-shaped but the first
// sanitized to a non-empty truncated refusal snippet — previously that
// snippet leaked through; now the refusal check gates the fallback.
func pickAutoTitle(raw, retryRaw, userContent string) string {
	if retryRaw != "" && !looksLikeRefusal(retryRaw) {
		if t := sanitizeAutoTitle(retryRaw); t != "" {
			return t
		}
	}
	if !looksLikeRefusal(raw) {
		if t := sanitizeAutoTitle(raw); t != "" {
			return t
		}
	}
	return fallbackTitleFromUser(userContent)
}

// looksLikeRefusal returns true when the utility-model output has the shape of
// an assistant refusal/caveat rather than a label (CW-20260512-0004). Cheap
// substring checks against the raw response — sanitization may strip the
// distinguishing prefix, so this runs on the raw text.
func looksLikeRefusal(raw string) bool {
	trim := strings.TrimSpace(raw)
	if trim == "" {
		return false
	}
	// Long-form output is itself a refusal signal regardless of content —
	// label responses are short.
	if len(trim) > 120 {
		return true
	}
	lower := strings.ToLower(trim)
	// "I" + caveat phrasing is the canonical refusal shape from c160.
	if strings.HasPrefix(lower, "i ") || strings.HasPrefix(lower, "i'") {
		for _, marker := range []string{
			"i cannot", "i can't", "i won't",
			"i don't have access", "i do not have access",
			"i appreciate", "i'm sorry", "i am sorry",
			"i need to", "i must",
		} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

// fallbackTitleFromUser returns a deterministic label derived from the first
// 40 chars of the user message (CW-20260512-0004). Used when both the model
// response and its retry are unusable.
func fallbackTitleFromUser(userContent string) string {
	cleaned := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(userContent)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}
	const fallbackLen = 40
	if len(cleaned) > fallbackLen {
		cleaned = truncateToRune(cleaned, fallbackLen)
	}
	return cleaned
}

// truncateToRune cuts s at <= max bytes without splitting a UTF-8 rune.
func truncateToRune(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Walk back from max until we land on a rune boundary.
	for i := max; i > 0; i-- {
		if (s[i] & 0xC0) != 0x80 { // not a UTF-8 continuation byte
			return strings.TrimRight(s[:i], " ")
		}
	}
	return ""
}

// autoTags generates tags for a session based on recent messages.
func (s *chatServiceImpl) autoTags(ctx context.Context, sessionID string) {
	prov, ok := s.providers.Get(s.utilityProvider)
	if !ok {
		return
	}

	msgs, err := s.store.ListMessages(ctx, sessionID, 10)
	if err != nil || len(msgs) < 2 {
		return
	}

	var sb strings.Builder
	for _, m := range msgs {
		if m.Role == "user" || m.Role == "assistant" {
			content := m.Content
			if len(content) > 300 {
				content = content[:300]
			}
			fmt.Fprintf(&sb, "%s: %s\n", m.Role, content)
		}
	}

	prompt := "Generate 2-5 short tags (1-2 words each, lowercase) that describe this conversation's topics. Return ONLY a JSON array of strings, e.g. [\"go\",\"refactoring\",\"api design\"]. No explanation."
	tagMsgs := []llmtypes.ChatMessage{{Role: "user", Content: sb.String()}}

	start := time.Now()
	raw, err := prov.Complete(ctx, llmtypes.ChatRequest{
		SystemPrompt: prompt,
		Messages:     tagMsgs,
		Model:        s.utilityModel,
		CacheHints:   llmcontracts.DefaultCacheStrategy(),
	})
	duration := time.Since(start)
	s.recordUtilityMetrics(sessionID, "autoTags", duration, err)

	if err != nil {
		return
	}
	raw = strings.TrimSpace(raw)
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil || len(tags) == 0 || len(tags) > 5 {
		return
	}
	tagsJSON, _ := json.Marshal(tags)
	_ = s.store.UpdateSessionTags(ctx, sessionID, string(tagsJSON))
}

// recordUtilityMetrics records execution metrics for utility calls.
func (s *chatServiceImpl) recordUtilityMetrics(sessionID, callType string, duration time.Duration, callErr error) {
	errMsg := ""
	if callErr != nil {
		errMsg = callErr.Error()
	}
	m := &store.ExecutionMetrics{
		SessionID:  sessionID,
		MessageID:  callType,
		Provider:   s.utilityProvider,
		Adapter:    "http",
		Model:      s.utilityModel,
		DurationMs: duration.Milliseconds(),
		IsUtility:  true,
		Error:      errMsg,
	}
	_ = s.store.RecordExecutionMetrics(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, m)
}

// isAgentDebugEnabled checks the agent's settings JSON for a "debug" flag.
func isAgentDebugEnabled(settingsJSON string) bool {
	if settingsJSON == "" {
		return false
	}
	var settings struct {
		Debug bool `json:"debug"`
	}
	_ = json.Unmarshal([]byte(settingsJSON), &settings)
	return settings.Debug
}

// isGlobalDebugMode checks the user_settings developer_mode flag.
func (s *chatServiceImpl) isGlobalDebugMode() bool {
	settings, err := s.store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
	if err != nil {
		return false
	}
	return settings.DeveloperMode
}

// earlyStopSynthesisPrompt is the user-turn prompt injected when the chat loop
// hits its iteration ceiling (max_turns or runaway_fail_cap). The final LLM
// call is made without tools so the model cannot recurse further.
const earlyStopSynthesisPrompt = "You've reached the maximum number of steps. Provide your best answer now based on the work you've done so far."

// earlyStopSynthesis makes one final, no-tools completion call to the provider
// when the chat loop hits max_iter or runaway_fail_cap. The response is
// streamed as delta events into ch and accumulated in fullContent and
// finalContent. finalContent may be nil (pre-F4 call sites).
//
// Errors from the synthesis call are logged and silently swallowed — the
// loop will still break cleanly regardless of whether synthesis succeeds.
// This preserves existing break semantics: the caller always exits the loop
// after invoking this helper.
func (s *chatServiceImpl) earlyStopSynthesis(
	ctx context.Context,
	prov llmcontracts.Provider,
	model string,
	systemPrompt string,
	slotResult *SlotAssemblyResult,
	chatMessages []llmtypes.ChatMessage,
	ch chan<- chat.StreamEvent,
	fullContent *strings.Builder,
	finalContent *strings.Builder,
) {
	// CLI-bypass path (CW-20260514-0045): prov can be nil when classifyNilProvider
	// routed a CLI alias through driveBootSession. Synthesis is best-effort and
	// would NPE on the StreamChat call below — skip cleanly. The terminated
	// envelope still emits in the caller.
	if prov == nil {
		return
	}
	// Truncate to the most recent messages to avoid sending a near-limit history
	// to the synthesis call. Near max_turns the context may already be at the
	// ceiling; a fresh synthesis call with the full slice would fail for the
	// same reason the loop stopped. Keeping the last 20 messages preserves
	// enough context for a coherent summary while staying well within limits.
	const synthHistoryCap = 20
	base := chatMessages
	if len(base) > synthHistoryCap {
		base = base[len(base)-synthHistoryCap:]
	}

	// Append the synthesis prompt as a user message so the LLM has the
	// instruction in-context without modifying the shared chatMessages slice.
	synthMessages := make([]llmtypes.ChatMessage, len(base)+1)
	copy(synthMessages, base)
	synthMessages[len(base)] = llmtypes.ChatMessage{
		Role:    "user",
		Content: earlyStopSynthesisPrompt,
	}

	synthCh, err := prov.StreamChat(ctx, llmtypes.ChatRequest{
		SystemPrompt: systemPrompt,
		SlotBlocks:   slotBlocksFor(slotResult),
		Messages:     synthMessages,
		Model:        model,
		// Tools intentionally omitted — synthesis must not recurse.
		CacheHints: llmcontracts.DefaultCacheStrategy(),
	})
	if err != nil {
		slog.Warn("chat-service: early-stop synthesis call failed", "err", err, "model", model)
		return
	}

	for evt := range synthCh {
		switch evt.Type {
		case "delta":
			fullContent.WriteString(evt.Content)
			if finalContent != nil {
				finalContent.WriteString(evt.Content)
			}
			// Synthesis is the final answer after loop exhaustion; always final.
			ch <- chat.StreamEvent{Type: "delta", Content: evt.Content, Phase: chat.PhaseFinal}
		case "error":
			slog.Warn("chat-service: early-stop synthesis stream error", "err", evt.Error)
		}
		// usage / done / status events are intentionally discarded — the
		// main-loop token accounting has already closed.
	}
}

// normalizeToolInputSchemas ensures every object-type node in each tool's
// InputSchema that enumerates its own "properties" also has
// "additionalProperties": false — closing it against unexpected keys, which
// several providers' strict tool-use validation expects.
//
// CW-20260815-0016: a "type": "object" node with NO "properties" key is a
// free-form/pass-through container by construction (`mux_call.arguments`,
// `card_show.data`, `dispatch_executor.data`, several `workflow_*` tools'
// `args`/`params` — confirmed via a live-catalog audit). Closing such a node
// makes it satisfiable ONLY by the empty object {} — whitelisting nothing
// (no properties key) while admitting nothing (additionalProperties:false).
// This previously collapsed at least `mux_call`'s "arguments" the same way,
// contributing to the ARG_VALIDATION_FAILED failures a live Orchestrator
// session hit on every `mux_call` attempt. normalizeSchemaNode below only
// closes nodes that actually declare "properties"; property-less nodes are
// left exactly as the tool's own schema specifies.
//
// CW-20260429-0017: each tool's InputSchema is deep-cloned BEFORE normalization
// so the in-memory map shared with BuiltinToolRegistry / mcp.SelfToolProviderDefinitions
// stays untouched — a bare in-place mutation would corrupt the canonical
// schema for every subsequent harness arg-validation pass (see
// tool.go GetToolSchema, which reads that canonical map directly, not this
// function's clone).
func normalizeToolInputSchemas(tools []llmtypes.ToolDefinition) {
	for i := range tools {
		clone := cloneSchemaNode(tools[i].InputSchema)
		normalizeSchemaNode(clone)
		tools[i].InputSchema = clone
	}
}

// applyToolSelectionFilter runs the FilterToolSelection plugin filter chain
// over the fully-resolved per-turn tool list, letting a plugin add, remove,
// or reshape which tools are offered to the model this turn. See
// TASKS/phase-4/06-add-filter-tool-selection.md.
//
// Extracted as its own function (mirroring composeExtraSystemPrefix and
// applyChatSurfaceFilter's existing precedent in this package) so the exact
// production call site is independently unit-testable without needing to
// exercise the rest of generateResponse's provider/streaming machinery.
//
// nil-safe: a nil pluginHost (no plugin host wired) or a chain with zero
// registered handlers returns tools unchanged. On a filter error, or when
// the handler chain returns a value that doesn't type-assert back to
// []llmtypes.ToolDefinition, the input tools are returned unchanged and the
// error (if any) is logged — a misbehaving plugin filter must never crash
// the turn or silently empty the tool surface.
func applyToolSelectionFilter(pluginHost PluginEventSink, tools []llmtypes.ToolDefinition, fctx pluginpkg.FilterContext) []llmtypes.ToolDefinition {
	if pluginHost == nil {
		return tools
	}
	filtered, err := pluginHost.ApplyFilter(pluginpkg.FilterToolSelection, tools, fctx)
	if err != nil {
		slog.Warn("chat-service: tool_selection filter error", "err", err)
		return tools
	}
	ft, ok := filtered.([]llmtypes.ToolDefinition)
	if !ok {
		slog.Warn("chat-service: tool_selection filter returned unexpected type — ignoring", "type", fmt.Sprintf("%T", filtered))
		return tools
	}
	return ft
}

// cloneSchemaNode returns a deep copy of a JSON-Schema-shaped map. Maps and
// []any slices are copied; leaf scalars (string, number, bool, nil) and
// non-[]any slices (e.g. []string for `enum`/`required`) are returned as-is
// because normalizeSchemaNode never mutates them. If callers later start
// mutating those slice types, extend this helper accordingly.
func cloneSchemaNode(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}
	out := make(map[string]any, len(node))
	for k, v := range node {
		out[k] = cloneSchemaValue(v)
	}
	return out
}

func cloneSchemaValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneSchemaNode(t)
	case []any:
		dup := make([]any, len(t))
		for i, elem := range t {
			dup[i] = cloneSchemaValue(elem)
		}
		return dup
	default:
		return v
	}
}

func normalizeSchemaNode(node map[string]any) {
	if node == nil {
		return
	}
	if typ, _ := node["type"].(string); typ == "object" {
		// Only close a node that actually enumerates its properties.
		// CW-20260815-0016: a "type": "object" node with NO "properties"
		// key is a free-form/pass-through container by construction (e.g.
		// mux_call's "arguments", card_show's "data" — per-type validation
		// lives in the handler, not the schema). Forcing
		// additionalProperties: false on it whitelists nothing and admits
		// nothing but the empty object {} — the node becomes unsatisfiable
		// with any real content. Leave it alone; whatever the tool's own
		// schema specifies (or doesn't) for additionalProperties stands.
		if _, hasProps := node["properties"]; hasProps {
			if _, ok := node["additionalProperties"]; !ok {
				node["additionalProperties"] = false
			}
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for _, v := range props {
			if child, ok := v.(map[string]any); ok {
				normalizeSchemaNode(child)
			}
		}
	}
	for _, key := range []string{"items", "not"} {
		if child, ok := node[key].(map[string]any); ok {
			normalizeSchemaNode(child)
		}
	}
	for _, key := range []string{"anyOf", "allOf", "oneOf"} {
		if arr, ok := node[key].([]any); ok {
			for _, elem := range arr {
				if child, ok := elem.(map[string]any); ok {
					normalizeSchemaNode(child)
				}
			}
		}
	}
}
