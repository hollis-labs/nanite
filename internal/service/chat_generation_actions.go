package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/effort"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	"github.com/hollis-labs/nanite/internal/messaging"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
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
	selection := setup.selection
	prov := setup.provider
	// --- Build assistant message with tool_use blocks ---
	var assistantBlocks []llmtypes.ContentBlock
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

	// --- Execute tools (pre-check → parallel/serial → post-process) ---

	// Handle request_tools meta-tool calls first.
	var resultBlocks []llmtypes.ContentBlock
	var regularTools []llmtypes.ToolUseBlock
	for _, tu := range turn.toolUseBlocks {
		if tu.Name == "request_tools" && selection.Progressive {
			resultBlocks, run.loop.toolCallRefs = s.handleRequestTools(
				ctx, tu, ch, run.tools, run.loop.loadedTools,
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
			startCtx, sessionID, messaging.EventPTYTurnStart, providerName, startPayload)
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

type settleToolTurnResult struct {
	directive generationDirective
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
			ch <- chat.ErrorEnvelopeDelta(chat.ClassifyError(fmt.Errorf("%s", evt.Error)), "Streaming error from provider", errDetails)
			ch <- chat.ErrorEvent(chat.ClassifyError(fmt.Errorf("%s", evt.Error)), "Streaming error from provider", errDetails)
			// Suppression already checked at line above; use the pre-classified
			// broker-notify variant. CW-20260512-0001, CW-20260512-0002.
			s.persistPartialAssistantAndNotifyBrokerPreClassified(ctx, sessionID, assistantMsgID, agentID, run.fullContent.String(), providerName, agent.Slug, fmt.Errorf("%s", evt.Error)) // CW-20260419-0019, CW-20260512-0001, CW-20260512-0002
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

		case "thinking":
			// F3 (CW-20260420-0023): interleaved thinking block. Persist
			// signed block for round-trip; emit to FE as PhaseThinking.
			if evt.ThinkingBlock != nil {
				run.thinkingBlocks = append(run.thinkingBlocks, *evt.ThinkingBlock)
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

	// If no tool use, we are done.
	if stopReason != "tool_use" || len(toolUseBlocks) == 0 {
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
	// the stored message is not empty. Old behaviour preserved for those paths.
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

	normalizeToolInputSchemas(tools)

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
