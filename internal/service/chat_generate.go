package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	feotel "github.com/hollis-labs/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/store"
)

// generateResponseTimeout is the maximum wall-clock time a single
// generateResponse goroutine is allowed to run before being cancelled.
const generateResponseTimeout = 5 * time.Minute

// nativeToolGuide is injected into every system prompt so the LLM correctly
// uses native dev/general tools.
const nativeToolGuide = `

## Native Tool Usage

When using file and search tools, follow these rules:

- **All paths must be absolute** (start with /Users/). Never use ~ or relative paths.
- **dev_glob requires TWO separate params**: pattern (relative glob like **/*.md) and directory (absolute path like /Users/chrispian/Projects-apps/mentat). Do NOT put the full path in the pattern.
- **dev_grep requires TWO separate params**: pattern (regex) and directory (absolute path). Same rule — keep them separate.
- **dev_read/dev_write/dev_edit**: path must be absolute.
- **web_fetch**: many news/social sites block automated requests. Works best with APIs, docs sites, and raw content URLs.
- **Allowed directories**: /Users/chrispian/Projects-apps, /Users/chrispian/Projects. Files outside these paths will be rejected.`

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

	defer func() {
		close(ch)
		s.streams.CloseStream(assistantMsgID)

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

	// --- Assemble context ---
	systemPrompt, chatMessages, err := s.context.AssembleContext(ctx, session, agent, mode, workspace)
	if err != nil {
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to assemble context", map[string]interface{}{"raw": err.Error()})
		return
	}

	// --- Resolve model ---
	model := session.Model
	if model == "" && agent.DefaultModel != "" {
		model = agent.DefaultModel
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// --- Resolve provider ---
	providerName, prov := s.resolveProvider(session.Provider, agent.DefaultProvider, model)
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

	// --- Tool selection via ToolService ---
	selection, err := s.tools.SelectForAgent(ctx, sessionID, agentID, userContent, session.WorkspaceID)
	if err != nil {
		log.Printf("chat-service: tool selection failed: %v", err)
		selection = &ToolSelection{}
	}
	tools := selection.Tools

	// Warn if no MCP tools and not progressive discovery.
	if !selection.Progressive {
		mcpCount := countMCPTools(tools)
		if mcpCount == 0 {
			warningPayload := chat.ToolWarningPayload{
				Error: "This agent has no MCP tools configured. Responses will be text-only.",
				Level: "critical",
			}
			warningJSON, _ := json.Marshal(warningPayload)
			ch <- chat.StreamEvent{Type: "tool_warning", Data: string(warningJSON)}

			systemPrompt += "\n\nIMPORTANT: You have no tools available in this session. Do NOT attempt to call any tools — all tool calls will fail. Respond with text only. If the user's request requires tools (data lookup, task management, code execution, etc.), clearly explain that this agent is not configured with the necessary tools and suggest they switch to an agent that has tools configured."
		}
	}

	// Inject progressive discovery catalog.
	if selection.Progressive && selection.Catalog != "" {
		systemPrompt = systemPrompt + "\n\n" + selection.Catalog
	}

	systemPrompt += nativeToolGuide

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

	// --- Loop state ---
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name
	}
	// Debug mode: per-agent setting or global developer_mode.
	debugMode := isAgentDebugEnabled(agent.Settings) || s.isGlobalDebugMode()
	ls := newLoopState(constraints, toolNames, debugMode)

	// --- Tool-use loop ---
	var fullContent strings.Builder
	var finalUsage *chat.Usage
	var breakdown *chat.TokenBreakdown

	for ls.iteration = 0; ; ls.iteration++ {
		// Check layered iteration limits.
		if stop, reason := ls.shouldStop(); stop {
			log.Printf("[WARN] chat-loop stopped: %s (session=%s agent=%s iter=%d)", reason, sessionID, agent.ID, ls.iteration)
			ch <- chat.StreamEvent{Type: "status", Content: fmt.Sprintf("Stopped: %s", reason)}
			break
		}
		// Deadline check.
		if ctx.Err() != nil {
			log.Printf("[WARN] generateResponse context cancelled: %v (session=%s)", ctx.Err(), sessionID)
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
			log.Printf("chat-service: token budget enforcement refused: %v", budgetErr)
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

		var provCh <-chan provider.StreamEvent
		if len(tools) > 0 {
			log.Printf("chat-service: tool-use iteration %d — %d tools, %d messages, ~%d tokens (ceiling=%d)",
				ls.iteration, len(tools), len(chatMessages), breakdown.Total, breakdown.Ceiling)
			provCh, err = prov.StreamChatWithTools(provCtx, systemPrompt, chatMessages, model, tools)
		} else {
			provCh, err = prov.StreamChat(provCtx, systemPrompt, chatMessages, model)
		}
		if err != nil {
			provSpan.RecordError(err)
			provSpan.SetStatus(codes.Error, err.Error())
			provSpan.End()
			log.Printf("chat-service: provider stream error on iteration %d: %v", ls.iteration, err)
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
			if ls.retryBudget > 0 {
				ls.retryBudget--
			}
			// TODO(phase4): When provider error recovery is added (e.g., prompt_too_long
			// triggers compaction + retry), use:
			//   ls.continueWith(ContinueRecovery, fmt.Sprintf("recovered from %s", errCode))
			//   continue
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

		for evt := range provCh {
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
				ch <- chat.StreamEvent{Type: "delta", Content: evt.Content}

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
				errDetails := map[string]interface{}{"raw": evt.Error, "model": model}
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
				log.Printf("chat-service: response truncated by max_tokens on iteration %d", ls.iteration)
				ch <- chat.StreamEvent{Type: "status", Content: "Response was cut short due to length limits. Some content may be missing."}
				s.store.LogEvent(sessionID, "max_tokens_truncation", "warning",
					fmt.Sprintf("iteration %d: response truncated by max_tokens", ls.iteration),
					fmt.Sprintf(`{"model":%q,"iteration":%d}`, model, ls.iteration))
				ch <- chat.ErrorEnvelopeDelta(chat.ErrorCodeInternal, "Response truncated — hit output token limit", map[string]interface{}{
					"stop_reason": "max_tokens", "iteration": ls.iteration, "model": model,
				})
			}
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
		plans := s.preCheckTools(ctx, sessionID, regularTools, ls, ch, selection, tools)

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
					Name:     r.ref.Name,
					Duration: r.duration,
					Success:  !r.isError,
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
			log.Printf("chat-service: circuit breaker open, stopping at iteration %d", ls.iteration)
			if s.events != nil {
				s.events.EmitCircuitBreakerTripped(ctx, sessionID, "anthropic")
			}
			ch <- chat.StreamEvent{
				Type:    "circuit_open",
				Content: "Provider rate limited. Tool-use loop stopped. Would you like to retry?",
			}
			break
		}
	}

	// --- Post-processing ---
	responseContent := fullContent.String()
	if s.outputFilter != nil && s.outputFilter.Len() > 0 {
		responseContent = s.outputFilter.Apply(responseContent)
	}

	// Inject pending envelopes.
	for _, env := range ls.pendingEnvelopes {
		envelopeBlock := "\n\n```nanite-envelope\n" + env + "\n```"
		responseContent += envelopeBlock
		ch <- chat.StreamEvent{Type: "delta", Content: envelopeBlock}
	}

	// Inject ticket confirmation envelope.
	if tStart := strings.Index(userContent, "<!--TICKET_DATA:"); tStart >= 0 {
		tail := userContent[tStart+len("<!--TICKET_DATA:"):]
		if tEnd := strings.Index(tail, ":TICKET_DATA-->"); tEnd >= 0 {
			ticketJSON := tail[:tEnd]
			env := chat.BuildTicketConfirmationEnvelope(ticketJSON)
			if env != "" {
				envelopeBlock := "\n\n```nanite-envelope\n" + env + "\n```"
				responseContent += envelopeBlock
				ch <- chat.StreamEvent{Type: "delta", Content: envelopeBlock}
			}
		}
	}

	// Parse envelopes.
	envelopes, cleanContent, envErrors := chat.ParseEnvelopes(responseContent)
	for _, envErr := range envErrors {
		log.Printf("chat-service: envelope error (%s): %s", envErr.Reason, chat.TruncateStr(envErr.Raw, 200))
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
	for _, env := range envelopes {
		innerData, _ := json.Marshal(env.Data)
		envRefs = append(envRefs, chat.EnvelopeRef{Type: env.Type, Data: json.RawMessage(innerData)})
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
		log.Printf("chat-service: failed to save assistant message: %v", err)
		ch <- chat.ErrorEvent(chat.ErrorCodeInternal, "Failed to save response", map[string]interface{}{"raw": err.Error()})
		return
	}

	// Prune old tool messages.
	if err := s.context.PruneAfterTurn(ctx, sessionID); err != nil {
		log.Printf("chat-service: prune after turn failed: %v", err)
	}

	// Record token usage.
	if finalUsage != nil && (finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0) {
		if err := s.store.RecordUsage(sessionID, assistantMsgID, model,
			finalUsage.InputTokens, finalUsage.OutputTokens,
			finalUsage.CacheCreationTokens, finalUsage.CacheReadTokens); err != nil {
			log.Printf("chat-service: failed to record token usage: %v", err)
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
		log.Printf("chat-service: failed to record execution metrics: %v", err)
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
		go s.autoTitle(sessionID, userContent)
	}
	go s.autoTags(sessionID)
}

// ---------------------------------------------------------------------------
// Private helper methods
// ---------------------------------------------------------------------------

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

	// Hard cap.
	if *totalCalls > maxCalls || *consecutiveEmpty >= 2 {
		reason := fmt.Sprintf("consecutive_empty=%d, total_calls=%d", *consecutiveEmpty, *totalCalls)
		rtResult := "Tool discovery limit reached (" + reason + "). No more request_tools calls will be processed. Proceed with the tools you already have — do NOT call request_tools again."
		log.Printf("chat-service: request_tools halted — %s", reason)
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

	log.Printf("chat-service: request_tools loaded %d tools (consecutive_empty=%d): %v", len(loaded), *consecutiveEmpty, loaded)

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
			resultText = fmt.Sprintf("ERROR: Tool %q has been called %d times with identical results. "+
				"This tool is now BLOCKED for this session turn. "+
				"You MUST stop calling this tool and either try a completely different approach "+
				"or tell the user: \"I was unable to complete this task because the tool returned the same result repeatedly.\"",
				toolName, repeats+1)
			log.Printf("chat-service: tool %s BLOCKED after %d identical results", toolName, repeats+1)
		} else {
			resultText += "\n\nWARNING: This tool has returned the same result " +
				fmt.Sprintf("%d times in a row. You are likely stuck in a loop. ", repeats+1) +
				"Do NOT call this tool again with the same arguments. " +
				"Either provide different arguments or inform the user that this task cannot be completed."
			log.Printf("chat-service: tool %s repeat detected (%d times)", toolName, repeats+1)
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
		log.Printf("chat-service: sandbox dir error: %v", err)
	} else {
		if err := sandbox.Populate(sbDir, agent, mode, sandbox.PopulateOpts{
			SessionID: sessionID,
			DBPath:    "", // Store interface doesn't expose DBPath; will be wired in container
		}); err != nil {
			log.Printf("chat-service: sandbox populate error: %v", err)
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
		log.Printf("chat-service: failed to persist CLI session ID: %v", err)
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
		log.Printf("chat-service: auto-artifact creation failed for %s: %v", filePath, err)
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

	log.Printf("chat-service: envelope retry for session %s", sessionID)
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
	retryCh, err := prov.StreamChat(retryCtx, "", correctionMsgs, model)
	if err != nil {
		log.Printf("chat-service: envelope retry stream error: %v", err)
		return nil
	}

	var retryContent strings.Builder
	for evt := range retryCh {
		switch evt.Type {
		case "delta":
			retryContent.WriteString(evt.Content)
			ch <- chat.StreamEvent{Type: "delta", Content: evt.Content}
		case "error":
			log.Printf("chat-service: envelope retry error: %s", evt.Error)
			return nil
		}
	}

	retryEnvelopes, _, retryErrors := chat.ParseEnvelopes(retryContent.String())
	if len(retryErrors) > 0 {
		log.Printf("chat-service: envelope retry still had %d errors — giving up", len(retryErrors))
	}
	if len(retryEnvelopes) > 0 {
		log.Printf("chat-service: envelope retry recovered %d envelope(s)", len(retryEnvelopes))
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
	title, err := prov.Complete(context.Background(), prompt, msgs, s.utilityModel)
	duration := time.Since(start)
	s.recordUtilityMetrics(sessionID, "autoTitle", duration, err)

	if err != nil {
		log.Printf("chat-service: auto-title failed: %v", err)
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
	raw, err := prov.Complete(context.Background(), prompt, tagMsgs, s.utilityModel)
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
