package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/effort"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/internal/truncate"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type generationDirective uint8

const (
	generationProceed generationDirective = iota
	generationRetryIteration
	generationContinueIteration
	generationFinishRun
	generationTerminate
)

type generationLifecycle struct {
	startTime        time.Time
	ptyTurnStarted   bool
	ptyTurnSucceeded bool
	ptyProviderName  string
}

func (s *chatServiceImpl) settleToolTurn(
	ctx context.Context,
	sessionID string,
	assistantMsgID string,
	setup *turnSetup,
	run *runState,
	turn providerTurn,
	ch chan chat.StreamEvent,
) settleToolTurnResult {
	agentID := setup.agentID
	model := setup.model
	run.loop.resultBudget = truncate.BudgetForModel(model)
	selection := setup.selection
	prov := setup.provider
	// --- Build assistant message with tool_use blocks ---
	var assistantBlocks []llmtypes.ContentBlock
	if run.providerOutput != nil {
		assistantBlocks = append(assistantBlocks, *run.providerOutput)
	}
	// F3 (CW-20260420-0023): thinking blocks MUST precede text and tool_use
	// blocks in the assistant message. Anthropic verifies signatures on round-trip;
	// preserve Thinking and Signature verbatim.
	for _, tb := range run.thinkingBlocks {
		assistantBlocks = append(assistantBlocks, llmtypes.ContentBlock{
			Type:      "thinking",
			Text:      tb.Thinking,
			Signature: tb.Signature,
		})
	}
	if text := turn.content; text != "" {
		assistantBlocks = append(assistantBlocks, llmtypes.ContentBlock{Type: "text", Text: text})
	}
	for _, tu := range turn.toolUseBlocks {
		input := tu.Input
		if input == nil {
			input = map[string]any{}
		}
		assistantBlocks = append(assistantBlocks, llmtypes.ContentBlock{
			Type: "tool_use", ID: tu.ID, Name: tu.Name, Input: &input,
		})
	}
	run.chatMessages = append(run.chatMessages, llmtypes.ChatMessage{
		Role: "assistant", ContentBlocks: assistantBlocks,
	})
	// Reset per-iteration thinking accumulator so next iteration starts fresh.
	run.thinkingBlocks = run.thinkingBlocks[:0]
	run.providerOutput = nil

	// --- Execute tools (pre-check → parallel/serial → post-process) ---

	// Handle request_tools meta-tool calls first.
	var resultBlocks []llmtypes.ContentBlock
	var regularTools []llmtypes.ToolUseBlock
	for _, tu := range turn.toolUseBlocks {
		if tu.Name == "request_tools" {
			resultBlocks, run.loop.toolCallRefs, run.tools = s.handleRequestTools(
				ctx, agentID, tu, ch, run.tools, run.loop.loadedTools,
				&run.loop.consecutiveEmptyRequests, &run.loop.totalRequestToolsCalls, run.loop.maxRequestToolsCalls,
				resultBlocks, run.loop.toolCallRefs,
				sessionID, &run.loop.reflectionFired,
				run.loop.inspectorTurnID,
			)
		} else {
			regularTools = append(regularTools, tu)
		}
	}

	// Pre-check regular tools: permission, blocked, concurrency safety.
	plans := s.preCheckTools(ctx, sessionID, agentID, regularTools, run.loop, ch, selection, run.tools)

	// Execute tools: concurrent-safe in parallel, serial one at a time.
	execResults := s.executeToolBatch(ctx, plans, run.loop, agentID, ch, sessionID)

	// Post-process: stuck loop detection, truncation, envelopes, artifacts.
	// model is threaded through so truncate.OutputForModel can size the
	// per-call MaxChars budget from the model's context window — see
	// CW-20260430-0008 (P2 pilot conversion).
	newBlocks, newRefs := s.postProcessToolResults(ctx, plans, execResults, run.loop, ch, sessionID, agentID, assistantMsgID, model)
	resultBlocks = append(resultBlocks, newBlocks...)
	run.loop.toolCallRefs = append(run.loop.toolCallRefs, newRefs...)
	if run.loop.directReturn != "" {
		run.fullContent.Reset()
		run.fullContent.WriteString(run.loop.directReturn)
		// F4: directReturn replaces all accumulated text; treat as final.
		run.finalContent.Reset()
		run.finalContent.WriteString(run.loop.directReturn)
		ch <- chat.StreamEvent{Type: "replace_content", Content: run.loop.directReturn}
		diagLogLoopExit(sessionID, assistantMsgID, run.loop.iteration, "done:direct_return=subagent_literal", len(run.loop.toolCallRefs), ch)
		return settleToolTurnResult{directive: generationFinishRun}
	}

	// Append tool results as user message.
	run.chatMessages = append(run.chatMessages, llmtypes.ChatMessage{
		Role: "user", ContentBlocks: resultBlocks,
	})

	// Update activity timestamp.
	run.loop.touchActivity()

	// Log continuation site: tool results ready, feeding back to provider.
	reason := fmt.Sprintf("%d tools executed", len(turn.toolUseBlocks))
	run.loop.continueWith(ContinueToolResults, reason)

	// Capture turn snapshot for debugging.
	if run.loop.debugMode {
		var snapshotTools []ToolCallSnapshot
		for _, r := range execResults {
			snapshotTools = append(snapshotTools, ToolCallSnapshot{
				Name:       r.ref.Name,
				DurationMs: float64(r.duration.Milliseconds()),
				Success:    !r.isError,
			})
		}
		tokensUsed := 0
		if run.breakdown != nil {
			tokensUsed = run.breakdown.Total
		}
		run.loop.captureSnapshotWithTools(ContinueToolResults, reason, tokensUsed, len(run.chatMessages), snapshotTools)
	}

	// Future continuation sites (wired when features are implemented):
	// - ContinueAgentReturn:  sub-agent or sideloaded task returned results
	// - ContinueHookModified: plugin hook modified state (injected context, changed tools)
	// - ContinueModeChange:   mode switch mid-turn (plan mode, worktree, agent switch)

	// Brief pause between iterations.
	if run.loop.iteration > 0 {
		time.Sleep(1 * time.Second)
	}

	// Check circuit breaker.
	if ap, ok := prov.(*nllmanthropic.Client); ok && ap.CircuitBreaker != nil && ap.CircuitBreaker.IsOpen() {
		slog.Warn("chat-service: circuit breaker open, stopping", "iter", run.loop.iteration)
		if s.events != nil {
			s.events.EmitCircuitBreakerTripped(ctx, sessionID, "anthropic")
		}
		ch <- chat.StreamEvent{
			Type:    "circuit_open",
			Content: "Provider rate limited. Tool-use loop stopped. Would you like to retry?",
		}
		diagLogLoopExit(sessionID, assistantMsgID, run.loop.iteration, "circuit_open", len(run.loop.toolCallRefs), ch)
		return settleToolTurnResult{directive: generationFinishRun}
	}

	return settleToolTurnResult{directive: generationContinueIteration}
}

func (s *chatServiceImpl) initializeRun(
	ctx context.Context,
	sessionID string,
	assistantMsgID string,
	setup *turnSetup,
	lifecycle *generationLifecycle,
	ch chan chat.StreamEvent,
) initializeRunResult {
	agent := setup.agent
	constraints := setup.constraints
	model := setup.model
	providerName := setup.providerName
	tools := setup.tools
	userContent := setup.userContent
	slotResult := setup.slotResult
	chatMessages := setup.chatMessages
	systemPrompt := setup.systemPrompt
	inspectorTurnID := setup.inspectorTurnID
	var startCancel context.CancelFunc
	// --- Stream start ---
	ch <- chat.StreamEvent{Type: "stream_start", MessageID: assistantMsgID, AgentID: agent.ID}

	presenceStart := chat.PresenceEvent{
		Type:      "stream_start",
		SessionID: sessionID,
		AgentID:   agent.ID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	s.streams.SetActivePresence(sessionID, presenceStart)
	s.streams.BroadcastPresence(presenceStart)

	if s.events != nil {
		// Phase 0 item 21 ("Cut Modes, in full") deleted store.AgentMode —
		// there is no more per-agent mode slug to report. "default" matches
		// the literal already used at session.go's own EmitSessionStart call
		// site for the same event.
		s.events.EmitSessionStart(ctx, sessionID, agent.ID, model, "default")
	}

	// CW-20260420-0032: PTY observability — emit pty_turn_start so
	// session_diagnose can reconstruct what happened. The deferred
	// closer emits pty_turn_complete or pty_turn_failed when the function
	// returns. Only emitted for PTY-provider sessions; API-path sessions
	// already have sufficient observability via event_log + execution_metrics.
	if chat.IsPTYProvider(providerName) && s.sessionEventWriter != nil {
		lifecycle.ptyTurnStarted = true
		lifecycle.ptyProviderName = providerName
		startPayload := fmt.Sprintf(`{"message_id":%q,"provider":%q,"agent_id":%q,"model":%q}`,
			assistantMsgID, providerName, agent.ID, model)
		startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		startCancel = cancel
		s.sessionEventWriter.WriteSessionEvent(
			startCtx, sessionID, EventPTYTurnStart, providerName, startPayload)
	}

	// Emit agent.loaded plugin event (fire-and-forget).
	if s.pluginHost != nil {
		s.goTracked("emit.agent-loaded", func(context.Context) {
			s.pluginHost.EmitAgentLoaded(sessionID, agent.ID, agent.Name, fmt.Sprintf("%d", agent.Version))
		})
	}

	// --- Pre-loop budget / compaction gate (pt3 T4) ---
	chatMessages, tools = s.enforceBudgetOrCompact(ctx, sessionID, slotResult, agent, chatMessages, tools, ch)

	// --- Loop state ---
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name
	}
	// Debug mode: per-agent setting or global developer_mode.
	debugMode := isAgentDebugEnabled(agent.Settings) || s.isGlobalDebugMode()

	// P3 (CW-20260420-0013): pre-loop classification. Runs once per
	// generation; downstream consumers read via loopState.Classification().
	// CW-20260519-0073: the dispatch caller selects the loop's
	// inactivity-timeout window. A subagent dispatch uses the
	// Torque-parity liveness window (subagentIdleTimeoutSeconds) — the
	// fixed 300s wall-clock deadline that used to bound subagent runs
	// has been removed, so the chat loop's idle-timeout terminator is
	// now the governing liveness signal.
	ls := newLoopState(constraints, toolNames, debugMode, dispatcher.CallerTypeFromContext(ctx))
	// P3 (CW-20260420-0013): pre-loop classification. Downstream consumers
	// read via loopState.Classification().
	classifyAndAttach(ls, sessionID, userContent, toolNames)

	// I1 (CW-20260426-0004): attach turn ID to loop state so broker/tool
	// producers can record to the same snapshot.
	ls.inspectorTurnID = inspectorTurnID

	// I1 (CW-20260426-0004): record scope tier to inspector (B2 already shipped).
	if s.inspector != nil && inspectorTurnID != "" {
		classifiedTier, _ := ls.Classification()
		s.inspector.RecordScopeTier(sessionID, inspectorTurnID, classifiedTier.String())
	}

	// Phase 4 item 02
	// (TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md):
	// dispatch_to_agent reflex evaluation (upstream seam) — replaces the
	// retired agent-broker call site (attemptBrokerDispatch/
	// buildBrokerInput, formerly chat_broker_dispatch.go, deleted in
	// full). Runs BEFORE the chat-loop entry: on a firing reflex,
	// synthesizes a task_execute call so the resulting envelope reaches
	// the FE as a plugin_envelope SSE event, matching the pattern
	// attemptRouteDispatch uses for the B2 route hint. Nil-safe when the
	// reflex engine isn't wired. See chat_reflex_dispatch.go's header
	// comment for the Rule-1-feed / broker-subsumption design decision.
	//
	// Ordered BEFORE attemptRouteDispatch for the same reason the old
	// broker call was: this is the dispatch DECISION layer; the route
	// hint and the existing reflex/grounding scaffold in callExecuteTask
	// (internal/mcp/self_tools_dispatch.go — a deliberately independent
	// second consumer, untouched by this task) enrich the dispatch CALL.
	s.attemptReflexDispatch(ctx, sessionID, inspectorTurnID, userContent, agent.ID, agent.Class, ls, ch)

	// B2 (CW-20260429-0031): route-dispatch seam. When the classifier
	// emits a non-chat-direct route AND the envelope-render executor is
	// wired, dispatch the executor and emit the resulting envelope (if
	// any) onto the SSE stream as a side-channel plugin_envelope event.
	// The chat-direct LLM loop runs regardless — the route is
	// INFORMATIVE per docs/architecture/classifier-routing.md §1, and
	// short-circuit on success is reserved for B5 / Phase 2 graduation
	// (executor-handoff.md §6).
	s.attemptRouteDispatch(ctx, sessionID, userContent, ls, ch)

	// F1 (CW-20260420-0014): Effort scalar. Extracted from request context;
	// defaults to EffortNormal when the caller did not set it. Biases the
	// per-iteration token budget ceiling and reasoning-block configuration.
	// Orthogonal to ScopeTier — does NOT change roles, tools, or turn counts.
	turnEffort := effort.FromContext(ctx)
	ls.SetEffort(turnEffort)
	reasoningCfg := turnEffort.ReasoningCfg()
	slog.Debug("chat-service: effort scalar",
		"session_id", sessionID,
		"effort", turnEffort.String(),
		"budget_multiplier", turnEffort.BudgetMultiplier(),
		"reasoning_enabled", reasoningCfg.Enabled,
		"reasoning_budget_tokens", reasoningCfg.BudgetTokens,
	)

	// Load per-tool cap from UserSettings.
	if us, err := s.store.GetUserSettings(ctx); err == nil && us.ToolPerTurnCap > 0 {
		ls.limits.defaultPerToolCap = us.ToolPerTurnCap
	}

	// CW-20260418-0043 diagnostic — log effective loop config on entry.
	diagLogLoopStart(sessionID, assistantMsgID, agent.ID, ls, cap(ch))

	// --- Tool-use loop ---
	var fullContent strings.Builder
	// F4 (CW-20260419-0029) — separate narration and final text accumulators.
	// narrationContent captures inter-iteration prose (iterations that end with
	// tool_use). finalContent captures the post-end_turn text (the answer).
	// Only finalContent is stored as message.Content; narrationContent is saved
	// in metadata.thinking for the expand-thinking affordance.
	var narrationContent strings.Builder
	var finalContent strings.Builder
	var finalUsage *chat.Usage
	var breakdown *chat.TokenBreakdown

	// F3 (CW-20260420-0023) — interleaved thinking block accumulator.
	// Collects signed thinking blocks emitted by the Anthropic provider when
	// the interleaved-thinking-2025-05-14 beta is active. Blocks are stored in
	// metadata.thinking_blocks for the post-stream pill and round-tripped as
	// assistant message ContentBlocks on subsequent turns.
	var thinkingBlocks []llmtypes.ThinkingBlock

	return initializeRunResult{
		directive: generationProceed,
		run: &runState{
			loop:             ls,
			chatMessages:     chatMessages,
			tools:            tools,
			systemPrompt:     systemPrompt,
			reasoningConfig:  reasoningCfg,
			fullContent:      fullContent,
			narrationContent: narrationContent,
			finalContent:     finalContent,
			finalUsage:       finalUsage,
			breakdown:        breakdown,
			thinkingBlocks:   thinkingBlocks,
			startCancel:      startCancel,
		},
	}
}

type turnSetup struct {
	session            *store.Session
	agent              *store.AgentProfile
	agentID            string
	constraints        chat.AgentConstraints
	model              string
	providerName       string
	provider           llmcontracts.Provider
	cacheStrategy      []llmcontracts.CacheHint
	selection          *ToolSelection
	tools              []llmtypes.ToolDefinition
	filterContext      pluginpkg.FilterContext
	lazyToolCount      int
	essentialToolCount int
	lazyLoadActive     bool
	extraSystemPrefix  string
	userContent        string
	slotResult         *SlotAssemblyResult
	chatMessages       []llmtypes.ChatMessage
	systemPrompt       string
	inspectorTurnID    string
}

type prepareTurnResult struct {
	directive generationDirective
	setup     *turnSetup
}

type runState struct {
	loop             *loopState
	chatMessages     []llmtypes.ChatMessage
	tools            []llmtypes.ToolDefinition
	systemPrompt     string
	reasoningConfig  effort.ReasoningConfig
	fullContent      strings.Builder
	narrationContent strings.Builder
	finalContent     strings.Builder
	finalUsage       *chat.Usage
	breakdown        *chat.TokenBreakdown
	thinkingBlocks   []llmtypes.ThinkingBlock
	providerOutput   *llmtypes.ContentBlock
	startCancel      context.CancelFunc
}

type initializeRunResult struct {
	directive generationDirective
	run       *runState
}

type providerTurn struct {
	content       string
	toolUseBlocks []llmtypes.ToolUseBlock
}

type providerAttempt struct {
	events     <-chan llmtypes.StreamEvent
	cancel     context.CancelFunc
	span       trace.Span
	cancelOnce sync.Once
	endOnce    sync.Once
}

func (a *providerAttempt) cancelStream() {
	if a == nil {
		return
	}
	a.cancelOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
	})
}

func (a *providerAttempt) close() {
	if a == nil {
		return
	}
	a.cancelStream()
	a.endOnce.Do(func() {
		if a.span != nil {
			a.span.End()
		}
	})
}

type consumeProviderIterationResult struct {
	directive generationDirective
	turn      providerTurn
}

type requestProviderIterationResult struct {
	directive generationDirective
	attempt   *providerAttempt
}

type settleToolTurnResult struct {
	directive generationDirective
}

func (s *chatServiceImpl) requestProviderIteration(
	ctx context.Context,
	sessionID string,
	assistantMsgID string,
	setup *turnSetup,
	run *runState,
	ch chan chat.StreamEvent,
) requestProviderIterationResult {
	session := setup.session
	agent := setup.agent
	agentID := setup.agentID
	model := setup.model
	providerName := setup.providerName
	prov := setup.provider
	cacheStrategy := setup.cacheStrategy
	fctx := setup.filterContext
	lazyToolCount := setup.lazyToolCount
	essentialToolCount := setup.essentialToolCount
	lazyLoadActive := setup.lazyLoadActive
	extraSystemPrefix := setup.extraSystemPrefix
	userContent := setup.userContent
	slotResult := setup.slotResult
	var err error

	// Check layered iteration limits.
	if stop, code, reason := run.loop.shouldStop(); stop {
		slog.Warn("chat-loop stopped", "reason", reason, "code", code, "session_id", sessionID, "agent", agent.ID, "iter", run.loop.iteration)

		// Early-stopping-generate: when the loop hits the runaway hard
		// circuit-breaker, make one final no-tools LLM call to
		// synthesize a best-effort answer from the work done so far.
		// Synthesis deltas land in the stream before the terminated
		// envelope so the user sees a coherent response rather than an
		// abrupt cutoff. CW-20260504-0001: max_turns is no longer a
		// terminator; Phase 0 item 12 (2026-08-18) subsequently removed
		// the soft max_turns warning signal entirely (it was
		// telemetry-only and never gated the loop either), so the only
		// synthesis trigger left is the runaway hard circuit-breaker
		// below.
		if code == TerminationRunawayToolFailures {
			s.earlyStopSynthesis(ctx, prov, model, extraSystemPrefix, slotResult, run.chatMessages, ch, &run.fullContent, &run.finalContent)
		}

		// CW-20260417-0485: emit a typed `chat-loop-terminated` envelope
		// BEFORE the fallback status event so the FE (CW-20260418-0008)
		// can render a terminal pause card. Any FE that doesn't yet
		// understand the new envelope type will still see the status
		// event, preserving existing behavior.
		s.emitChatLoopTerminated(sessionID, run.loop, code, reason, ch)
		ch <- chat.StreamEvent{Type: "status", Content: fmt.Sprintf("Stopped: %s", reason)}
		diagLogLoopExit(sessionID, assistantMsgID, run.loop.iteration, "shouldStop:"+string(code), len(run.loop.toolCallRefs), ch)
		return requestProviderIterationResult{directive: generationFinishRun}
	}
	// Context cancellation.
	//
	// CW-20260512-0006 removed the global 5-minute wall-clock deadline.
	// CW-20260512-0123 (SP-20260512-0011 W3) removed the per-agent
	// `MaxTimeSeconds` opt-in wall-clock that used to be the sole
	// remaining producer of context.DeadlineExceeded here — that
	// field was the origin of the c160 "Agent execution time limit
	// exceeded (0 seconds)" error class and has been deleted.
	//
	// The remaining sources of a non-nil ctx.Err() are all
	// intentional cancellations (NOT errors):
	//   - Takeover — a new HandleMessage call for the same session
	//     cancels the prior generation via registerGeneration.
	//   - Lifecycle shutdown — process-wide drain bridges bgCtx to
	//     cancel.
	//   - User-initiated stop — POST /chat/cancel resolves the
	//     registered cancel via CancelActiveGeneration.
	// All three surface as context.Canceled; emit a status event
	// (NOT an error envelope) and persist any partial content via
	// the cancellation-specific helper that writes HasError=false.
	//
	// Hung subagents continue to be bounded by the subagent reaper
	// (internal/subagent/reaper.go); runaway chat loops are bounded
	// by the in-loop runaway-fail-cap + idle-timeout + hard-ceiling
	// (resolveIterationLimits in chat_loop_state.go).
	if ctxErr := ctx.Err(); ctxErr != nil {
		diagLogLoopExit(sessionID, assistantMsgID, run.loop.iteration, "ctx_canceled:"+ctxErr.Error(), len(run.loop.toolCallRefs), ch)
		slog.Info("generateResponse canceled", "err", ctxErr, "session_id", sessionID)
		ch <- chat.StreamEvent{Type: "status", Content: "Stopped: canceled"}
		s.persistPartialAssistantCanceled(sessionID, assistantMsgID, agentID, run.fullContent.String())
		return requestProviderIterationResult{directive: generationTerminate}
	}

	// Token budget enforcement.
	// Thread the per-model context window into EnforceTokenBudget so that
	// models with larger windows (e.g. Gemini 1M) are not over-pruned by the
	// hardcoded 200K default. contextWindowSize returns 0 on miss, which
	// causes EnforceTokenBudget to fall back to DefaultContextWindow * HardCeilingPct.
	// (CW-20260426-0031)
	//
	// F1 (CW-20260420-0014): apply the Effort scalar multiplier to the
	// budget ceiling BEFORE passing it to EnforceTokenBudget. This is the
	// budget seam — the multiplier scales the existing ceiling rather than
	// replacing it, so provider limits and model window sizes remain the
	// authoritative upper bound.
	preBudgetMsgCount := len(run.chatMessages)
	preBudgetToolCount := len(run.tools)
	var budgetErr error
	var budgetCeiling int
	if ws := s.contextWindowSize(providerName, model); ws > 0 {
		budgetCeiling = int(float64(ws) * chat.HardCeilingPct)
	}
	budgetCeiling = effort.ApplyToCeiling(budgetCeiling, run.loop.Effort())
	run.chatMessages, run.tools, run.breakdown, budgetErr = chat.EnforceTokenBudget(run.systemPrompt, run.chatMessages, run.tools, budgetCeiling)
	if budgetErr != nil {
		slog.Warn("chat-service: token budget enforcement refused", "err", budgetErr)
		if s.events != nil {
			s.events.EmitContextBudgetExceeded(ctx, sessionID, run.breakdown.Total, run.breakdown.Ceiling)
		}
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Context too large after all reductions",
			map[string]interface{}{
				"total":   run.breakdown.Total,
				"ceiling": run.breakdown.Ceiling,
				"system":  run.breakdown.System,
				"msgs":    run.breakdown.Messages,
				"tools":   run.breakdown.Tools,
			})
		s.persistPartialAssistantAndNotifyBroker(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, budgetErr) // CW-20260419-0019, CW-20260512-0001
		return requestProviderIterationResult{directive: generationTerminate}
	}
	// Log compaction continuation if budget enforcement reduced context.
	if len(run.chatMessages) < preBudgetMsgCount || len(run.tools) < preBudgetToolCount {
		run.loop.continueWith(ContinueCompaction, fmt.Sprintf("budget reduced: msgs %d→%d, tools %d→%d",
			preBudgetMsgCount, len(run.chatMessages), preBudgetToolCount, len(run.tools)))
	}

	// --- Provider call ---
	provCtx, provSpan := feotel.StartSpan(ctx, "nanite.provider.call")
	// CW-20260517-0036: a cancelable child of provCtx so the
	// provider-stream inactivity watchdog (streamLoop below) can tear
	// down a silently stalled HTTP stream from the inside. Canceling
	// it propagates context.Canceled into the provider adapter, which
	// closes provCh — unblocking the consume loop. providerAttempt owns
	// both cancellation and span completion with independent sync.Once
	// guards: request failures close here, while a successful request is
	// handed to consumeProviderIteration for closure. No per-iteration
	// cleanup defer accumulates on the coordinator.
	provCtx, provStreamCancel := context.WithCancel(provCtx)
	attempt := &providerAttempt{cancel: provStreamCancel, span: provSpan}
	provSpan.SetAttributes(
		attribute.String("nanite.model", model),
		attribute.Int("nanite.iteration", run.loop.iteration),
		attribute.Int("nanite.tools.count", len(run.tools)),
		attribute.Int("nanite.messages.count", len(run.chatMessages)),
		attribute.Int("nanite.tokens.total", run.breakdown.Total),
		attribute.Int("nanite.tokens.ceiling", run.breakdown.Ceiling),
	)

	// F3 (CW-20260420-0023): inject reasoning config into provider context so
	// the Anthropic adapter can gate the interleaved-thinking beta header.
	if run.reasoningConfig.Enabled {
		provCtx = llmcontracts.WithReasoningConfig(provCtx, llmcontracts.ReasoningConfig{
			Enabled:      run.reasoningConfig.Enabled,
			BudgetTokens: run.reasoningConfig.BudgetTokens,
			BetasHeader:  run.reasoningConfig.BetasHeader,
		})
	}

	// Phase 4c.6 (CW-20260508-0002): setupCLIContext deleted. CLI
	// agents now spawn through internal/runtime/agent.Boot via the
	// driveBootSession branch below; sandbox planting, --resume
	// session-id threading, and process tracking moved into the agent
	// runtime composition root. The HTTP-API providers below get the
	// existing per-turn provCtx unchanged.

	// --- Pre-hook: message.sending ---
	// Plugins observing "message.sending" may cancel the LLM call.
	// Data shape: {session_id, agent_id, model, messages, system_prompt_length, iteration}.
	if s.pluginHost != nil {
		canceled := s.pluginHost.EmitPreHook("message.sending", sessionID, map[string]any{
			"agent_id":             agent.ID,
			"model":                model,
			"messages":             len(run.chatMessages),
			"system_prompt_length": len(run.systemPrompt),
			"iteration":            run.loop.iteration,
		})
		if canceled {
			attempt.close()
			slog.Info("chat-service: message.sending canceled by plugin hook", "session_id", sessionID, "iter", run.loop.iteration)
			blockMsg := "Message blocked by plugin policy."
			ch <- chat.StreamEvent{Type: "delta", Content: blockMsg, Phase: chat.PhaseFinal}
			run.fullContent.WriteString(blockMsg)
			run.finalContent.WriteString(blockMsg)
			diagLogLoopExit(sessionID, assistantMsgID, run.loop.iteration, "plugin_cancel:message.sending", len(run.loop.toolCallRefs), ch)
			return requestProviderIterationResult{directive: generationFinishRun}
		}
	}

	// Filter: context_window — plugin prunes or injects context blocks.
	if s.pluginHost != nil {
		if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterContextWindow, run.chatMessages, fctx); err != nil {
			slog.Warn("chat-service: context_window filter error", "err", err)
		} else if fm, ok := filtered.([]llmtypes.ChatMessage); ok {
			run.chatMessages = fm
		}
	}

	var provCh <-chan llmtypes.StreamEvent
	if len(run.tools) > 0 {
		slog.Debug("chat-service: tool-use iteration",
			"iter", run.loop.iteration, "tools", len(run.tools), "messages", len(run.chatMessages),
			"tokens", run.breakdown.Total, "ceiling", run.breakdown.Ceiling)
	}

	// request_build telemetry — Glass-2 (CW-20260502-0010, SP-20260502-0001).
	// Emits per-slot + total token counts at request build time. Feeds Glass-7
	// (slot trim) which uses this data to identify the biggest contributor to
	// the cacheable prefix. Walks ctxpkg.SlotOrder so new slots (e.g. Glass-3
	// SlotHandoff) are picked up automatically — do not hardcode a slot list
	// here. cacheable_prefix_tokens comes from the optional llmcontracts.Cacheable
	// interface (go-providers): the provider builds the same payload it would
	// send and reports the offset of the last cache_control marker / 4. Same
	// heuristic as the rate-budget pre-flight; 0 when the provider doesn't
	// implement Cacheable or has no cache hints set.
	slotTokens := make(map[string]int, len(ctxpkg.SlotOrder))
	totalEstimate := 0
	if slotResult != nil && slotResult.Window != nil {
		for _, name := range ctxpkg.SlotOrder {
			if sl := slotResult.Window.Slot(name); sl != nil {
				slotTokens[name] = sl.TokenCount
				totalEstimate += sl.TokenCount
			}
		}
	}
	rateLimitTPM := 0
	if rl, ok := prov.(llmcontracts.RateLimited); ok {
		rateLimitTPM = rl.RateLimitTPM()
	}
	cacheablePrefixTokens := 0
	// Per-call cache hints close the shared-singleton race that used to
	// live on this code path: cache hints now travel on each ChatRequest
	// instead of being set on the provider via SetCacheHints, so
	// concurrent sessions on the same Client no longer overwrite each
	// other's hints between the assignment and the read. FU-13 /
	// CW-20260520-0054 — see the cache-miss-echo signature
	// (cache_read=0 AND input_tokens<10) that motivated the migration.
	if cp, ok := prov.(llmcontracts.Cacheable); ok {
		cacheablePrefixTokens = cp.EstimateCacheablePrefix(provCtx, llmtypes.ChatRequest{
			SystemPrompt: extraSystemPrefix,
			SlotBlocks:   slotBlocksFor(slotResult),
			Messages:     run.chatMessages,
			Model:        model,
			Tools:        run.tools,
			CacheHints:   cacheStrategy,
		})
	}
	// CW-20260512-0121 (SP-20260512-0011): stamp dispatcher CallerType
	// on the request_build slog so the cross-call-site smoke can prove
	// every dispatch type (chat | subagent | background) produces an
	// identical structural slot shape. The CallerType arrives via the
	// dispatcher-stamped ctx value (see internal/dispatcher). All
	// production call-sites (launchGeneration, ChatRunner.invokeChat,
	// DelegateTask) now route through Dispatcher.Run, so a populated
	// CallerType is the expected case. The "unknown" fallback covers
	// only the test-stub seam (`chatInvoker` overrides in
	// subagent_runner_test.go, subagent_runner_lineage_test.go,
	// subagent_runner_derivation_test.go) which call generateResponse
	// without going through the dispatcher by design. Production runs
	// must NEVER emit caller=unknown — if they do, a call-site has
	// escaped the dispatcher door and the consolidation is leaking.
	caller := dispatcher.CallerTypeFromContext(ctx)
	callerLabel := caller.String()
	if callerLabel == "" {
		callerLabel = "unknown"
	}
	rbArgs := []any{
		"session_id", sessionID,
		"agent_id", agentID,
		"model", model,
		"provider", providerName,
		"caller", callerLabel,
		"total_estimated_request_tokens", totalEstimate,
		"rate_limit_tpm_observed", rateLimitTPM,
		"cacheable_prefix_tokens", cacheablePrefixTokens,
		// G-HOT-SWAP-DEAD activation telemetry. tools_lazy_load_active=false
		// indicates the partition either was disabled or produced an empty
		// lazy set (no overflow — every tool fit inline). When active, the
		// slot's tools_tokens already reflects the inline+hint-only payload,
		// so the savings vs. legacy can be derived as
		// `legacy_tools_tokens - tools_tokens` once a control sample is
		// captured (off-flag run on the same agent + workspace).
		"tools_essential_count", essentialToolCount,
		"tools_lazy_count", lazyToolCount,
		"tools_lazy_load_active", lazyLoadActive,
	}
	for _, name := range ctxpkg.SlotOrder {
		rbArgs = append(rbArgs, name+"_tokens", slotTokens[name])
	}
	slog.Info("request_build", rbArgs...)

	// Phase 4c.4: CLI providers route through the long-lived agent.Boot
	// path. The chat-harness slot pipeline still ran above (telemetry,
	// classification, mode-suggestion), but the slot blocks themselves
	// are delivered via the boot dir's CLAUDE.md / agent-context.md
	// (planted at Boot, regenerated on slot change). Per-turn delivery
	// is the user message + UserContext slot via SendInput.
	//
	// HTTP API providers (anthropic / openai / gemini-api / mistral /
	// openrouter / openzen / azure-openai / ollama) keep going through
	// provider.StreamChat per turn with the slot pipeline running as
	// today.
	//
	// Phase 2 item 01 (TASKS/phase-2/01-wire-runtime-kind-routing.md):
	// this branches on `prov == nil` rather than re-checking
	// chat.IsCLIProvider(providerName) — prov/providerName are fixed
	// for the whole call (resolveProvider ran once, above, outside
	// this loop) and classifyNilProvider already made the CLI-vs-API
	// routing decision (primarily from agent.RuntimeKind) the one time
	// it needed to be made. Re-deriving it here from the string a
	// second time is exactly the "matched in N places, expected to
	// stay in sync by convention" pattern architecture/
	// 02-agent-launching.md's runtime_kind field replaces.
	if prov == nil {
		provCh, err = s.driveBootSession(provCtx, sessionID, session, agent, slotResult, userContent, run.loop.iteration, providerName)
	} else {
		provCh, err = prov.StreamChat(provCtx, llmtypes.ChatRequest{
			SystemPrompt: extraSystemPrefix,
			SlotBlocks:   slotBlocksFor(slotResult),
			Messages:     run.chatMessages,
			Model:        model,
			Tools:        run.tools,
			CacheHints:   cacheStrategy,
		})
	}
	if err != nil {
		// CW-20260517-0036: StreamChat returned an error before any
		// stream was established — release the per-iteration stream
		// context. Idempotent; harmless if some branches below recover
		// and `continue` (the next iteration allocates a fresh one).
		attempt.cancelStream()
		// T9 — provider-error recovery: detect a compaction-recoverable
		// failure (context-window overflow OR rate-budget overflow), run
		// the compaction pipeline synchronously, and retry once. Second
		// failure surfaces as a user-facing error.
		//
		// CW-20260418-0099: IsCompactRecoverable covers both the original
		// context-overflow case and the new go-providers ≥ v0.2.1
		// ErrRequestExceedsRateBudget sentinel — when the estimated
		// request is bigger than the per-minute rate budget, waiting is
		// futile and compaction is the right response.
		if ctxpkg.IsCompactRecoverable(err) && run.loop.compactRecoverableAttempts < maxCompactRecoverableAttempts {
			// attempt.close() waits until the recovery outcome is
			// known so the span is ended exactly once. On success we
			// end it cleanly before retrying; on refused recovery the
			// branch below records the error, then ends the span.
			triggerKind := compactTriggerContextOverflow
			recoveryReason := "context_overflow at stream start"
			if errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget) {
				triggerKind = compactTriggerRateBudget
				recoveryReason = "rate_budget_exceeded at stream start"
			}
			run.loop.continueWith(ContinueRecovery, recoveryReason)
			newMsgs, newTools, ok := s.recoverFromContextOverflow(ctx, sessionID, slotResult, agent, run.chatMessages, run.tools, ch, err.Error(), triggerKind)
			if ok {
				// Recovery produced stages — end the span cleanly (this
				// wasn't a provider failure from the user's perspective),
				// increment the attempt counter, and continue the loop
				// with the compacted request. The "failed after retry"
				// branch below catches us if we exhaust attempts later.
				// CW-20260419-0018: incrementing (not latching) lets a
				// second compaction run when tool results later blow the
				// budget again, while the maxCompactRecoverableAttempts
				// cap still prevents infinite loops.
				attempt.close()
				run.loop.compactRecoverableAttempts++
				// CW-20260419-0012: compaction's strip_tool_blocks stage
				// drops tool-definition context from the conversation,
				// so the LLM has to re-request tools post-compaction.
				// Reset the discovery-call counter so pre-compaction
				// calls don't eat the post-compaction budget (observed
				// in nanite-chat-debug-3.md: total_calls jumped 4→5
				// immediately after a successful compaction).
				run.loop.totalRequestToolsCalls = 0
				run.loop.consecutiveEmptyRequests = 0
				run.chatMessages = newMsgs
				run.tools = newTools
				run.systemPrompt = slotResult.SystemPrompt
				return requestProviderIterationResult{directive: generationRetryIteration}
			}
			// Recovery refused (flag off / no summarizer / no stages).
			// Exhaust attempts so the same request can't loop back
			// into this branch, and preserve the normal provider-error
			// telemetry so refused recovery is as diagnosable as any
			// other provider failure (PR #68 review #2).
			run.loop.compactRecoverableAttempts = maxCompactRecoverableAttempts
			attempt.span.RecordError(err)
			attempt.span.SetStatus(codes.Error, err.Error())
			attempt.close()
			slog.Warn("chat-service: compact-recoverable recovery refused — surfacing unrecovered error",
				"session_id", sessionID, "iter", run.loop.iteration, "trigger_kind", triggerKind, "err", err)
			// Outcome bookkeeping must survive cancellation of the provider request it records.
			s.store.LogEvent(context.WithoutCancel(ctx), sessionID, "provider_error", "error",
				fmt.Sprintf("iteration %d: %v (recovery refused, trigger=%s)", run.loop.iteration, err, triggerKind),
				fmt.Sprintf(`{"model":%q,"tools":%d,"messages":%d,"trigger_kind":%q}`, model, len(run.tools), len(run.chatMessages), triggerKind))
			if s.events != nil {
				s.events.EmitError(ctx, sessionID, "provider_error", err.Error())
				if chat.ClassifyError(err) == chat.ErrorCodeRateLimit {
					s.events.EmitRateLimitHit(ctx, sessionID, "anthropic", 0)
				}
			}
			if s.pluginHost != nil {
				errMsg := err.Error()
				s.goTracked("emit.provider-error", func(context.Context) {
					s.pluginHost.EmitProviderError(sessionID, providerName, model, errMsg)
				})
			}
			// Glass-6 (CW-20260502-0013, SP-20260502-0001): for the
			// rate-budget refused-recovery branch, do NOT emit a fatal
			// internal_error envelope. Surface the failure as a
			// rate_budget_pause stream event, optionally auto-retry once
			// after RateTracker.WaitTime, and end the turn cleanly when
			// the budget can't recover. The session stays alive so the
			// next user message reuses the same session_id — replaces
			// the prior "/clear to recover" UX.
			if triggerKind == compactTriggerRateBudget {
				if s.pauseAndMaybeRetryRateBudget(ctx, sessionID, prov, ch, err, triggerKind, &run.loop.rateBudgetPauseAttempts) {
					// Auto-retry: rerun StreamChat with the same args.
					// Resetting compactRecoverableAttempts isn't right
					// here — recovery already refused — but we DO need
					// to step the iteration counter so the retry isn't
					// counted as a fresh turn. Mirrors the recovery-ok
					// path above.
					return requestProviderIterationResult{directive: generationRetryIteration}
				}
				// Pause emitted with user_action_needed; end the turn
				// cleanly without a fatal envelope.
				s.persistPartialAssistantAndNotifyBroker(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, err) // CW-20260419-0019, CW-20260512-0001
				return requestProviderIterationResult{directive: generationTerminate}
			}
			var msg string
			switch triggerKind {
			case compactTriggerRateBudget:
				msg = "Request exceeds the per-minute rate budget and automatic compaction could not reduce it. Reduce or split the request, or use `/clear` to remove context."
			default:
				msg = "Context is too large and automatic compaction could not reduce it. Use `/clear` or split the request."
			}
			details := map[string]interface{}{"recovery": "refused", "trigger_kind": triggerKind}
			if s.surfaceErrorOrSuppress(ch, sessionID, "recovery_refused", msg, details, run.fullContent.String()) {
				return requestProviderIterationResult{directive: generationTerminate}
			}
			// surfaceErrorOrSuppress already classified above; use the pre-classified
			// broker-notify variant. CW-20260512-0001, CW-20260512-0002.
			s.persistPartialAssistantAndNotifyBrokerPreClassified(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, err) // CW-20260419-0019, CW-20260512-0001, CW-20260512-0002
			return requestProviderIterationResult{directive: generationTerminate}
		}
		attempt.span.RecordError(err)
		attempt.span.SetStatus(codes.Error, err.Error())
		attempt.close()
		slog.Error("chat-service: provider stream error", "iter", run.loop.iteration, "err", err)
		// Outcome bookkeeping must survive cancellation of the provider request it records.
		s.store.LogEvent(context.WithoutCancel(ctx), sessionID, "provider_error", "error",
			fmt.Sprintf("iteration %d: %v", run.loop.iteration, err),
			fmt.Sprintf(`{"model":%q,"tools":%d,"messages":%d}`, model, len(run.tools), len(run.chatMessages)))
		if s.events != nil {
			errCode := chat.ClassifyError(err)
			s.events.EmitError(ctx, sessionID, "provider_error", err.Error())
			if errCode == chat.ErrorCodeRateLimit {
				s.events.EmitRateLimitHit(ctx, sessionID, "anthropic", 0)
			}
		}
		// Emit provider.error plugin event.
		if s.pluginHost != nil {
			errMsg := err.Error()
			s.goTracked("emit.provider-error", func(context.Context) {
				s.pluginHost.EmitProviderError(sessionID, providerName, model, errMsg)
			})
		}
		// CW-20260512-0123 (SP-20260512-0011 W3): the `loopState.retryBudget`
		// counter was removed alongside the deleted `RetryBudget`
		// agent-constraints field; the runaway-fail-cap is the
		// surviving tool-failure terminator.
		if ctxpkg.IsCompactRecoverable(err) && run.loop.compactRecoverableAttempts >= maxCompactRecoverableAttempts {
			// Already retried once — emit a user-facing message tailored
			// to which recoverable mode tripped. CW-20260418-0099.
			// PR #67 review #1: waiting does not help ErrRequestExceedsRateBudget
			// because the request itself is bigger than a single window.
			slog.Warn("chat-service: compaction-recoverable error persisted after retry",
				"session_id", sessionID, "iter", run.loop.iteration, "err", err)
			// Glass-6 (CW-20260502-0013, SP-20260502-0001): rate-budget
			// failures past compaction also stop killing the session. Emit
			// rate_budget_pause and end the turn cleanly. context-overflow
			// retains the prior user-facing internal_error envelope —
			// Glass-6's scope is rate-budget only.
			if errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget) {
				if s.pauseAndMaybeRetryRateBudget(ctx, sessionID, prov, ch, err, compactTriggerRateBudget, &run.loop.rateBudgetPauseAttempts) {
					return requestProviderIterationResult{directive: generationRetryIteration}
				}
				s.persistPartialAssistantAndNotifyBroker(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, err) // CW-20260419-0019, CW-20260512-0001
				return requestProviderIterationResult{directive: generationTerminate}
			}
			msg := "Context is still too large after compaction. Use `/clear` or split the request."
			if s.surfaceErrorOrSuppress(ch, sessionID, "compact_failed_after_retry", msg, map[string]interface{}{"recovery": "failed_after_retry"}, run.fullContent.String()) {
				return requestProviderIterationResult{directive: generationTerminate}
			}
			// surfaceErrorOrSuppress already classified above; use the pre-classified
			// broker-notify variant. CW-20260512-0001, CW-20260512-0002.
			s.persistPartialAssistantAndNotifyBrokerPreClassified(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, err) // CW-20260419-0019, CW-20260512-0001, CW-20260512-0002
			return requestProviderIterationResult{directive: generationTerminate}
		}
		// Provider stream error (general) — classifier-driven error code,
		// not necessarily ErrorCodeInternal. Subagent suppression still
		// applies: if a subagent is actively running and the parent's
		// provider call errors, surfacing it as a parent-side failure
		// confuses the user. The classifier-driven code is preserved
		// for the non-subagent branch.
		if s.suppressSurfaceIfSubagentCaused(sessionID, "provider_stream_error", run.fullContent.String()) {
			return requestProviderIterationResult{directive: generationTerminate}
		}
		s.surfaceProviderFailure(ctx, ch, sessionID, assistantMsgID, agentID, run.fullContent.String(), model, providerName, agent.Slug, err, map[string]interface{}{"tools": len(run.tools)})
		return requestProviderIterationResult{directive: generationTerminate}
	}

	attempt.events = provCh
	return requestProviderIterationResult{directive: generationProceed, attempt: attempt}
}

func (s *chatServiceImpl) consumeProviderIteration(
	ctx context.Context,
	sessionID string,
	assistantMsgID string,
	setup *turnSetup,
	run *runState,
	attempt *providerAttempt,
	ch chan chat.StreamEvent,
) consumeProviderIterationResult {
	agent := setup.agent
	agentID := agent.ID
	model := setup.model
	providerName := setup.providerName
	slotResult := setup.slotResult
	defer attempt.close()

	// --- Consume provider stream ---
	var turnContent strings.Builder
	var toolUseBlocks []llmtypes.ToolUseBlock
	var stopReason string
	var lastPTYToolPending string
	// T9 — set by the mid-stream overflow handler when the outer loop
	// should retry with a compacted request instead of terminating.
	contextOverflowRecovered := false

	// F4 (CW-20260419-0029) — per-iteration delta buffer. Deltas are
	// buffered during streaming and flushed with the correct phase once
	// stopReason is known after the stream closes. Narration iterations
	// (stopReason=tool_use) flush as PhaseNarration; the final iteration
	// (stopReason=end_turn) flushes as PhaseFinal. The buffer is small —
	// typically a handful of short prose fragments per iteration.
	var iterDeltaBuf []string

	// CW-20260418-0043 diagnostic — track provider stream duration + event count.
	provStreamStart := time.Now()
	diagProvEventCount := 0

	// CW-20260517-0036 — provider-stream inactivity watchdog.
	//
	// `for evt := range attempt.events` blocks indefinitely when the provider
	// holds the stream open but emits nothing (a silent stall — c242).
	// CW-20260519-0073's idle terminator (shouldStop Layer 2) only runs
	// at the *top* of the next chat-loop iteration, which a stalled
	// streamLoop never reaches; within a single stall the run was
	// bounded only by the 1800s wall-clock backstop. This select-based
	// loop bounds the *gap between provider events*: every event resets
	// streamIdle; if no event arrives for the inactivity window the
	// watchdog cancels provStreamCtx (closing attempt.events) and the run
	// surfaces a structured `stalled` error.
	//
	// This CANNOT kill a legitimate long-running tool call: a tool call
	// executes *between* iterations — after streamLoop closes for this
	// iteration, inside executeToolBatch — so it never blocks this
	// loop. The watchdog only measures silence on the provider channel
	// itself, which is exactly the "provider produced nothing" signal.
	// The window is run.loop.limits.idleTimeout (formerly
	// ls.limits.idleTimeout), already caller-scoped by
	// CW-20260519-0073 (300s for a subagent dispatch, 900s interactive),
	// so we reuse the chat loop's existing liveness budget rather than
	// inventing a parallel one.
	streamInactivityWindow := run.loop.limits.idleTimeout
	streamIdle := time.NewTimer(streamInactivityWindow)
	streamStalled := false

streamLoop:
	for {
		var evt llmtypes.StreamEvent
		var ok bool
		select {
		case evt, ok = <-attempt.events:
			if !ok {
				// Channel closed — normal end of stream.
				break streamLoop
			}
			// An event arrived: the provider is alive. Reset the
			// inactivity window. Stop-then-drain-then-reset is the
			// race-free Timer reset idiom.
			if !streamIdle.Stop() {
				select {
				case <-streamIdle.C:
				default:
				}
			}
			streamIdle.Reset(streamInactivityWindow)
		case <-streamIdle.C:
			// No provider event for the full inactivity window — a
			// silent stall. Cancel the stream context so the adapter
			// tears down the HTTP connection, then fall through to the
			// post-loop `streamStalled` handler.
			streamStalled = true
			break streamLoop
		}
		diagProvEventCount++
		switch evt.Type {
		case "delta":
			if lastPTYToolPending != "" && chat.IsCLIProvider(providerName) {
				s.streams.BroadcastPresence(chat.PresenceEvent{
					Type: "tool_resolved", SessionID: sessionID, AgentID: agent.ID,
					ToolName: lastPTYToolPending, Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				lastPTYToolPending = ""
			}
			turnContent.WriteString(evt.Content)
			run.fullContent.WriteString(evt.Content)
			// Buffer for phase-tagged flush after stopReason is known.
			// CW-20260418-0043 diagnostic watchdog is deferred to flush site.
			iterDeltaBuf = append(iterDeltaBuf, evt.Content)

		case "tool_use":
			if evt.ToolUse != nil {
				toolUseBlocks = append(toolUseBlocks, *evt.ToolUse)
				if chat.IsCLIProvider(providerName) {
					if lastPTYToolPending != "" {
						s.streams.BroadcastPresence(chat.PresenceEvent{
							Type: "tool_resolved", SessionID: sessionID, AgentID: agent.ID,
							ToolName: lastPTYToolPending, Timestamp: time.Now().UTC().Format(time.RFC3339),
						})
					}
					lastPTYToolPending = evt.ToolUse.Name
					s.streams.BroadcastPresence(chat.PresenceEvent{
						Type: "tool_pending", SessionID: sessionID, AgentID: agent.ID,
						ToolName: evt.ToolUse.Name, Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
					s.maybeCreateAutoArtifact(sessionID, assistantMsgID, agent.ID, *evt.ToolUse)
				}
			}

		case "usage":
			if evt.Usage != nil {
				if run.finalUsage == nil {
					run.finalUsage = &chat.Usage{}
				}
				if evt.Usage.InputTokens > 0 {
					run.finalUsage.InputTokens += evt.Usage.InputTokens
				}
				if evt.Usage.OutputTokens > 0 {
					run.finalUsage.OutputTokens += evt.Usage.OutputTokens
				}
				if evt.Usage.CacheCreationTokens > 0 {
					run.finalUsage.CacheCreationTokens += evt.Usage.CacheCreationTokens
				}
				if evt.Usage.CacheReadTokens > 0 {
					run.finalUsage.CacheReadTokens += evt.Usage.CacheReadTokens
				}
				if evt.Usage.StopReason != "" {
					stopReason = evt.Usage.StopReason
					run.finalUsage.StopReason = evt.Usage.StopReason
				}
			}

		case "error":
			// T9 — mid-stream overflow recovery: same pattern as the
			// stream-start case above. Streaming surgery isn't attempted
			// (plan non-goal) — we abort the in-flight stream, recover,
			// and the outer loop retries with a rebuilt request.
			if ctxpkg.IsContextOverflowMessage(evt.Error) && run.loop.compactRecoverableAttempts < maxCompactRecoverableAttempts {
				run.loop.compactRecoverableAttempts++
				run.loop.continueWith(ContinueRecovery, "context_overflow mid-stream")
				if newMsgs, newTools, ok := s.recoverFromContextOverflow(ctx, sessionID, slotResult, agent, run.chatMessages, run.tools, ch, evt.Error, compactTriggerContextOverflow); ok {
					// Mirror the stream-start recovery path: strip_tool_blocks
					// drops tool-definition context, so post-compaction the
					// LLM has to re-request tools. Reset the discovery-call
					// counters so pre-compaction calls don't eat the
					// post-compaction budget (CW-20260419-0012).
					run.loop.totalRequestToolsCalls = 0
					run.loop.consecutiveEmptyRequests = 0
					run.chatMessages = newMsgs
					run.tools = newTools
					run.systemPrompt = slotResult.SystemPrompt
					contextOverflowRecovered = true
					// Labeled break — without the label, `break` would exit
					// the switch only, and the stream loop would keep
					// draining events. Copilot review #3095050032.
					break streamLoop
				}
			}
			errDetails := map[string]interface{}{"raw": evt.Error, "model": model}
			// CW-20260517-0036 — the loop is about to exit on a
			// mid-stream provider error; release the per-iteration
			// stream context before any return path below.
			streamIdle.Stop()
			attempt.cancelStream()
			if ctxpkg.IsContextOverflowMessage(evt.Error) && run.loop.compactRecoverableAttempts >= maxCompactRecoverableAttempts {
				msg := "Context is still too large after compaction. Use `/clear` or split the request."
				errDetails["recovery"] = "failed_after_retry"
				if s.surfaceErrorOrSuppress(ch, sessionID, "midstream_failed_after_retry", msg, errDetails, run.fullContent.String()) {
					return consumeProviderIterationResult{directive: generationTerminate}
				}
				// surfaceErrorOrSuppress already classified above; use the pre-classified
				// broker-notify variant. CW-20260512-0001, CW-20260512-0002.
				s.persistPartialAssistantAndNotifyBrokerPreClassified(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, fmt.Errorf("%s", evt.Error)) // CW-20260419-0019, CW-20260512-0001, CW-20260512-0002
				return consumeProviderIterationResult{directive: generationTerminate}
			}
			// Mid-stream provider error (general). Same suppression rule
			// as the pre-stream provider error above: if a subagent is
			// active, the FE shouldn't see this surface.
			if s.suppressSurfaceIfSubagentCaused(sessionID, "midstream_provider_error", run.fullContent.String()) {
				return consumeProviderIterationResult{directive: generationTerminate}
			}
			s.surfaceProviderFailure(ctx, ch, sessionID, assistantMsgID, agentID, run.fullContent.String(), model, providerName, agent.Slug, fmt.Errorf("%s", evt.Error), nil)
			return consumeProviderIterationResult{directive: generationTerminate}

		case "session_id":
			// Phase 4c.6 (CW-20260508-0002): persistCLISessionID
			// deleted. Boot's StartOptions.OnSessionID writes the
			// provider session id straight to the agent_runtime row
			// via SetProviderSessionID — no chat-harness side
			// persistence needed. The case branch is kept so
			// EventSessionID events remain a known type the loop
			// observes and discards (vs falling into the default).
			_ = evt.SessionID

		case "openai_response_output":
			// Opaque Responses items are replayed only within this tool loop.
			// They contain encrypted reasoning and must never become UI deltas.
			run.providerOutput = &llmtypes.ContentBlock{Type: "openai_response_output", Text: evt.Content}

		case "thinking":
			// F3 (CW-20260420-0023): interleaved thinking block. Persist
			// signed block for round-trip; emit to FE as PhaseThinking.
			if evt.ThinkingBlock != nil {
				last := len(run.thinkingBlocks) - 1
				if last >= 0 && evt.ThinkingBlock.Signature == "" && run.thinkingBlocks[last].Signature == "" {
					// Responses summaries arrive as unsigned deltas. Keep their
					// persisted text contiguous while rendering each delta live.
					run.thinkingBlocks[last].Thinking += evt.ThinkingBlock.Thinking
				} else {
					run.thinkingBlocks = append(run.thinkingBlocks, *evt.ThinkingBlock)
				}
				stopDiag := diagWatchChSend(ctx, "streamLoop.thinking", ch, sessionID, assistantMsgID, run.loop.iteration, "delta")
				ch <- chat.StreamEvent{Type: "delta", Content: evt.ThinkingBlock.Thinking, Phase: chat.PhaseThinking}
				stopDiag()
			}

		case "done":
			// handled below
		}
	}

	// CW-20260517-0036 — release the per-iteration stream resources.
	// Stop the inactivity timer (it may still be armed when the loop
	// exited on a closed channel or a labeled break), and cancel the
	// stream context so a still-running provider goroutine is torn down
	// rather than leaking to the next iteration.
	streamIdle.Stop()
	attempt.cancelStream()

	// CW-20260517-0036 — provider-stream inactivity timeout fired.
	// The provider held the channel open but produced no event for the
	// full inactivity window: a silent stall. attempt.cancelStream() above
	// has already torn down the HTTP stream. Surface this as a
	// structured `stalled` error and end the turn — the same shape the
	// mid-stream provider-error branch uses, with a distinct cause so
	// operators (and subagent_runs.error) can tell a genuine stall from
	// a provider-emitted error.
	if streamStalled {
		attempt.span.SetStatus(codes.Error, "provider stream inactivity timeout")
		attempt.close()
		stallErr := fmt.Errorf("provider stream stalled: no events for %s", streamInactivityWindow)
		slog.Warn("chat-service: provider stream inactivity timeout — terminating stalled stream",
			"session_id", sessionID, "iter", run.loop.iteration,
			"inactivity_window", streamInactivityWindow.String(),
			"events_seen", diagProvEventCount,
			"caller", string(dispatcher.CallerTypeFromContext(ctx)))
		// Outcome bookkeeping must survive cancellation of the provider stream it records.
		s.store.LogEvent(context.WithoutCancel(ctx), sessionID, "provider_stream_stalled", "error",
			fmt.Sprintf("iteration %d: no provider events for %s", run.loop.iteration, streamInactivityWindow),
			fmt.Sprintf(`{"model":%q,"inactivity_window_s":%d,"events_seen":%d}`,
				model, int(streamInactivityWindow.Seconds()), diagProvEventCount))
		if s.events != nil {
			s.events.EmitError(ctx, sessionID, "provider_stream_stalled", stallErr.Error())
		}
		// Subagent suppression: if a subagent is active, the parent FE
		// shouldn't see the stall surface (mirrors the provider-error
		// branches). The subagent run still fails — drainCapture sees
		// the error event below.
		if s.suppressSurfaceIfSubagentCaused(sessionID, "provider_stream_stalled", run.fullContent.String()) {
			return consumeProviderIterationResult{directive: generationTerminate}
		}
		stallDetails := map[string]interface{}{
			"raw":               stallErr.Error(),
			"model":             model,
			"cause":             "stalled",
			"inactivity_window": streamInactivityWindow.String(),
		}
		ch <- chat.ErrorEnvelopeDelta(chat.ClassifyError(stallErr), "Provider stream stalled — no response", stallDetails)
		ch <- chat.ErrorEvent(chat.ClassifyError(stallErr), "Provider stream stalled — no response", stallDetails)
		s.persistPartialAssistantAndNotifyBroker(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, stallErr)
		return consumeProviderIterationResult{directive: generationTerminate}
	}

	// CW-20260418-0043 diagnostic — provider stream closed.
	diagIteration := run.loop.iteration
	if contextOverflowRecovered {
		// The coordinator owns the actual decrement when it interprets the
		// retry directive. Preserve the pre-extraction diagnostic value, which
		// observed that decrement before logging the recovered attempt.
		diagIteration--
	}
	diagLogProviderStream(sessionID, assistantMsgID, diagIteration,
		time.Since(provStreamStart), diagProvEventCount, stopReason, len(toolUseBlocks))

	// F4 (CW-20260419-0029) — flush buffered deltas with the correct phase.
	// stopReason is now known: "tool_use" → narration, anything else → final.
	// On context-overflow recovery (contextOverflowRecovered) we discard the
	// buffer — the iteration is being retried so the partial content is stale.
	if len(iterDeltaBuf) > 0 && !contextOverflowRecovered {
		phase := chat.PhaseNarration
		if stopReason != "tool_use" {
			phase = chat.PhaseFinal
		}
		for _, fragment := range iterDeltaBuf {
			stopDiag := diagWatchChSend(ctx, "streamLoop.delta.flush", ch, sessionID, assistantMsgID, run.loop.iteration, "delta")
			ch <- chat.StreamEvent{Type: "delta", Content: fragment, Phase: phase}
			stopDiag()
		}
	}

	// F4 — route turnContent to the right accumulator now that phase is known.
	// This drives the persistence split: run.narrationContent → metadata.thinking,
	// run.finalContent → message.Content (via cleanContent / WrapResponse).
	if !contextOverflowRecovered {
		turnText := turnContent.String()
		if stopReason == "tool_use" {
			if turnText != "" {
				run.narrationContent.WriteString(turnText)
				run.narrationContent.WriteString("\n")
			}
		} else {
			// The last iteration — and any early-exit text already in
			// run.fullContent that didn't come from tool_use iterations.
			run.finalContent.WriteString(turnText)
		}
	}

	// T9 — the mid-stream overflow handler broke out of the stream loop so
	// the outer loop can retry with a compacted request. Skip the
	// post-stream processing (no content produced this attempt) and
	// continue.
	if contextOverflowRecovered {
		attempt.close()
		return consumeProviderIterationResult{directive: generationRetryIteration}
	}

	// Resolve remaining PTY tool presence.
	if lastPTYToolPending != "" && chat.IsCLIProvider(providerName) {
		s.streams.BroadcastPresence(chat.PresenceEvent{
			Type: "tool_resolved", SessionID: sessionID, AgentID: agent.ID,
			ToolName: lastPTYToolPending, Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	attempt.close()

	// If no tool use, check whether this was an unfulfilled promissory preamble
	// on iteration 0 that can be self-healed before concluding the turn.
	if stopReason != "tool_use" || len(toolUseBlocks) == 0 {
		turnText := turnContent.String()
		if !chat.IsCLIProvider(providerName) &&
			run.loop.iteration == 0 &&
			run.loop.preambleNudgeCount == 0 &&
			len(run.tools) > 0 &&
			len(run.loop.toolCallRefs) == 0 &&
			chat.IsPromissoryPreamble(turnText) {

			run.loop.preambleNudgeCount++
			slog.Info("chat-service: promissory preamble detected without tool calls; injecting self-healing nudge",
				"session_id", sessionID, "iter", run.loop.iteration, "text_len", len(turnText))
			s.store.LogEvent(context.WithoutCancel(ctx), sessionID, "preamble_stall_recovery", "recovery",
				"Injected self-healing nudge after promissory preamble ended turn without tool calls",
				fmt.Sprintf(`{"model":%q,"iteration":%d,"text_len":%d}`, model, run.loop.iteration, len(turnText)))

			// Clear final content in UI and convert preamble text to narration/thinking
			ch <- chat.StreamEvent{Type: "replace_content", Content: ""}
			if turnText != "" {
				ch <- chat.StreamEvent{Type: "delta", Phase: "narration", Content: turnText + "\n"}
				run.narrationContent.WriteString(turnText)
				run.narrationContent.WriteString("\n")
			}
			run.finalContent.Reset()

			// Reset per-iteration thinking accumulator so next iteration starts fresh.
			run.thinkingBlocks = run.thinkingBlocks[:0]
			run.providerOutput = nil

			// Append assistant turn with the promissory preamble text
			run.chatMessages = append(run.chatMessages, llmtypes.ChatMessage{
				Role: "assistant",
				ContentBlocks: []llmtypes.ContentBlock{
					{Type: "text", Text: turnText},
				},
			})

			// Append synthetic recovery nudge as user message
			run.chatMessages = append(run.chatMessages, llmtypes.ChatMessage{
				Role: "user",
				ContentBlocks: []llmtypes.ContentBlock{
					{Type: "text", Text: chat.PromissoryPreambleRecoveryNudge},
				},
			})

			run.loop.touchActivity()
			run.loop.continueWith(ContinuePreambleNudge, "promissory preamble nudge")
			return consumeProviderIterationResult{directive: generationContinueIteration}
		}

		if stopReason == "max_tokens" {
			run.loop.wasTruncated = true
			slog.Warn("chat-service: response truncated by max_tokens", "iter", run.loop.iteration)
			ch <- chat.StreamEvent{Type: "status", Content: "Response was cut short due to length limits. Some content may be missing."}
			// Outcome bookkeeping must survive cancellation of the provider response it records.
			s.store.LogEvent(context.WithoutCancel(ctx), sessionID, "max_tokens_truncation", "warning",
				fmt.Sprintf("iteration %d: response truncated by max_tokens", run.loop.iteration),
				fmt.Sprintf(`{"model":%q,"iteration":%d}`, model, run.loop.iteration))
			ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, "Response truncated — hit output token limit", map[string]interface{}{
				"stop_reason": "max_tokens", "iteration": run.loop.iteration, "model": model,
			})
		}
		diagLogLoopExit(sessionID, assistantMsgID, run.loop.iteration, "done:stop_reason="+stopReason, len(run.loop.toolCallRefs), ch)
		return consumeProviderIterationResult{directive: generationFinishRun}
	}

	return consumeProviderIterationResult{
		directive: generationProceed,
		turn:      providerTurn{content: turnContent.String(), toolUseBlocks: toolUseBlocks},
	}
}

type finalizeRunResult struct {
	directive generationDirective
}

func (s *chatServiceImpl) finalizeRun(
	ctx context.Context,
	sessionID string,
	assistantMsgID string,
	setup *turnSetup,
	run *runState,
	lifecycle *generationLifecycle,
	ch chan chat.StreamEvent,
) finalizeRunResult {
	session := setup.session
	agent := setup.agent
	model := setup.model
	providerName := setup.providerName
	prov := setup.provider
	fctx := setup.filterContext
	userContent := setup.userContent
	// --- Post-processing ---
	// F4 (CW-20260419-0029): responseContent operates on run.finalContent only —
	// the narration (inter-iteration prose) is stored separately in metadata.
	// run.fullContent still accumulates everything for legacy/error paths that need
	// the full stream (e.g. persistPartialAssistant).
	//
	// If run.finalContent is empty (e.g. the loop exited on circuit_open with no
	// final iteration, or directReturn was set), fall back to run.fullContent so
	// the stored message is not empty. Old behavior preserved for those paths.
	finalText := run.finalContent.String()
	if finalText == "" {
		finalText = run.fullContent.String()
	}
	responseContent := finalText
	if s.outputFilter != nil && s.outputFilter.Len() > 0 {
		responseContent = s.outputFilter.Apply(responseContent)
	}
	// Filter: assistant_response — tone filter, brand voice, compliance scrubbing.
	if s.pluginHost != nil {
		if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterAssistantResponse, responseContent, fctx); err != nil {
			slog.Warn("chat-service: assistant_response filter error", "err", err)
		} else if fs, ok := filtered.(string); ok {
			responseContent = fs
		}
	}

	// Inject pending envelopes. These are appended post-loop so they are
	// always part of the final response (PhaseFinal).
	for _, env := range run.loop.pendingEnvelopes {
		envelopeBlock := "\n\n```nanite-envelope\n" + env + "\n```"
		responseContent += envelopeBlock
		ch <- chat.StreamEvent{Type: "delta", Content: envelopeBlock, Phase: chat.PhaseFinal}
	}

	// Parse envelopes.
	envelopes, cleanContent, envErrors := chat.ParseEnvelopes(responseContent)
	// Outcome bookkeeping must survive cancellation of the response parsing it records.
	envelopeOutcomeCtx := context.WithoutCancel(ctx)
	for _, envErr := range envErrors {
		slog.Warn("chat-service: envelope error", "reason", envErr.Reason, "content", chat.TruncateStr(envErr.Raw, 200))
		s.store.LogEvent(envelopeOutcomeCtx, sessionID, "envelope_error", "warning",
			envErr.Reason, fmt.Sprintf(`{"raw":%q}`, chat.TruncateStr(envErr.Raw, 500)))
	}

	// CLI envelope retry.
	//
	// CW-20260514-0045: retryEnvelopeCorrection drives a fresh
	// prov.StreamChat for the correction prompt — meaningful only when
	// the registry returned a real llmcontracts.Provider. The dropdown
	// CLI bypass (resolveProvider returns nil) leaves prov == nil; skip
	// the retry rather than NPE on prov.StreamChat. The CLI runtime
	// path doesn't go through llmcontracts.StreamChat anyway, so the
	// correction would mis-route even if we built a fake provider here.
	if len(envErrors) > 0 && chat.IsCLIProvider(providerName) && prov != nil {
		hasFatal := false
		for _, envErr := range envErrors {
			if envErr.Reason == "invalid_json" {
				hasFatal = true
				break
			}
		}
		if hasFatal {
			retryEnvelopes := s.retryEnvelopeCorrection(ctx, sessionID, session, prov, model, envErrors, ch)
			envelopes = append(envelopes, retryEnvelopes...)
		}
	}

	var envelopeJSON string
	if len(envelopes) > 0 {
		if data, err := json.Marshal(envelopes); err == nil {
			envelopeJSON = string(data)
		}
	}

	var envRefs []chat.EnvelopeRef
	for i, env := range envelopes {
		// Filter: envelope_data — enrich card data, add links, transform fields.
		if s.pluginHost != nil && env.Data != nil {
			if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterEnvelopeData, env.Data, fctx); err != nil {
				slog.Warn("chat-service: envelope_data filter error", "type", env.Type, "err", err)
			} else if fd, ok := filtered.(map[string]interface{}); ok {
				envelopes[i].Data = fd
				env = envelopes[i]
			}
		}
		innerData, _ := json.Marshal(env.Data)
		// CW-20260429-0019: copy the routing-relevant fields onto the
		// persisted ref so page reload can route the card to its intended
		// drawer/panel. Dropping these here was the c110 regression.
		envRefs = append(envRefs, chat.EnvelopeRef{
			Type:                env.Type,
			Data:                json.RawMessage(innerData),
			ID:                  env.ID,
			Title:               env.Title,
			Subtitle:            env.Subtitle,
			Target:              env.Target,
			Mode:                env.Mode,
			RenderTarget:        env.RenderTarget,
			RenderTargetBlocked: env.RenderTargetBlocked,
		})
		// Emit envelope.rendered plugin event for each envelope attached to the response.
		if s.pluginHost != nil {
			envType := env.Type
			envData := env.Data
			s.goTracked("emit.envelope-rendered", func(context.Context) {
				s.pluginHost.EmitEnvelopeRendered(sessionID, envType, envData)
			})
		}
	}

	// CW-20260429-0029: broadcast a plugin_envelope SSE event for every parsed
	// envelope that carries a panel-routing hint (target/render_target/mode).
	// The FE's useChat plugin_envelope handler calls applyEnvelopePanelEffects
	// on each event, which is the ONLY path that opens the bottom_chat_drawer
	// or routes to a render_target. Without this broadcast, show_card
	// envelopes (which arrive as `<!--ENVELOPE_DATA:...-->` markers in the
	// assistant text and reach the FE only as part of StructuredMessage)
	// rendered correctly but never triggered the drawer-open — render_target
	// became dead weight on the wire.
	//
	// Note: we do NOT call CreateEnvelopeInstance here. show_card envelopes
	// are passive (no response routing) and are already persisted as part of
	// the assistant message's `envelopes` field. Persistence stays the
	// responsibility of interactive paths (approval / elicitation) where the
	// row ID is needed for response endpoints.
	for _, env := range envelopes {
		if env.Target == "" && env.RenderTarget == "" && env.Mode == "" && env.RenderTargetBlocked == "" {
			continue
		}
		envID := env.ID
		if envID == "" {
			envID = uuid.New().String()
		}
		innerData, err := json.Marshal(env.Data)
		if err != nil {
			slog.Warn("chat-service: marshal envelope data for plugin_envelope broadcast", "type", env.Type, "err", err)
			continue
		}
		streamWrap, err := buildPluginEnvelopeWrap(envID, env.Type, innerData, EnvelopeRouting{
			Target:              env.Target,
			RenderTarget:        env.RenderTarget,
			RenderTargetBlocked: env.RenderTargetBlocked,
			Mode:                env.Mode,
			DisplayClass:        EnvelopeDisplayClassContent,
		})
		if err != nil {
			slog.Warn("chat-service: marshal plugin_envelope wrap", "type", env.Type, "err", err)
			continue
		}
		s.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
			Type:     "plugin_envelope",
			Envelope: string(streamWrap),
		})
	}

	// Determine tier.
	tier := "default"
	if len(run.loop.toolCallRefs) > 0 {
		tier = "tool"
	}
	hasError := false
	for _, e := range envRefs {
		if e.Type == "error-report" {
			hasError = true
			break
		}
	}

	// CW-20260429-0026: harness-side failure-footer fallback. If the per-turn
	// tool_calls accumulator contains any Status:"error" entries AND the
	// model didn't already acknowledge failure in the response text, append
	// a small footer note so the user is oriented. No-op when there are no
	// errors, when the model already acknowledged, or when disabled via
	// NANITE_HARNESS_FAILURE_FOOTER. Mutate cleanContent so the footer is
	// part of the persisted text (and the structured-message hash).
	cleanContent = maybeAppendFailureFooter(cleanContent, run.loop.toolCallRefs)

	// Structured message.
	structured := chat.WrapResponse(cleanContent, tier, run.loop.toolCallRefs, envRefs, run.loop.wasTruncated, hasError)
	chat.LogStructuredWarnings(structured)
	structuredJSON := structured.MarshalContent()

	// Build message metadata. F4 (CW-20260419-0029): narration goes in
	// metadata.thinking so the "expand thinking" UI affordance works across
	// page refresh. Only stored when non-empty (tool-use turns).
	// F3 (CW-20260420-0023): signed thinking blocks go in metadata.thinking_blocks
	// (separate key from F4's narration prose so they round-trip with signatures).
	type thinkingBlockMeta struct {
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
	}
	meta := map[string]any{}
	if thinking := run.narrationContent.String(); thinking != "" {
		meta["thinking"] = thinking
	}
	if len(run.thinkingBlocks) > 0 {
		blocks := make([]thinkingBlockMeta, len(run.thinkingBlocks))
		for i, b := range run.thinkingBlocks {
			blocks[i] = thinkingBlockMeta{Thinking: b.Thinking, Signature: b.Signature}
		}
		meta["thinking_blocks"] = blocks
	}
	msgMetadata := "{}"
	if len(meta) > 0 {
		if metaJSON, err := json.Marshal(meta); err == nil {
			msgMetadata = string(metaJSON)
		}
	}

	// Save assistant message.
	assistantMsg := &store.Message{
		ID: assistantMsgID, SessionID: sessionID, AgentID: agent.ID,
		Role: "assistant", Content: structuredJSON, Envelope: envelopeJSON,
		Metadata: msgMetadata,
	}
	if err := s.store.CreateMessage(ctx, assistantMsg); err != nil {
		slog.Error("chat-service: failed to save assistant message", "err", err)
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to save response", map[string]interface{}{"raw": err.Error()})
		return finalizeRunResult{directive: generationTerminate}
	}

	// PruneAfterTurn retired in Phase 3 S3a — slot compaction supersedes.
	// The ContextService interface method remains for one release so out-of-tree
	// callers don't break; removal is a follow-up.

	// Outcome bookkeeping must survive cancellation of the completed turn it records.
	persistCtx := context.WithoutCancel(ctx)

	// Record token usage.
	if run.finalUsage != nil && (run.finalUsage.InputTokens > 0 || run.finalUsage.OutputTokens > 0) {
		toolInputTokens := 0
		if run.breakdown != nil {
			toolInputTokens = run.breakdown.Tools
		}
		if err := s.store.RecordUsage(persistCtx, sessionID, assistantMsgID, model,
			run.finalUsage.InputTokens, run.finalUsage.OutputTokens, toolInputTokens,
			run.finalUsage.CacheCreationTokens, run.finalUsage.CacheReadTokens); err != nil {
			slog.Warn("chat-service: failed to record token usage", "err", err)
		}
	}

	// Record execution metrics.
	adapterType := "http"
	if chat.IsPTYProvider(providerName) {
		adapterType = "pty"
	} else if strings.HasPrefix(providerName, "sub-") {
		adapterType = "sub"
	}
	metrics := &store.ExecutionMetrics{
		SessionID: sessionID, MessageID: assistantMsgID,
		Provider: providerName, Adapter: adapterType, Model: model,
		AgentID: agent.ID, AgentSlug: agent.Slug,
		DurationMs:      time.Since(lifecycle.startTime).Milliseconds(),
		ContextMessages: len(run.chatMessages), ToolIterations: run.loop.iteration,
		ToolCalls: len(run.loop.toolCallRefs),
	}
	if run.breakdown != nil {
		metrics.ContextTokens = run.breakdown.Total
	}
	if run.finalUsage != nil {
		metrics.InputTokens = run.finalUsage.InputTokens
		metrics.OutputTokens = run.finalUsage.OutputTokens
		metrics.CacheCreationTokens = run.finalUsage.CacheCreationTokens
		metrics.CacheReadTokens = run.finalUsage.CacheReadTokens
		metrics.StopReason = run.finalUsage.StopReason
	}
	// Attach debug snapshots if captured.
	if run.loop.debugMode && len(run.loop.snapshots) > 0 {
		if snapJSON, err := json.Marshal(run.loop.snapshots); err == nil {
			metrics.DebugSnapshots = string(snapJSON)
		}
	}
	if err := s.store.RecordExecutionMetrics(persistCtx, metrics); err != nil {
		slog.Warn("chat-service: failed to record execution metrics", "err", err)
	}

	// CW-20260420-0032: mark the PTY turn as successful so the deferred
	// closer emits pty_turn_complete instead of pty_turn_failed.
	lifecycle.ptyTurnSucceeded = true

	// Stream end.
	ch <- chat.StreamEvent{Type: "stream_end", MessageID: assistantMsgID, Usage: run.finalUsage, AgentID: agent.ID, Envelope: envelopeJSON}

	// Post-response events.
	if s.events != nil && run.finalUsage != nil {
		s.events.EmitResponseComplete(ctx, sessionID, agent.ID, model, run.finalUsage.InputTokens, run.finalUsage.OutputTokens)
	}
	if s.events != nil {
		elapsed := time.Since(lifecycle.startTime).Milliseconds()
		contentPreview := envelopeJSON
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500]
		}
		s.events.EmitMessageReceived(ctx, sessionID, assistantMsgID, contentPreview, elapsed)
	}

	// Auto-title and auto-tags.
	if session.Title == "" {
		s.goTracked("autoTitle", func(bgCtx context.Context) {
			s.autoTitle(bgCtx, sessionID, userContent)
		})
	}
	s.goTracked("autoTags", func(bgCtx context.Context) {
		s.autoTags(bgCtx, sessionID)
	})

	return finalizeRunResult{directive: generationProceed}
}

func (s *chatServiceImpl) prepareTurn(
	ctx context.Context,
	sessionID string,
	userContent string,
	ch chan chat.StreamEvent,
) prepareTurnResult {
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to load session", map[string]interface{}{"raw": err.Error()})
		return prepareTurnResult{directive: generationTerminate}
	}

	// Advisory: warn once per session when the memory embedder isn't active.
	// Fires regardless of whether memory sources get queried on this turn —
	// users see the state without having to trigger a recall.
	s.maybeEmitEmbeddingWarning(sessionID, ch)

	// --- Resolve agent ---
	agent, err := s.agents.ResolveForSession(ctx, sessionID)
	if err != nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to resolve agent", map[string]interface{}{"raw": err.Error()})
		return prepareTurnResult{directive: generationTerminate}
	}
	agentID := agent.ID

	// Block disabled agents.
	if agent.Status == "disabled" {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, fmt.Sprintf("Agent %q is disabled", agent.Name), nil)
		return prepareTurnResult{directive: generationTerminate}
	}

	// Parse agent constraints (schema v2).
	//
	// CW-20260512-0123 (SP-20260512-0011 W3): the per-agent
	// `MaxTimeSeconds` opt-in wall-clock that used to wrap ctx with
	// context.WithTimeout was removed entirely. That field was the
	// origin of the c160 "Agent execution time limit exceeded
	// (0 seconds)" error class — when MaxTimeSeconds defaulted to 0
	// the wrap fired immediately. The class is now unreachable: the
	// field is gone from chat.AgentConstraints and there is no
	// remaining call site that produces a context.DeadlineExceeded
	// here. Hung subagents are bounded by the subagent reaper
	// (internal/subagent/reaper.go), and runaway chat loops are
	// bounded by the in-loop runaway-fail-cap + idle-timeout +
	// hard-ceiling (see resolveIterationLimits in chat_loop_state.go).
	constraints := chat.ParseAgentConstraints(agent.Constraints)

	// --- Resolve model ---
	// CW-20260526-0003: model resolution walks session → agent →
	// store.ResolveProviderAndModel (user_settings.default_model →
	// providers.default_model). The previous chain dead-ended on a Go
	// literal which silently masked misconfiguration (the bare-alias
	// `claude-sonnet-4` 404 bug).
	//
	// Provider routing is handled separately by resolveProvider /
	// chat.InferProvider below — we only invoke the resolver when model
	// is empty, and we use the model-derived provider as a hint for the
	// per-provider default_model lookup (the resolver returns it for
	// free). Calling the resolver when an explicit model is already
	// present would risk masking provider routing the downstream knows
	// better than we do.
	model := session.Model
	if model == "" && agent.DefaultModel != "" {
		model = agent.DefaultModel
	}
	if model == "" {
		explicitProvider := session.Provider
		if explicitProvider == "" {
			explicitProvider = agent.DefaultProvider
		}
		_, resolved, resolveErr := s.store.ResolveProviderAndModel(ctx, explicitProvider, "")
		if resolveErr != nil {
			ch <- chat.ErrorEvent(chat.ErrorCodeProviderError,
				"No default model configured. Set providers.default_model or user_settings.default_model.",
				map[string]interface{}{"raw": resolveErr.Error()})
			return prepareTurnResult{directive: generationTerminate}
		}
		model = resolved
	}

	// --- Resolve provider ---
	providerName, prov := s.resolveProvider(sessionID, session.Provider, agent.DefaultProvider, model, agent.RuntimeKind)
	if prov == nil {
		// CW-20260514-0045: dropdown-selected CLI providers (e.g.
		// "pty-claude", "pty-codex", "pty-opencode", legacy "pty") no
		// longer register an llmcontracts.Provider — the bridges were
		// removed when agent-sessions became the runtime path (Phase
		// 4c.6, CW-20260508-0002). Resolution therefore returns
		// (name, nil) for these, but classifyNilProvider below still
		// routes a legitimate CLI turn (per agent.RuntimeKind, Phase 2
		// item 01) to driveBootSession — see the per-iteration provider
		// call site further down, which now branches on `prov == nil`
		// (this decision, made exactly once here) rather than
		// re-deriving CLI-ness from providerName's string shape.
		//
		// Phase 3 item 01 (TASKS/phase-3/01-collapse-resolveprovider-
		// into-cascade.md): resolveProvider above now also threads
		// agent.RuntimeKind through its own walk, so a runtime_kind='cli'
		// agent can no longer silently resolve to a non-nil HTTP provider
		// in the first place (the bug this task fixed) — classifyNilProvider
		// here is now purely the nil-provider classification step, not
		// the only place runtime_kind is consulted.
		switch s.classifyNilProvider(agent.RuntimeKind, providerName) {
		case nilProviderRouteCLI:
			slog.Info("chat-service: CLI provider routed to agent runtime (no llmcontracts.Provider registered)",
				"session_id", sessionID, "provider", providerName)
			// Fall through; the CLI branch at the per-iteration
			// provider call site routes to driveBootSession. `prov`
			// stays nil and is only dereferenced via comma-ok type
			// assertions on the non-CLI path.
		case nilProviderRouteCLINoAdapter:
			ch <- chat.ErrorEvent(chat.ErrorCodeProviderError,
				fmt.Sprintf("CLI provider %q has no runtime adapter registered.", providerName),
				map[string]interface{}{"raw": fmt.Sprintf("CLI provider %q not in agent runtime adapter index", providerName)})
			return prepareTurnResult{directive: generationTerminate}
		default:
			ch <- chat.ErrorEvent(chat.ErrorCodeProviderError,
				fmt.Sprintf("Provider %q not available — check configuration and restart the server.", providerName),
				map[string]interface{}{"raw": fmt.Sprintf("provider %q not registered", providerName)})
			return prepareTurnResult{directive: generationTerminate}
		}
	}

	// Wire status callback for retry notifications and circuit breaker.
	if ap, ok := prov.(*nllmanthropic.Client); ok {
		ap.OnStatus = func(message string) {
			ch <- chat.StreamEvent{Type: "status", Content: message}
		}
		ap.OnCircuitOpen = func() {
			ch <- chat.StreamEvent{
				Type:    "circuit_open",
				Content: "Provider rate limited after multiple retries. Would you like to keep trying?",
			}
		}
	}

	// Cache hints are set per-call via ChatRequest.CacheHints below (and on
	// each EstimateCacheablePrefix / StreamChat invocation in the main loop)
	// so concurrent sessions sharing this provider singleton don't race on
	// a mutated field. The deprecated llmcontracts.CacheableProvider /
	// SetCacheHints pathway is intentionally NOT exercised here — see
	// FU-13 / CW-20260520-0054 for the cache-miss-echo signature that
	// motivated the migration.
	cacheStrategy := llmcontracts.DefaultCacheStrategy()

	// --- Tool selection via ToolService (must precede slot assembly so the
	// Tools slot and the dynamic system prefix can be derived from the result). ---
	// Thread the per-model context window so the tool token budget is computed
	// from the actual model window (e.g. 1M for Gemini) rather than the
	// hardcoded 200K default. contextWindowSize returns 0 on miss, which causes
	// the broker to fall back to DefaultContextWindowTokens (CW-20260426-0032).
	// Phase 0 item 20 (retire workspaces): session.WorkspaceID no longer
	// exists — SelectForAgent's workspaceID param (toolclient's
	// Config.WorkspaceOverrides rule-merge hook) has no populated loader in
	// production today, so this is a no-op change, not a feature removal.
	selection, err := s.tools.SelectForAgent(ctx, sessionID, agentID, userContent, "", s.contextWindowSize(providerName, model))
	if err != nil {
		slog.Warn("chat-service: tool selection failed", "err", err)
		selection = &ToolSelection{}
	}
	tools := selection.Tools

	// Filter: tool_selection — a plugin may add, remove, or reshape the
	// tool list offered to the model this turn. Applied here, immediately
	// after SelectForAgent returns, rather than wiring pluginHost into
	// toolServiceImpl.SelectForAgent itself — matching the exact pattern
	// the other five chat_generate.go-resident filters already use
	// (system_prompt / user_message / context_window / assistant_response /
	// envelope_data all operate on the value about to be used, not on
	// SelectForAgent's internal intermediate state). Consequence: a plugin
	// sees the fully-resolved tool list (post agent_tools grant filter,
	// post cap/token-budget prune, post always_included escape hatch, post
	// progressive-discovery repackaging when active) — not the broker's
	// raw pre-filter output. See
	// TASKS/phase-4/06-add-filter-tool-selection.md's Work Log for the full
	// insertion-point reasoning.
	//
	// Applied before normalizeToolInputSchemas (below) so a plugin-added
	// tool's schema is normalized too, and before the tools-lazy-load
	// essential/lazy partition and the no-tools warning check further down
	// so both see the plugin-filtered list, not the pre-filter one.
	fctx := pluginpkg.FilterContext{SessionID: sessionID, AgentID: agentID}
	tools = applyToolSelectionFilter(s.pluginHost, tools, fctx)

	// Phase 0 item 21 ("Cut Modes, in full") deleted the B1 (CW-20260428-0009)
	// + F1 (CW-20260429-0001) session-mode tool_overrides block that used to
	// live here — it resolved session.CurrentModeID -> s.store.GetMode and
	// applied the mode's tool_overrides (deny > allow, explicit > pattern)
	// to the tool surface. Both the session-mode pointer and store.GetMode
	// are gone; there is no more per-session tool_overrides source.

	// G-HOT-SWAP-DEAD activation. NANITE_TOOLS_LAZY_LOAD=true partitions the
	// tool universe into "essential" (inline, full schemas) and "lazy"
	// (announced via a LoadHint pointer; fetched via request_tools). The
	// progressive-discovery path already curates a builtins-only surface,
	// so partition stacks above non-progressive selections only — applying
	// it on top of progressive would double-curate the same content.
	var toolsLazyHint string
	var lazyToolCount, essentialToolCount int
	lazyLoadActive := false
	if chat.IsToolsLazyLoadEnabled() && !selection.Progressive {
		prevState := s.loadToolPartitionState(sessionID)
		partition, newState := chat.PartitionTools(tools, nil, prevState, chat.ToolEssentialCap)
		s.storeToolPartitionState(sessionID, newState)
		if len(partition.Lazy) > 0 {
			lazyLoadActive = true
			lazyToolCount = len(partition.Lazy)
			essentialToolCount = len(partition.Essential)
			tools = partition.Essential
			// Ensure request_tools is reachable so the agent can hydrate
			// lazy entries on demand. Idempotent — partition leaves the
			// meta-tool in essential if it was in the input.
			if !containsToolNamed(tools, "request_tools") {
				tools = append(tools, toolclient.RequestToolsMetaTool())
			}
			toolsLazyHint = chat.RenderToolLazyHint(partition.Lazy)
			slog.Debug("chat-service: tools-lazy-load partition applied",
				"session_id", sessionID,
				"essential", essentialToolCount,
				"lazy", lazyToolCount,
				"hyst_pinned", len(newState.PromotedAt))
		}
	}

	// Cache navigation is a local, session-scoped harness capability. Keep its
	// schemas even when ordinary tools were pruned or deferred by lazy loading.
	if s.resultCache != nil {
		tools = unionToolsByName([]llmtypes.ToolDefinition{
			toolclient.FetchToolResultMetaTool(), toolclient.SearchToolResultMetaTool(),
		}, tools)
	}
	normalizeToolInputSchemas(tools)

	// Build the dynamic per-turn system prefix from tool selection. This text
	// is sent verbatim in ChatRequest.SystemPrompt (it leads the slot blocks
	// in the provider payload) and varies per turn; static agent / rules /
	// session content lives in the slot blocks.
	//
	// Determine the no-tools condition up front so we can both emit the client
	// warning event (side-effect, stays here) and pass the flag to the pure
	// composeExtraSystemPrefix helper.
	noTools := false
	if !selection.Progressive && !hasUsableTools(tools) {
		noTools = true
		warningPayload := chat.ToolWarningPayload{
			Error: "This agent has no tools configured. Responses will be text-only.",
			Level: "critical",
		}
		warningJSON, _ := json.Marshal(warningPayload)
		ch <- chat.StreamEvent{Type: "tool_warning", Data: string(warningJSON)}
	}

	extraSystemPrefix := composeExtraSystemPrefix(composeConfig{
		noTools:            noTools,
		progressiveActive:  selection.Progressive,
		progressiveCatalog: selection.Catalog,
	})

	// --- Plugin filter: system_prompt ---
	// In the slot model the filter operates on the dynamic per-turn prefix
	// (the only string-shaped portion of the system payload); slot content is
	// not exposed to the filter. Plugins that need to mutate static system
	// content should target the upcoming slot-aware filter (S4 backlog).
	// fctx was already declared above, right before the tool_selection
	// filter — reused here and by every other pluginHost.ApplyFilter call
	// site below.
	if s.pluginHost != nil {
		if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterSystemPrompt, extraSystemPrefix, fctx); err != nil {
			slog.Warn("chat-service: system_prompt filter error", "err", err)
		} else if fs, ok := filtered.(string); ok {
			extraSystemPrefix = fs
		}
	}

	// Filter: user_message — PII redaction, input sanitization, expansion.
	if s.pluginHost != nil && userContent != "" {
		if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterUserMessage, userContent, fctx); err != nil {
			slog.Warn("chat-service: user_message filter error", "err", err)
		} else if fs, ok := filtered.(string); ok {
			userContent = fs
		}
	}

	// Phase 0 item 21 ("Cut Modes, in full") deleted two blocks that used to
	// live here:
	//   - B1 (CW-20260428-0009)'s sessionMode resolution
	//     (session.CurrentModeID -> s.store.GetMode).
	//   - B2 (CW-20260428-0010)'s mode_suggestion SSE emit, which compared
	//     classify.ClassifyMode(userContent) against the resolved session
	//     mode and streamed a non-binding suggestion event.
	// Both classify.ClassifyMode and store.GetMode/sessions.current_mode_id
	// are gone. assembleTurnContext below no longer takes a sessionMode
	// argument.

	// --- Assemble context (slot-based) ---
	slotResult, err := s.assembleTurnContext(ctx, session, agent, tools, extraSystemPrefix, providerName, model, ch, toolsLazyHint)
	if err != nil {
		return prepareTurnResult{directive: generationTerminate}
	}
	chatMessages := slotResult.Messages
	systemPrompt := slotResult.SystemPrompt // legacy concat — for budget enforcer + telemetry

	// J11 (CW-20260426-0009): evaluate reminder triggers and inject any that
	// fired into SlotUserContext. Uses session.MessageCount as the monotonic
	// session turn counter (no new schema field needed). Must run before the
	// inspector records slots so the inspector sees the injected content.
	//
	// CW-20260501-0002: after mutating SlotUserContext we MUST refresh
	// slotResult.Blocks (consumed by slotBlocksFor → ChatRequest.SlotBlocks,
	// the actual LLM payload) and slotResult.SystemPrompt (used by budget
	// enforcer, inspector telemetry, and EmitContextAssembled). Without the
	// refresh, EvalTurn marks the reminder fired in the DB but the agent's
	// next turn never sees the <system-reminder> block — the bug from c121.
	var firedReminders []store.Reminder
	if s.reminderEngine != nil && slotResult.Window != nil {
		if fired, evalErr := s.reminderEngine.EvalTurn(sessionID, session.MessageCount); evalErr != nil {
			slog.Warn("chat-service: reminder engine eval failed", "session_id", sessionID, "err", evalErr)
		} else if len(fired) > 0 {
			firedReminders = fired
			injection := reminders.FormatInjection(fired)
			existing := ""
			if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil {
				existing = slot.Content
			}
			if existing != "" {
				slotResult.Window.SetContent(ctxpkg.SlotUserContext, existing+"\n\n"+injection)
			} else {
				slotResult.Window.SetContent(ctxpkg.SlotUserContext, injection)
			}
			// Refresh derived views so the reminder reaches the LLM. The
			// Window mutation alone only updates the in-place slot map;
			// Blocks (already Assembled) and SystemPrompt (already concat'd)
			// are stale until rebuilt.
			slotResult.Blocks = slotResult.Window.Assemble()
			slotResult.SystemPrompt = rebuildLegacySystemPrompt(slotResult.Window)
			systemPrompt = slotResult.SystemPrompt
		}
	}

	// FU-30: evaluate DB-backed agent reflexes for this turn and inject any
	// staged actions (e.g. inject_reminder) into SlotUserContext. nil-safe via
	// the engine guard inside evaluateAndInjectReflexes.
	reflexActions := s.evaluateAndInjectReflexes(ctx, session, agent, slotResult)
	if len(reflexActions) > 0 {
		systemPrompt = slotResult.SystemPrompt
	}

	// TASKS/reflex-taxonomy/04-halt-turn-synchronicity.md: a fired
	// halt_session reflex must stop THIS turn synchronously, not just stamp
	// halted_at for a future request to notice (frontend_readiness.go
	// already handles that side, untouched by this task). 03's Resolve()
	// deny_overrides guarantee (internal/agent/reflexes/resolve.go) means
	// that if a halt_session action is present here, it is the ONLY action
	// in reflexActions for this pass — no need to also handle "halt plus
	// other actions" as a real case, but this loop doesn't rely on that
	// invisibly: it scans the whole slice and aborts on the first halt it
	// finds regardless of position.
	for _, action := range reflexActions {
		if action.ActionKind != store.ReflexActionHaltSession {
			continue
		}
		reason, _ := action.Spec["reason"].(string)
		if reason == "" {
			reason = "reflex " + action.ReflexName + " fired"
		}
		// HaltHook (container.go's Executor.Halt -> store.MarkSessionHalted)
		// already ran synchronously inside evaluateAndInjectReflexes's call
		// to reflexEngine.Evaluate, above — the session row is already
		// stamped halted_at/halted_reason by the time we get here. This
		// return is the other half: stop THIS turn before it reaches the
		// LLM provider call further down in this function. Matches this
		// file's existing early-abort shape (e.g. the disabled-agent check
		// and the provider-resolution-failure branch above).
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal,
			fmt.Sprintf("Session halted: %s", reason),
			map[string]interface{}{"reflex": action.ReflexName, "reason": reason})
		return prepareTurnResult{directive: generationTerminate}
	}

	// CW-20260512-0019: surface pending subagent completions at turn
	// start. Async/api subagent runs complete after the parent's turn
	// has already ended, so their kind=subagent_result reply otherwise
	// sits unread in the inbox until the agent thinks to poll for it
	// (or never does — the c160/c271 failure mode). Injected only at
	// turn start (never mid-turn) to avoid non-determinism from a
	// subagent completing while this turn is still composing.
	if pending := s.evaluateAndInjectSubagentResults(ctx, sessionID, agentID, slotResult); len(pending) > 0 {
		systemPrompt = slotResult.SystemPrompt
	}

	// I1 (CW-20260426-0004): inspector — allocate a turn ID and record slots.
	// turnID is carried forward through the rest of generateResponse so
	// broker/tool producers can append to the same snapshot.
	var inspectorTurnID string
	if s.inspector != nil {
		inspectorTurnID = s.inspector.NextTurnID(sessionID)
		s.inspector.EnsureTurn(sessionID, inspectorTurnID)
		if slotResult.Window != nil {
			s.recordInspectorSlots(sessionID, inspectorTurnID, slotResult)
		}
		s.recordInspectorLLMMessages(sessionID, inspectorTurnID, chatMessages, systemPrompt)
		// J11 (CW-20260426-0009): record fired reminders so the I1 dev-mode panel
		// can surface them. No-op when no reminders fired this turn.
		if len(firedReminders) > 0 {
			rec := inspectsvc.RemindersRecord{}
			for _, r := range firedReminders {
				rec.FiredThisTurn = append(rec.FiredThisTurn, inspectsvc.ReminderItem{
					ID:          r.ID,
					Text:        r.Text,
					TriggerJSON: r.TriggerJSON,
					Scope:       r.Scope,
				})
			}
			s.inspector.RecordReminders(sessionID, inspectorTurnID, rec)
		}
	}

	// S3b — emit a tools-variant slot_changed envelope when the classifier
	// transitioned this turn's Tools slot between pointer / full / partial.
	emitToolSlotChangedIfNeeded(ch, slotResult.ToolCache)

	// Emit context.assembled event after tool selection and filters are applied.
	if s.pluginHost != nil {
		s.goTracked("emit.context-assembled", func(context.Context) {
			s.pluginHost.EmitContextAssembled(sessionID, len(systemPrompt), len(chatMessages), len(tools))
		})
	}

	return prepareTurnResult{
		directive: generationProceed,
		setup: &turnSetup{
			session:            session,
			agent:              agent,
			agentID:            agentID,
			constraints:        constraints,
			model:              model,
			providerName:       providerName,
			provider:           prov,
			cacheStrategy:      cacheStrategy,
			selection:          selection,
			tools:              tools,
			filterContext:      fctx,
			lazyToolCount:      lazyToolCount,
			essentialToolCount: essentialToolCount,
			lazyLoadActive:     lazyLoadActive,
			extraSystemPrefix:  extraSystemPrefix,
			userContent:        userContent,
			slotResult:         slotResult,
			chatMessages:       chatMessages,
			systemPrompt:       systemPrompt,
			inspectorTurnID:    inspectorTurnID,
		},
	}
}
