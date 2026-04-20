package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/models"
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
// no-tools warning, optional progressive discovery catalog, the native tool
// guide, and (when non-empty) the per-tool override block appended after the
// native guide. Order is significant — tool-specific overrides ship AFTER the
// general guide so they override conflicting general rules for the named tool.
//
// overrideBlock is the markdown "## Tool Overrides" section composed by the
// broker (via broker.ComposeOverrideBlock). Empty string skips the section.
func composeExtraSystemPrefix(overrideBlock string, cfg composeConfig) string {
	var b strings.Builder
	if cfg.noTools {
		b.WriteString(noToolsWarningPrefix)
	}
	if cfg.progressiveActive && cfg.progressiveCatalog != "" {
		b.WriteString(cfg.progressiveCatalog)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimLeft(nativeToolGuide, "\n"))
	if overrideBlock != "" {
		b.WriteString("\n\n")
		b.WriteString(overrideBlock)
	}
	return b.String()
}

// generateResponseTimeout is the maximum wall-clock time a single
// generateResponse goroutine is allowed to run before being cancelled.
const generateResponseTimeout = 5 * time.Minute

// nativeToolGuide is injected into every system prompt so the LLM correctly
// uses native dev/general tools.
const nativeToolGuide = `

## Native Tool Usage

Parameter shape:
- **All paths must be absolute** (start with /Users/). Never use ~ or relative paths.
- **dev_glob** takes TWO separate params: pattern (relative glob like **/*.md) and directory (absolute path like /Users/chrispian/Projects-apps/my-project). Do NOT put the full path in the pattern.
- **dev_grep** takes TWO separate params: pattern (regex) and directory (absolute path). Same rule — keep them separate.
- **dev_read/dev_write/dev_edit**: path must be absolute.
- **web_fetch**: many news/social sites block automated requests. Works best with APIs, docs sites, and raw content URLs.
- **Allowed directories**: /Users/chrispian/Projects-apps, /Users/chrispian/Projects. Files outside these paths will be rejected.

Workflow:
- **Discover before read.** Use dev_glob or dev_grep first if you aren't already sure the path exists. Running dev_read on a speculative path wastes a tool call.
- **Cache pointer pattern.** When a tool result ends with a footer like ` + "`[TRUNCATED — full result cached as tool_result://<ULID> ...]`" + `, don't re-invoke the source tool to get more. Call ` + "`fetch_tool_result`" + ` with the ULID to retrieve slices, or ` + "`search_tool_result`" + ` to regex-match across the full cached body.
- **Parallelize independent calls.** If two lookups don't depend on each other, request them in the same assistant turn — the harness executes tool blocks in parallel.
- **Stop when done.** Extra tool calls don't add trust; they just dilute the grounding.

Grounded rendering:
- ` + "`nanite_show_report`" + ` and ` + "`nanite_show_document`" + ` require a ` + "`sources`" + ` array citing the tool_use_ids whose results ground the content. Build that list as you make the calls — if you didn't fetch the data this turn, render a plain-text reply instead of an empty card.`

// generateResponse loads context, calls the provider, streams events, and saves
// the result. This is the refactored version of Engine.generateResponse — it
// uses service interfaces instead of direct Store/sync.Map access, and unifies
// the duplicated ToolClient/MCPManager execution into ToolService.Execute.
func (s *chatServiceImpl) generateResponse(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(ctx, generateResponseTimeout)
	defer cancel()

	ctx, span := feotel.StartSpan(ctx, "nanite.service.generateResponse")
	span.SetAttributes(
		attribute.String("nanite.session.id", sessionID),
		attribute.String("nanite.message.id", assistantMsgID),
	)
	defer span.End()

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
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to load session", map[string]interface{}{"raw": err.Error()})
		return
	}

	// Advisory: warn once per session when the memory embedder isn't active.
	// Fires regardless of whether memory sources get queried on this turn —
	// users see the state without having to trigger a recall.
	s.maybeEmitEmbeddingWarning(sessionID, ch)

	// --- Resolve agent ---
	agent, mode, err := s.agents.ResolveForSession(ctx, sessionID)
	if err != nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to resolve agent", map[string]interface{}{"raw": err.Error()})
		return
	}
	agentID := agent.ID

	// Block disabled agents.
	if agent.Status == "disabled" {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, fmt.Sprintf("Agent %q is disabled", agent.Name), nil)
		return
	}

	// Parse agent constraints (schema v2).
	constraints := chat.ParseAgentConstraints(agent.Constraints)

	if constraints.MaxTimeSeconds > 0 {
		agentTimeout := time.Duration(constraints.MaxTimeSeconds) * time.Second
		ctx, cancel = context.WithTimeout(ctx, agentTimeout)
		defer cancel()
	}

	// --- Load workspace ---
	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, _ = s.store.GetWorkspace(session.WorkspaceID)
	}

	// --- Resolve model ---
	model := session.Model
	if model == "" && agent.DefaultModel != "" {
		model = agent.DefaultModel
	}
	if model == "" {
		model = models.DefaultChatModel()
	}

	// --- Resolve provider ---
	providerName, prov := s.resolveProvider(sessionID, session.Provider, agent.DefaultProvider, model)
	if prov == nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeProviderError,
			fmt.Sprintf("Provider %q not available — check configuration and restart the server.", providerName),
			map[string]interface{}{"raw": fmt.Sprintf("provider %q not registered", providerName)})
		return
	}

	// Wire status callback for retry notifications and circuit breaker.
	if ap, ok := prov.(*provider.Anthropic); ok {
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

	// Apply cache hints.
	if cacheable, ok := prov.(provider.CacheableProvider); ok {
		cacheable.SetCacheHints(provider.DefaultCacheStrategy())
	}

	// --- Tool selection via ToolService (must precede slot assembly so the
	// Tools slot and the dynamic system prefix can be derived from the result). ---
	selection, err := s.tools.SelectForAgent(ctx, sessionID, agentID, userContent, session.WorkspaceID)
	if err != nil {
		slog.Warn("chat-service: tool selection failed", "err", err)
		selection = &ToolSelection{}
	}
	tools := selection.Tools

	// Build the dynamic per-turn system prefix from tool selection. This text
	// is sent verbatim in ChatRequest.SystemPrompt (it leads the slot blocks
	// in the provider payload) and varies per turn; static agent / rules /
	// session content lives in the slot blocks.
	//
	// Determine the no-tools condition up front so we can both emit the client
	// warning event (side-effect, stays here) and pass the flag to the pure
	// composeExtraSystemPrefix helper.
	noTools := false
	if !selection.Progressive && countMCPTools(tools) == 0 {
		noTools = true
		warningPayload := chat.ToolWarningPayload{
			Error: "This agent has no MCP tools configured. Responses will be text-only.",
			Level: "critical",
		}
		warningJSON, _ := json.Marshal(warningPayload)
		ch <- chat.StreamEvent{Type: "tool_warning", Data: string(warningJSON)}
	}

	// selection.OverrideBlock is composed upstream by ToolClient via
	// go-toolbroker's ComposeOverrideBlock over the FINAL tool set (post
	// permission filtering + token budget prune), so the block never mentions
	// a tool the LLM won't see. Empty string when no enricher is configured
	// or no selected tool has Hints — composeExtraSystemPrefix skips the
	// section in that case.
	extraSystemPrefix := composeExtraSystemPrefix(selection.OverrideBlock, composeConfig{
		noTools:            noTools,
		progressiveActive:  selection.Progressive,
		progressiveCatalog: selection.Catalog,
	})

	// --- Plugin filter: system_prompt ---
	// In the slot model the filter operates on the dynamic per-turn prefix
	// (the only string-shaped portion of the system payload); slot content is
	// not exposed to the filter. Plugins that need to mutate static system
	// content should target the upcoming slot-aware filter (S4 backlog).
	fctx := pluginpkg.FilterContext{SessionID: sessionID, AgentID: agentID}
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

	// --- Assemble context (slot-based) ---
	slotResult, err := s.assembleTurnContext(ctx, session, agent, mode, workspace, tools, extraSystemPrefix, ch)
	if err != nil {
		return
	}
	chatMessages := slotResult.Messages
	systemPrompt := slotResult.SystemPrompt // legacy concat — for budget enforcer + telemetry

	// S3b — emit a tools-variant slot_changed envelope when the classifier
	// transitioned this turn's Tools slot between pointer / full / partial.
	emitToolSlotChangedIfNeeded(ch, slotResult.ToolCache)

	// Emit context.assembled event after tool selection and filters are applied.
	if s.pluginHost != nil {
		safego.Go(ctx, "service.chat.emit.context-assembled", func() {
			s.pluginHost.EmitContextAssembled(sessionID, len(systemPrompt), len(chatMessages), len(tools))
		})
	}

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
		s.events.EmitSessionStart(ctx, sessionID, agent.ID, model, mode.Slug)
	}
	// Emit agent.loaded plugin event (fire-and-forget).
	if s.pluginHost != nil {
		safego.Go(ctx, "service.chat.emit.agent-loaded", func() {
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
	intent := buildIntentSignals(userContent, toolNames, false /* attachments — see recon-notes.md */)
	scopeTier, executionPattern := classify.Classify(intent)
	slog.Info("chat-service: pre-loop classification",
		"session_id", sessionID,
		"scope_tier", scopeTier.String(),
		"execution_pattern", executionPattern.String(),
		"message_token_est", intent.MessageTokenEst,
		"tools_available", intent.ToolsAvailable,
	)
	ls := newLoopState(constraints, toolNames, debugMode)
	ls.SetClassification(scopeTier, executionPattern)

	// Load per-tool cap from UserSettings.
	if us, err := s.store.GetUserSettings(); err == nil && us.ToolPerTurnCap > 0 {
		ls.limits.defaultPerToolCap = us.ToolPerTurnCap
	}

	// CW-20260418-0043 diagnostic — log effective loop config on entry.
	diagLogLoopStart(sessionID, assistantMsgID, agent.ID, ls, cap(ch))

	// --- Tool-use loop ---
	var fullContent strings.Builder
	var finalUsage *chat.Usage
	var breakdown *chat.TokenBreakdown

	for ls.iteration = 0; ; ls.iteration++ {
		// CW-20260418-0043 diagnostic.
		diagCurrentIter = ls.iteration
		diagLogIterStart(ctx, sessionID, assistantMsgID, ls.iteration, ch)

		// Check layered iteration limits.
		if stop, code, reason := ls.shouldStop(); stop {
			slog.Warn("chat-loop stopped", "reason", reason, "code", code, "session_id", sessionID, "agent", agent.ID, "iter", ls.iteration)
			// CW-20260417-0485: emit a typed `chat-loop-terminated` envelope
			// BEFORE the fallback status event so the FE (CW-20260418-0008)
			// can render a terminal pause card. Any FE that doesn't yet
			// understand the new envelope type will still see the status
			// event, preserving existing behavior.
			s.emitChatLoopTerminated(sessionID, ls, code, reason, ch)
			ch <- chat.StreamEvent{Type: "status", Content: fmt.Sprintf("Stopped: %s", reason)}
			diagLogLoopExit(sessionID, assistantMsgID, ls.iteration, "shouldStop:"+string(code), len(ls.toolCallRefs), ch)
			break
		}
		// Deadline check.
		if ctx.Err() != nil {
			slog.Warn("generateResponse context cancelled", "err", ctx.Err(), "session_id", sessionID)
			diagLogLoopExit(sessionID, assistantMsgID, ls.iteration, "ctx_cancelled:"+ctx.Err().Error(), len(ls.toolCallRefs), ch)
			ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, "Response timed out after 5 minutes. Please try again with a simpler request.", map[string]interface{}{
				"timeout": generateResponseTimeout.String(),
				"session": sessionID,
			})
			ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Response timed out after 5 minutes. Please try again with a simpler request.", map[string]interface{}{
				"timeout": generateResponseTimeout.String(),
				"session": sessionID,
			})
			return
		}

		// Token budget enforcement.
		preBudgetMsgCount := len(chatMessages)
		preBudgetToolCount := len(tools)
		var budgetErr error
		chatMessages, tools, breakdown, budgetErr = chat.EnforceTokenBudget(systemPrompt, chatMessages, tools, 0)
		if budgetErr != nil {
			slog.Warn("chat-service: token budget enforcement refused", "err", budgetErr)
			if s.events != nil {
				s.events.EmitContextBudgetExceeded(ctx, sessionID, breakdown.Total, breakdown.Ceiling)
			}
			ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Context too large after all reductions",
				map[string]interface{}{
					"total":   breakdown.Total,
					"ceiling": breakdown.Ceiling,
					"system":  breakdown.System,
					"msgs":    breakdown.Messages,
					"tools":   breakdown.Tools,
				})
			return
		}
		// Log compaction continuation if budget enforcement reduced context.
		if len(chatMessages) < preBudgetMsgCount || len(tools) < preBudgetToolCount {
			ls.continueWith(ContinueCompaction, fmt.Sprintf("budget reduced: msgs %d→%d, tools %d→%d",
				preBudgetMsgCount, len(chatMessages), preBudgetToolCount, len(tools)))
		}

		// --- Provider call ---
		provCtx, provSpan := feotel.StartSpan(ctx, "nanite.provider.call")
		provSpan.SetAttributes(
			attribute.String("nanite.model", model),
			attribute.Int("nanite.iteration", ls.iteration),
			attribute.Int("nanite.tools.count", len(tools)),
			attribute.Int("nanite.messages.count", len(chatMessages)),
			attribute.Int("nanite.tokens.total", breakdown.Total),
			attribute.Int("nanite.tokens.ceiling", breakdown.Ceiling),
		)

		// CLI session setup.
		if chat.IsCLIProvider(providerName) {
			provCtx = s.setupCLIContext(provCtx, sessionID, session, agent, mode, ch)
		}

		// --- Pre-hook: message.sending ---
		// Plugins observing "message.sending" may cancel the LLM call.
		// Data shape: {session_id, agent_id, model, messages, system_prompt_length, iteration}.
		if s.pluginHost != nil {
			cancelled := s.pluginHost.EmitPreHook("message.sending", sessionID, map[string]any{
				"agent_id":             agent.ID,
				"model":                model,
				"messages":             len(chatMessages),
				"system_prompt_length": len(systemPrompt),
				"iteration":            ls.iteration,
			})
			if cancelled {
				provSpan.End()
				slog.Info("chat-service: message.sending cancelled by plugin hook", "session_id", sessionID, "iter", ls.iteration)
				blockMsg := "Message blocked by plugin policy."
				ch <- chat.StreamEvent{Type: "delta", Content: blockMsg}
				fullContent.WriteString(blockMsg)
				diagLogLoopExit(sessionID, assistantMsgID, ls.iteration, "plugin_cancel:message.sending", len(ls.toolCallRefs), ch)
				break
			}
		}

		// Filter: context_window — plugin prunes or injects context blocks.
		if s.pluginHost != nil {
			if filtered, err := s.pluginHost.ApplyFilter(pluginpkg.FilterContextWindow, chatMessages, fctx); err != nil {
				slog.Warn("chat-service: context_window filter error", "err", err)
			} else if fm, ok := filtered.([]provider.ChatMessage); ok {
				chatMessages = fm
			}
		}

		var provCh <-chan provider.StreamEvent
		if len(tools) > 0 {
			slog.Debug("chat-service: tool-use iteration",
				"iter", ls.iteration, "tools", len(tools), "messages", len(chatMessages),
				"tokens", breakdown.Total, "ceiling", breakdown.Ceiling)
		}
		provCh, err = prov.StreamChat(provCtx, provider.ChatRequest{
			SystemPrompt: extraSystemPrefix,
			SlotBlocks:   slotBlocksFor(slotResult),
			Messages:     chatMessages,
			Model:        model,
			Tools:        tools,
		})
		if err != nil {
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
			if ctxpkg.IsCompactRecoverable(err) && ls.compactRecoverableAttempts < maxCompactRecoverableAttempts {
				// provSpan.End() is deferred until the recovery outcome is
				// known so the span is ended exactly once. On success we
				// end it cleanly before retrying; on refused recovery the
				// branch below records the error, then ends the span.
				triggerKind := compactTriggerContextOverflow
				recoveryReason := "context_overflow at stream start"
				if errors.Is(err, provider.ErrRequestExceedsRateBudget) {
					triggerKind = compactTriggerRateBudget
					recoveryReason = "rate_budget_exceeded at stream start"
				}
				ls.continueWith(ContinueRecovery, recoveryReason)
				newMsgs, newTools, ok := s.recoverFromContextOverflow(ctx, sessionID, slotResult, agent, chatMessages, tools, ch, err.Error(), triggerKind)
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
					provSpan.End()
					ls.compactRecoverableAttempts++
					// CW-20260419-0012: compaction's strip_tool_blocks stage
					// drops tool-definition context from the conversation,
					// so the LLM has to re-request tools post-compaction.
					// Reset the discovery-call counter so pre-compaction
					// calls don't eat the post-compaction budget (observed
					// in nanite-chat-debug-3.md: total_calls jumped 4→5
					// immediately after a successful compaction).
					ls.totalRequestToolsCalls = 0
					ls.consecutiveEmptyRequests = 0
					chatMessages = newMsgs
					tools = newTools
					systemPrompt = slotResult.SystemPrompt
					ls.iteration-- // the retry isn't a fresh turn
					continue
				}
				// Recovery refused (flag off / no summarizer / no stages).
				// Exhaust attempts so the same request can't loop back
				// into this branch, and preserve the normal provider-error
				// telemetry so refused recovery is as diagnosable as any
				// other provider failure (PR #68 review #2).
				ls.compactRecoverableAttempts = maxCompactRecoverableAttempts
				provSpan.RecordError(err)
				provSpan.SetStatus(codes.Error, err.Error())
				provSpan.End()
				slog.Warn("chat-service: compact-recoverable recovery refused — surfacing unrecovered error",
					"session_id", sessionID, "iter", ls.iteration, "trigger_kind", triggerKind, "err", err)
				s.store.LogEvent(sessionID, "provider_error", "error",
					fmt.Sprintf("iteration %d: %v (recovery refused, trigger=%s)", ls.iteration, err, triggerKind),
					fmt.Sprintf(`{"model":%q,"tools":%d,"messages":%d,"trigger_kind":%q}`, model, len(tools), len(chatMessages), triggerKind))
				if s.events != nil {
					s.events.EmitError(ctx, sessionID, "provider_error", err.Error())
					if chat.ClassifyError(err) == chat.ErrorCodeRateLimit {
						s.events.EmitRateLimitHit(ctx, sessionID, "anthropic", 0)
					}
				}
				if s.pluginHost != nil {
					errMsg := err.Error()
					safego.Go(ctx, "service.chat.emit.provider-error", func() {
						s.pluginHost.EmitProviderError(sessionID, providerName, model, errMsg)
					})
				}
				var msg string
				switch triggerKind {
				case compactTriggerRateBudget:
					msg = "Request exceeds the per-minute rate budget and automatic compaction could not reduce it. Reduce or split the request, or use `/clear` to remove context."
				default:
					msg = "Context is too large and automatic compaction could not reduce it. Use `/clear` or split the request."
				}
				ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, msg, map[string]interface{}{"recovery": "refused", "trigger_kind": triggerKind})
				ch <- chat.ErrorEvent(chat.ErrorCodeInternal, msg, map[string]interface{}{"recovery": "refused", "trigger_kind": triggerKind})
				return
			}
			provSpan.RecordError(err)
			provSpan.SetStatus(codes.Error, err.Error())
			provSpan.End()
			slog.Error("chat-service: provider stream error", "iter", ls.iteration, "err", err)
			s.store.LogEvent(sessionID, "provider_error", "error",
				fmt.Sprintf("iteration %d: %v", ls.iteration, err),
				fmt.Sprintf(`{"model":%q,"tools":%d,"messages":%d}`, model, len(tools), len(chatMessages)))
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
				safego.Go(ctx, "service.chat.emit.provider-error", func() {
					s.pluginHost.EmitProviderError(sessionID, providerName, model, errMsg)
				})
			}
			if ls.retryBudget > 0 {
				ls.retryBudget--
			}
			if ctxpkg.IsCompactRecoverable(err) && ls.compactRecoverableAttempts >= maxCompactRecoverableAttempts {
				// Already retried once — emit a user-facing message tailored
				// to which recoverable mode tripped. CW-20260418-0099.
				// PR #67 review #1: waiting does not help ErrRequestExceedsRateBudget
				// because the request itself is bigger than a single window.
				slog.Warn("chat-service: compaction-recoverable error persisted after retry",
					"session_id", sessionID, "iter", ls.iteration, "err", err)
				msg := "Context is still too large after compaction. Use `/clear` or split the request."
				if errors.Is(err, provider.ErrRequestExceedsRateBudget) {
					msg = "Request exceeds the per-minute rate budget even after compaction. Reduce or split the request, or use `/clear` to remove context."
				}
				ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, msg, map[string]interface{}{"recovery": "failed_after_retry"})
				ch <- chat.ErrorEvent(chat.ErrorCodeInternal, msg, map[string]interface{}{"recovery": "failed_after_retry"})
				return
			}
			errDetails := map[string]interface{}{"raw": err.Error(), "model": model, "tools": len(tools)}
			ch <- chat.ErrorEnvelopeDelta(chat.ClassifyError(err), "Provider streaming failed", errDetails)
			ch <- chat.ErrorEvent(chat.ClassifyError(err), "Provider streaming failed", errDetails)
			return
		}

		// --- Consume provider stream ---
		var turnContent strings.Builder
		var toolUseBlocks []provider.ToolUseBlock
		var stopReason string
		var lastPTYToolPending string
		// T9 — set by the mid-stream overflow handler when the outer loop
		// should retry with a compacted request instead of terminating.
		contextOverflowRecovered := false

		// CW-20260418-0043 diagnostic — track provider stream duration + event count.
		provStreamStart := time.Now()
		diagProvEventCount := 0

	streamLoop:
		for evt := range provCh {
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
				fullContent.WriteString(evt.Content)
				// CW-20260418-0043 diagnostic — watchdog on the hot delta
				// send. No-op (returns a nil-op stop func) unless
				// NANITE_CHAT_LOOP_DIAG=1 so production is zero-cost.
				stopDiag := diagWatchChSend(ctx, "streamLoop.delta", ch, sessionID, assistantMsgID, ls.iteration, "delta")
				ch <- chat.StreamEvent{Type: "delta", Content: evt.Content}
				stopDiag()

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
					if finalUsage == nil {
						finalUsage = &chat.Usage{}
					}
					if evt.Usage.InputTokens > 0 {
						finalUsage.InputTokens += evt.Usage.InputTokens
					}
					if evt.Usage.OutputTokens > 0 {
						finalUsage.OutputTokens += evt.Usage.OutputTokens
					}
					if evt.Usage.CacheCreationTokens > 0 {
						finalUsage.CacheCreationTokens += evt.Usage.CacheCreationTokens
					}
					if evt.Usage.CacheReadTokens > 0 {
						finalUsage.CacheReadTokens += evt.Usage.CacheReadTokens
					}
					if evt.Usage.StopReason != "" {
						stopReason = evt.Usage.StopReason
						finalUsage.StopReason = evt.Usage.StopReason
					}
				}

			case "error":
				// T9 — mid-stream overflow recovery: same pattern as the
				// stream-start case above. Streaming surgery isn't attempted
				// (plan non-goal) — we abort the in-flight stream, recover,
				// and the outer loop retries with a rebuilt request.
				if ctxpkg.IsContextOverflowMessage(evt.Error) && ls.compactRecoverableAttempts < maxCompactRecoverableAttempts {
					ls.compactRecoverableAttempts++
					ls.continueWith(ContinueRecovery, "context_overflow mid-stream")
					if newMsgs, newTools, ok := s.recoverFromContextOverflow(ctx, sessionID, slotResult, agent, chatMessages, tools, ch, evt.Error, compactTriggerContextOverflow); ok {
						// Mirror the stream-start recovery path: strip_tool_blocks
						// drops tool-definition context, so post-compaction the
						// LLM has to re-request tools. Reset the discovery-call
						// counters so pre-compaction calls don't eat the
						// post-compaction budget (CW-20260419-0012).
						ls.totalRequestToolsCalls = 0
						ls.consecutiveEmptyRequests = 0
						chatMessages = newMsgs
						tools = newTools
						systemPrompt = slotResult.SystemPrompt
						ls.iteration-- // the retry isn't a fresh turn
						contextOverflowRecovered = true
						// Labeled break — without the label, `break` would exit
						// the switch only, and the stream loop would keep
						// draining events. Copilot review #3095050032.
						break streamLoop
					}
				}
				errDetails := map[string]interface{}{"raw": evt.Error, "model": model}
				if ctxpkg.IsContextOverflowMessage(evt.Error) && ls.compactRecoverableAttempts >= maxCompactRecoverableAttempts {
					msg := "Context is still too large after compaction. Use `/clear` or split the request."
					errDetails["recovery"] = "failed_after_retry"
					ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, msg, errDetails)
					ch <- chat.ErrorEvent(chat.ErrorCodeInternal, msg, errDetails)
					return
				}
				ch <- chat.ErrorEnvelopeDelta(chat.ClassifyError(fmt.Errorf("%s", evt.Error)), "Streaming error from provider", errDetails)
				ch <- chat.ErrorEvent(chat.ClassifyError(fmt.Errorf("%s", evt.Error)), "Streaming error from provider", errDetails)
				return

			case "session_id":
				if evt.SessionID != "" {
					s.persistCLISessionID(sessionID, session, evt.SessionID)
				}

			case "done":
				// handled below
			}
		}

		// CW-20260418-0043 diagnostic — provider stream closed.
		diagLogProviderStream(sessionID, assistantMsgID, ls.iteration,
			time.Since(provStreamStart), diagProvEventCount, stopReason, len(toolUseBlocks))

		// T9 — the mid-stream overflow handler broke out of the stream loop so
		// the outer loop can retry with a compacted request. Skip the
		// post-stream processing (no content produced this attempt) and
		// continue.
		if contextOverflowRecovered {
			provSpan.End()
			continue
		}

		// Resolve remaining PTY tool presence.
		if lastPTYToolPending != "" && chat.IsCLIProvider(providerName) {
			s.streams.BroadcastPresence(chat.PresenceEvent{
				Type: "tool_resolved", SessionID: sessionID, AgentID: agent.ID,
				ToolName: lastPTYToolPending, Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		provSpan.End()

		// If no tool use, we are done.
		if stopReason != "tool_use" || len(toolUseBlocks) == 0 {
			if stopReason == "max_tokens" {
				ls.wasTruncated = true
				slog.Warn("chat-service: response truncated by max_tokens", "iter", ls.iteration)
				ch <- chat.StreamEvent{Type: "status", Content: "Response was cut short due to length limits. Some content may be missing."}
				s.store.LogEvent(sessionID, "max_tokens_truncation", "warning",
					fmt.Sprintf("iteration %d: response truncated by max_tokens", ls.iteration),
					fmt.Sprintf(`{"model":%q,"iteration":%d}`, model, ls.iteration))
				ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, "Response truncated — hit output token limit", map[string]interface{}{
					"stop_reason": "max_tokens", "iteration": ls.iteration, "model": model,
				})
			}
			diagLogLoopExit(sessionID, assistantMsgID, ls.iteration, "done:stop_reason="+stopReason, len(ls.toolCallRefs), ch)
			break
		}

		// --- Build assistant message with tool_use blocks ---
		var assistantBlocks []provider.ContentBlock
		if text := turnContent.String(); text != "" {
			assistantBlocks = append(assistantBlocks, provider.ContentBlock{Type: "text", Text: text})
		}
		for _, tu := range toolUseBlocks {
			input := tu.Input
			if input == nil {
				input = map[string]any{}
			}
			assistantBlocks = append(assistantBlocks, provider.ContentBlock{
				Type: "tool_use", ID: tu.ID, Name: tu.Name, Input: &input,
			})
		}
		chatMessages = append(chatMessages, provider.ChatMessage{
			Role: "assistant", ContentBlocks: assistantBlocks,
		})

		// --- Execute tools (pre-check → parallel/serial → post-process) ---

		// Handle request_tools meta-tool calls first.
		var resultBlocks []provider.ContentBlock
		var regularTools []provider.ToolUseBlock
		for _, tu := range toolUseBlocks {
			if tu.Name == "request_tools" && selection.Progressive {
				resultBlocks, ls.toolCallRefs = s.handleRequestTools(
					ctx, tu, ch, tools, ls.loadedTools,
					&ls.consecutiveEmptyRequests, &ls.totalRequestToolsCalls, ls.maxRequestToolsCalls,
					resultBlocks, ls.toolCallRefs,
				)
			} else {
				regularTools = append(regularTools, tu)
			}
		}

		// Pre-check regular tools: permission, blocked, concurrency safety.
		plans := s.preCheckTools(ctx, sessionID, agentID, regularTools, ls, ch, selection, tools)

		// Execute tools: concurrent-safe in parallel, serial one at a time.
		execResults := s.executeToolBatch(ctx, plans, ls, agentID, ch, sessionID)

		// Post-process: stuck loop detection, truncation, envelopes, artifacts.
		newBlocks, newRefs := s.postProcessToolResults(ctx, plans, execResults, ls, ch, sessionID, agentID, assistantMsgID)
		resultBlocks = append(resultBlocks, newBlocks...)
		ls.toolCallRefs = append(ls.toolCallRefs, newRefs...)

		// Append tool results as user message.
		chatMessages = append(chatMessages, provider.ChatMessage{
			Role: "user", ContentBlocks: resultBlocks,
		})

		// Update activity timestamp.
		ls.touchActivity()

		// Log continuation site: tool results ready, feeding back to provider.
		reason := fmt.Sprintf("%d tools executed", len(toolUseBlocks))
		ls.continueWith(ContinueToolResults, reason)

		// Capture turn snapshot for debugging.
		if ls.debugMode {
			var snapshotTools []ToolCallSnapshot
			for _, r := range execResults {
				snapshotTools = append(snapshotTools, ToolCallSnapshot{
					Name:       r.ref.Name,
					DurationMs: float64(r.duration.Milliseconds()),
					Success:    !r.isError,
				})
			}
			tokensUsed := 0
			if breakdown != nil {
				tokensUsed = breakdown.Total
			}
			ls.captureSnapshotWithTools(ContinueToolResults, reason, tokensUsed, len(chatMessages), snapshotTools)
		}

		// Future continuation sites (wired when features are implemented):
		// - ContinueAgentReturn:  sub-agent or sideloaded task returned results
		// - ContinueHookModified: plugin hook modified state (injected context, changed tools)
		// - ContinueModeChange:   mode switch mid-turn (plan mode, worktree, agent switch)

		// Brief pause between iterations.
		if ls.iteration > 0 {
			time.Sleep(1 * time.Second)
		}

		// Check circuit breaker.
		if ap, ok := prov.(*provider.Anthropic); ok && ap.CircuitBreaker != nil && ap.CircuitBreaker.IsOpen() {
			slog.Warn("chat-service: circuit breaker open, stopping", "iter", ls.iteration)
			if s.events != nil {
				s.events.EmitCircuitBreakerTripped(ctx, sessionID, "anthropic")
			}
			ch <- chat.StreamEvent{
				Type:    "circuit_open",
				Content: "Provider rate limited. Tool-use loop stopped. Would you like to retry?",
			}
			diagLogLoopExit(sessionID, assistantMsgID, ls.iteration, "circuit_open", len(ls.toolCallRefs), ch)
			break
		}
	}

	// --- Post-processing ---
	responseContent := fullContent.String()
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

	// Inject pending envelopes.
	for _, env := range ls.pendingEnvelopes {
		envelopeBlock := "\n\n```nanite-envelope\n" + env + "\n```"
		responseContent += envelopeBlock
		ch <- chat.StreamEvent{Type: "delta", Content: envelopeBlock}
	}

	// Parse envelopes.
	envelopes, cleanContent, envErrors := chat.ParseEnvelopes(responseContent)
	for _, envErr := range envErrors {
		slog.Warn("chat-service: envelope error", "reason", envErr.Reason, "content", chat.TruncateStr(envErr.Raw, 200))
		s.store.LogEvent(sessionID, "envelope_error", "warning",
			envErr.Reason, fmt.Sprintf(`{"raw":%q}`, chat.TruncateStr(envErr.Raw, 500)))
	}

	// CLI envelope retry.
	if len(envErrors) > 0 && chat.IsCLIProvider(providerName) {
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
		envRefs = append(envRefs, chat.EnvelopeRef{Type: env.Type, Data: json.RawMessage(innerData)})
		// Emit envelope.rendered plugin event for each envelope attached to the response.
		if s.pluginHost != nil {
			envType := env.Type
			envData := env.Data
			safego.Go(ctx, "service.chat.emit.envelope-rendered", func() {
				s.pluginHost.EmitEnvelopeRendered(sessionID, envType, envData)
			})
		}
	}

	// Determine tier.
	tier := "default"
	if len(ls.toolCallRefs) > 0 {
		tier = "tool"
	}
	hasError := false
	for _, e := range envRefs {
		if e.Type == "error-report" {
			hasError = true
			break
		}
	}

	// Structured message.
	structured := chat.WrapResponse(cleanContent, tier, ls.toolCallRefs, envRefs, ls.wasTruncated, hasError)
	chat.LogStructuredWarnings(structured)
	structuredJSON := structured.MarshalContent()

	// Save assistant message.
	assistantMsg := &store.Message{
		ID: assistantMsgID, SessionID: sessionID, AgentID: agent.ID,
		Role: "assistant", Content: structuredJSON, Envelope: envelopeJSON,
	}
	if err := s.store.CreateMessage(assistantMsg); err != nil {
		slog.Error("chat-service: failed to save assistant message", "err", err)
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to save response", map[string]interface{}{"raw": err.Error()})
		return
	}

	// PruneAfterTurn retired in Phase 3 S3a — slot compaction supersedes.
	// The ContextService interface method remains for one release so out-of-tree
	// callers don't break; removal is a follow-up.

	// Record token usage.
	if finalUsage != nil && (finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0) {
		toolInputTokens := 0
		if breakdown != nil {
			toolInputTokens = breakdown.Tools
		}
		if err := s.store.RecordUsage(sessionID, assistantMsgID, model,
			finalUsage.InputTokens, finalUsage.OutputTokens, toolInputTokens,
			finalUsage.CacheCreationTokens, finalUsage.CacheReadTokens); err != nil {
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
		AgentID: agent.ID, AgentSlug: agent.Slug, Mode: mode.Slug,
		DurationMs: time.Since(startTime).Milliseconds(),
		ContextMessages: len(chatMessages), ToolIterations: ls.iteration,
		ToolCalls: len(ls.toolCallRefs),
	}
	if breakdown != nil {
		metrics.ContextTokens = breakdown.Total
	}
	if finalUsage != nil {
		metrics.InputTokens = finalUsage.InputTokens
		metrics.OutputTokens = finalUsage.OutputTokens
		metrics.CacheCreationTokens = finalUsage.CacheCreationTokens
		metrics.CacheReadTokens = finalUsage.CacheReadTokens
		metrics.StopReason = finalUsage.StopReason
	}
	// Attach debug snapshots if captured.
	if ls.debugMode && len(ls.snapshots) > 0 {
		if snapJSON, err := json.Marshal(ls.snapshots); err == nil {
			metrics.DebugSnapshots = string(snapJSON)
		}
	}
	if err := s.store.RecordExecutionMetrics(metrics); err != nil {
		slog.Warn("chat-service: failed to record execution metrics", "err", err)
	}

	// Stream end.
	ch <- chat.StreamEvent{Type: "stream_end", MessageID: assistantMsgID, Usage: finalUsage, AgentID: agent.ID, Envelope: envelopeJSON}

	// Post-response events.
	if s.events != nil && finalUsage != nil {
		s.events.EmitResponseComplete(ctx, sessionID, agent.ID, model, finalUsage.InputTokens, finalUsage.OutputTokens)
	}
	if s.events != nil {
		elapsed := time.Since(startTime).Milliseconds()
		contentPreview := envelopeJSON
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500]
		}
		s.events.EmitMessageReceived(ctx, sessionID, assistantMsgID, contentPreview, elapsed)
	}

	// Auto-title and auto-tags.
	if session.Title == "" {
		safego.Go(ctx, "service.chat.autoTitle", func() {
			s.autoTitle(sessionID, userContent)
		})
	}
	safego.Go(ctx, "service.chat.autoTags", func() {
		s.autoTags(sessionID)
	})
}

// ---------------------------------------------------------------------------
// Private helper methods
// ---------------------------------------------------------------------------

// Compact-recovery trigger kinds. CW-20260418-0099: the pipeline serves two
// distinct failure modes that share the same remedy; the kind is threaded
// through so logs / slot_changed reasoning reflect the actual trigger
// instead of hardcoding "context-overflow" for rate-budget failures too.
const (
	compactTriggerContextOverflow = "context_overflow"
	compactTriggerRateBudget      = "rate_budget_exceeded"
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
	chatMessages []provider.ChatMessage,
	tools []provider.ToolDefinition,
	ch chan chat.StreamEvent,
	triggerMsg string,
	triggerKind string,
) ([]provider.ChatMessage, []provider.ToolDefinition, bool) {
	if triggerKind == "" {
		triggerKind = compactTriggerContextOverflow
	}
	if result == nil || result.Window == nil {
		return chatMessages, tools, false
	}
	settings, _ := s.store.GetUserSettings()
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

	pipeline := &ctxpkg.CompactionPipeline{
		Window:               result.Window,
		Estimator:            ctxpkg.DefaultEstimator{},
		Summarizer:           summarizer,
		Mode:                 classifyModeFromAgentTags(agent),
		ConversationMessages: chatMessages,
	}

	tokensBefore := result.Window.UsedTokens()

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
func (s *chatServiceImpl) assembleTurnContext(
	ctx context.Context,
	session *store.Session,
	agent *store.AgentProfile,
	mode *store.AgentMode,
	workspace *store.Workspace,
	tools []provider.ToolDefinition,
	extraSystemPrefix string,
	ch chan chat.StreamEvent,
) (*SlotAssemblyResult, error) {
	windowSize := s.contextWindowSize()
	result, err := s.context.AssembleSlots(ctx, session, agent, mode, workspace, tools, extraSystemPrefix, windowSize)
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

// slotBlocksFor projects the context package's SlotBlock onto the provider
// package's mirror type. The two are kept separate so the provider module has
// no dependency on the host's context package.
//
// CW-20260419-0007: every SlotBlock is forwarded with Changed=true
// regardless of the context-side tracking. go-providers v0.2.1's
// buildSystemFromRequest emits one `cache_control: ephemeral` marker per
// unchanged slot, but Anthropic caps total cache_control markers per
// request at 4 — and DefaultCacheStrategy already consumes all 4 (system
// + tools + 2 recent_message). Any unchanged slot pushes us past the cap
// and the request fails with
// `"A maximum of 4 blocks with cache_control may be provided. Found N"`.
// Forcing Changed=true opts every slot block out of the cache_control
// path, effectively disabling slot-level prompt caching until the
// go-providers side learns to budget markers. The rest of the
// DefaultCacheStrategy (system / tools / recent messages) still applies.
func slotBlocksFor(result *SlotAssemblyResult) []provider.SlotBlock {
	if result == nil || len(result.Blocks) == 0 {
		return nil
	}
	out := make([]provider.SlotBlock, 0, len(result.Blocks))
	for _, b := range result.Blocks {
		if b.Content == "" {
			continue
		}
		out = append(out, provider.SlotBlock{
			Name:     b.SlotName,
			Content:  b.Content,
			CacheKey: b.CacheKey,
			Changed:  true, // see function-level comment — CW-20260419-0007
		})
	}
	return out
}

// contextWindowSize returns the user-configured slot budget source — the
// provider context window in tokens. Falls back to the slot package default
// when unset or unavailable.
func (s *chatServiceImpl) contextWindowSize() int {
	if s.store == nil {
		return 0
	}
	settings, err := s.store.GetUserSettings()
	if err != nil || settings == nil || settings.ContextWindowTokens <= 0 {
		return 0
	}
	return settings.ContextWindowTokens
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
	chatMessages []provider.ChatMessage,
	tools []provider.ToolDefinition,
	ch chan chat.StreamEvent,
) ([]provider.ChatMessage, []provider.ToolDefinition) {
	if result == nil || result.Window == nil || !result.NeedsCompaction {
		return chatMessages, tools
	}

	settings, _ := s.store.GetUserSettings()
	summarizer := s.buildSummarizer(settings)
	pipeline := &ctxpkg.CompactionPipeline{
		Window:               result.Window,
		Estimator:            ctxpkg.DefaultEstimator{},
		Summarizer:           summarizer,
		Mode:                 classifyModeFromAgentTags(agent),
		ConversationMessages: chatMessages,
	}

	tokensBefore := result.Window.UsedTokens()
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
	if s.pluginHost != nil {
		stages := append([]string(nil), cr.StagesApplied...)
		safego.Go(ctx, "service.chat.emit.context-compacted", func() {
			s.pluginHost.EmitContextCompacted(sessionID, tokensBefore-tokensAfter, stages)
		})
	}

	return chatMessages, tools
}

// buildSummarizer is the chat-service-bound form of BuildSummarizer.
func (s *chatServiceImpl) buildSummarizer(settings *store.UserSettings) ctxpkg.Summarizer {
	return BuildSummarizer(s.providers, settings)
}

// BuildSummarizer resolves the provider+model used to summarize compacted
// conversation spans. UserSettings can override; an empty/missing override
// falls back to the configured default chat provider so summarization always
// uses a known reachable model. Returns nil when the chosen provider is not
// registered — Stage 2 of the compaction pipeline is nil-safe and skips.
func BuildSummarizer(registry *provider.Registry, settings *store.UserSettings) ctxpkg.Summarizer {
	provName := ""
	model := ""
	if settings != nil {
		provName = settings.SummarizerProvider
		model = settings.SummarizerModel
	}
	if provName == "" {
		provName = models.DefaultProvider()
	}
	if model == "" {
		model = models.DefaultChatModel()
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
// inspecting the agent's tags. Default is "general" when no recognised tag is
// present. Exported so out-of-package callers (e.g. /compact handler) reuse the
// same classification heuristic as the chat hot path.
func ClassifyCompactionMode(agent *store.AgentProfile) string {
	return classifyModeFromAgentTags(agent)
}

// buildIntentSignals assembles the pre-loop signal struct consumed by
// classify.Classify. Pulled out as a helper for unit-testability; the
// full generation wires this plus the Classify call at the pre-loop
// boundary of generateResponse (CW-20260420-0013, P3).
func buildIntentSignals(userContent string, tools []string, hasAttachments bool) classify.IntentSignals {
	return classify.IntentSignals{
		Message: userContent,
		// NOTE: deliberately inline rather than internal/context.DefaultEstimator —
		// the floor-of-1 behavior that DefaultEstimator adds is irrelevant here because
		// classifyTier branches on est>0 explicitly. See planning/agent-platform/2026-04-19-p3-scopetier-plan.md Task 4.
		MessageTokenEst: len(userContent) / 4,
		HasAttachments:  hasAttachments,
		ToolsAvailable:  len(tools),
	}
}

// classifyModeFromAgentTags maps agent tags to a CompactionPipeline mode.
// Default is "general" when no recognised tag is present.
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
func (s *chatServiceImpl) handleRequestTools(
	ctx context.Context,
	tu provider.ToolUseBlock,
	ch chan chat.StreamEvent,
	tools []provider.ToolDefinition,
	loadedTools map[string]bool,
	consecutiveEmpty *int,
	totalCalls *int,
	maxCalls int,
	resultBlocks []provider.ContentBlock,
	toolCallRefs []chat.ToolCallRef,
) ([]provider.ContentBlock, []chat.ToolCallRef) {
	ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID}
	*totalCalls++

	// Hard cap. CW-20260419-0012: friendlier halt message that actually
	// helps the LLM decide what to do instead of just telling it "no."
	// It lists what IS loaded (so the LLM can inventory), prompts goal
	// reflection, and offers two escape paths (use what's there OR ask
	// user) — rather than the previous "do NOT call request_tools again"
	// which read as a punishment. Real conceptual rework lives in
	// CW-20260419-0011; this is the low-lift polish.
	if *totalCalls > maxCalls || *consecutiveEmpty >= 2 {
		reason := fmt.Sprintf("consecutive_empty=%d, total_calls=%d", *consecutiveEmpty, *totalCalls)
		loadedList := make([]string, 0, len(loadedTools))
		for name := range loadedTools {
			loadedList = append(loadedList, name)
		}
		sort.Strings(loadedList)
		rtResult := fmt.Sprintf(
			"Tool discovery cap reached (%s). The tools currently loaded for this turn are:\n\n  %s\n\n"+
				"Before asking for more tools, consider:\n"+
				"1. What is the underlying goal the user asked you to accomplish? State it in one sentence.\n"+
				"2. Can any tool above make partial progress toward that goal? Try it, then re-evaluate.\n"+
				"3. If no loaded tool fits, describe the specific capability you need to the user so they can guide you or load more tools manually.\n\n"+
				"Further request_tools calls this turn will be ignored; use a loaded tool or respond to the user.",
			reason, strings.Join(loadedList, ", "),
		)
		slog.Warn("chat-service: request_tools halted", "reason", reason)
		ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: rtResult}
		resultBlocks = append(resultBlocks, provider.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: rtResult,
		})
		toolCallRefs = append(toolCallRefs, chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})
		return resultBlocks, toolCallRefs
	}

	newTools, rtResult, _ := s.tools.HandleRequestTools(ctx, tu.Input)

	var loaded []string
	for _, nt := range newTools {
		if !loadedTools[nt.Name] {
			loadedTools[nt.Name] = true
			tools = append(tools, nt)
			loaded = append(loaded, nt.Name)
		}
	}

	if len(loaded) == 0 {
		*consecutiveEmpty++
		if *consecutiveEmpty == 1 {
			rtResult += "\n\nNo new tools were loaded for this request. If you believe the right tools exist, try rephrasing your intent with different keywords. Otherwise, proceed with the tools you have."
		}
	} else {
		*consecutiveEmpty = 0
	}

	slog.Info("chat-service: request_tools loaded", "count", len(loaded), "consecutive_empty", *consecutiveEmpty, "tools", loaded)

	ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: rtResult}
	resultBlocks = append(resultBlocks, provider.ContentBlock{
		Type: "tool_result", ToolUseID: tu.ID, Content: rtResult,
	})
	toolCallRefs = append(toolCallRefs, chat.ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})

	return resultBlocks, toolCallRefs
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
func captureEnvelopeData(result, toolName string, pending []string) []string {
	if eStart := strings.Index(result, "<!--ENVELOPE_DATA:"); eStart >= 0 {
		tail := result[eStart+len("<!--ENVELOPE_DATA:"):]
		if eEnd := strings.Index(tail, ":ENVELOPE_DATA-->"); eEnd >= 0 {
			payload := tail[:eEnd]
			if strings.HasSuffix(toolName, "__search_kb") {
				if env := chat.BuildKBEnvelope(payload); env != "" {
					pending = append(pending, env)
				}
			} else {
				pending = append(pending, payload)
			}
		}
	}
	return pending
}

// setupCLIContext prepares the context for CLI provider calls (sandbox, resume, process tracking).
func (s *chatServiceImpl) setupCLIContext(ctx context.Context, sessionID string, session *store.Session, agent *store.AgentProfile, mode *store.AgentMode, ch chan chat.StreamEvent) context.Context {
	// Concurrency limit.
	if s.processTracker != nil && s.processTracker.AtCapacity() {
		ch <- chat.ErrorEvent(chat.ErrorCodeProviderError,
			fmt.Sprintf("CLI process limit reached (%d). Close other CLI sessions or wait for them to finish.", s.processTracker.MaxProcesses),
			map[string]interface{}{"raw": "max concurrent CLI processes exceeded"})
		return ctx
	}

	// Sandbox.
	if sbDir, err := sandbox.Dir(sessionID); err != nil {
		slog.Warn("chat-service: sandbox dir error", "err", err)
	} else {
		if err := sandbox.Populate(sbDir, agent, mode, sandbox.PopulateOpts{
			SessionID: sessionID,
			DBPath:    "", // Store interface doesn't expose DBPath; will be wired in container
		}); err != nil {
			slog.Warn("chat-service: sandbox populate error", "err", err)
		}
		ctx = provider.WithSandboxDir(ctx, sbDir)
	}

	// CLI session ID for --resume.
	var meta map[string]any
	if err := json.Unmarshal([]byte(session.Metadata), &meta); err == nil {
		if cliSID, ok := meta["cli_session_id"].(string); ok && cliSID != "" {
			ctx = provider.WithCLISessionID(ctx, cliSID)
		}
	}

	// Process tracker callbacks.
	if s.processTracker != nil {
		sid := sessionID
		ctx = provider.WithProcessCallback(ctx, func(proc *os.Process, started bool) {
			if started {
				s.processTracker.Track(sid, proc)
			} else {
				s.processTracker.Untrack(sid, proc)
			}
		})
		ctx = provider.WithActivityCallback(ctx, func(pid int) {
			s.processTracker.Touch(sid, pid)
			s.streams.ThrottledCLIPresence(sid)
		})
	}

	return ctx
}

// persistCLISessionID saves the CLI session ID to session metadata.
func (s *chatServiceImpl) persistCLISessionID(sessionID string, session *store.Session, cliSessionID string) {
	var meta map[string]any
	if err := json.Unmarshal([]byte(session.Metadata), &meta); err != nil || meta == nil {
		meta = make(map[string]any)
	}
	meta["cli_session_id"] = cliSessionID
	metaJSON, _ := json.Marshal(meta)
	session.Metadata = string(metaJSON)
	if err := s.store.UpdateSessionMetadata(sessionID, session.Metadata); err != nil {
		slog.Warn("chat-service: failed to persist CLI session ID", "err", err)
	}
}

// maybeCreateAutoArtifact checks if a tool call wrote a file and auto-creates
// an artifact record.
func (s *chatServiceImpl) maybeCreateAutoArtifact(sessionID, messageID, agentID string, tu provider.ToolUseBlock) {
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
	if err := s.store.CreateArtifact(artifact); err != nil {
		slog.Warn("chat-service: auto-artifact creation failed", "path", filePath, "err", err)
	}
}

// retryEnvelopeCorrection sends a correction prompt for malformed envelopes.
func (s *chatServiceImpl) retryEnvelopeCorrection(
	ctx context.Context,
	sessionID string,
	session *store.Session,
	prov provider.Provider,
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
	s.store.LogEvent(sessionID, "envelope_retry", "info",
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

	correctionMsgs := []provider.ChatMessage{{Role: "user", Content: correction}}
	retryCh, err := prov.StreamChat(retryCtx, provider.ChatRequest{Messages: correctionMsgs, Model: model})
	if err != nil {
		slog.Warn("chat-service: envelope retry stream error", "err", err)
		return nil
	}

	var retryContent strings.Builder
	for evt := range retryCh {
		switch evt.Type {
		case "delta":
			retryContent.WriteString(evt.Content)
			ch <- chat.StreamEvent{Type: "delta", Content: evt.Content}
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

// autoTitle generates a title for a session from the first user message.
func (s *chatServiceImpl) autoTitle(sessionID, userContent string) {
	prov, ok := s.providers.Get(s.utilityProvider)
	if !ok {
		return
	}

	prompt := "Generate a concise 3-5 word title for this conversation. Respond with ONLY the title, no quotes or punctuation."
	msgs := []provider.ChatMessage{
		{Role: "user", Content: fmt.Sprintf("First message: %s", userContent)},
	}

	start := time.Now()
	title, err := prov.Complete(context.Background(), provider.ChatRequest{SystemPrompt: prompt, Messages: msgs, Model: s.utilityModel})
	duration := time.Since(start)
	s.recordUtilityMetrics(sessionID, "autoTitle", duration, err)

	if err != nil {
		slog.Warn("chat-service: auto-title failed", "err", err)
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}

	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return
	}
	sess.Title = title
	_ = s.store.UpdateSession(sess)
}

// autoTags generates tags for a session based on recent messages.
func (s *chatServiceImpl) autoTags(sessionID string) {
	prov, ok := s.providers.Get(s.utilityProvider)
	if !ok {
		return
	}

	msgs, err := s.store.ListMessages(sessionID, 10)
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
	tagMsgs := []provider.ChatMessage{{Role: "user", Content: sb.String()}}

	start := time.Now()
	raw, err := prov.Complete(context.Background(), provider.ChatRequest{SystemPrompt: prompt, Messages: tagMsgs, Model: s.utilityModel})
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
	_ = s.store.UpdateSessionTags(sessionID, string(tagsJSON))
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
	_ = s.store.RecordExecutionMetrics(m)
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
	settings, err := s.store.GetUserSettings()
	if err != nil {
		return false
	}
	return settings.DeveloperMode
}
