package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
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
