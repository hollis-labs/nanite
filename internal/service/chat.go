package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/filter"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/loopdetect"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/nanite/internal/task"
	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/worker"
	"github.com/hollis-labs/nanite/pkg/models"
)

// chatShutdownMaxWait bounds how long chatServiceImpl.Shutdown waits for
// in-flight generateResponse goroutines to observe cancellation and exit.
const chatShutdownMaxWait = 10 * time.Second

// ChatService is the top-level orchestrator for message handling. It composes
// all Wave 1–2 services and replaces the monolithic Engine for chat operations.
type ChatService interface {
	// HandleMessage processes an incoming user message: persists it, starts
	// async generation, and returns the assistant message ID for SSE streaming.
	HandleMessage(ctx context.Context, sessionID, content string) (messageID string, err error)

	// RetryLastMessage re-generates the response for the last user message
	// in the session, resetting the circuit breaker first.
	RetryLastMessage(ctx context.Context, sessionID string) (messageID string, err error)

	// SendAgentMessage sends a message from one agent session to another,
	// triggering async generation in the target session.
	SendAgentMessage(ctx context.Context, fromSessionID, toSessionID, content string) (messageID string, err error)

	// RecomposeSystemPrompt rebuilds the system prompt for a session after
	// an agent or mode change.
	RecomposeSystemPrompt(ctx context.Context, sessionID, agentID, newMode string) (string, error)

	// DelegateTask spawns a worker session, sends the task, waits for completion,
	// and returns the result.
	DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error)

	// DelegateAndAggregate decomposes a complex task, delegates sub-tasks to
	// workers, and aggregates results.
	DelegateAndAggregate(ctx context.Context, parentSessionID, message, model string) (*chat.OrchestrationResult, error)

	// GetStream returns the event channel for a given message ID.
	GetStream(messageID string) (<-chan chat.StreamEvent, bool)

	// Shutdown kills all tracked CLI processes.
	Shutdown()
}

// ChatServiceConfig holds dependencies for constructing a ChatService.
type ChatServiceConfig struct {
	Sessions  SessionService
	Agents    AgentService
	Tools     ToolService
	Streams   *StreamManager
	Context   ContextService
	Events    EventEmitter
	Providers *provider.Registry
	Store     Store // full store for low-level operations (usage, events, messages)

	// Optional subsystems — nil-safe.
	Orchestrator *chat.Orchestrator
	AppConfig    *config.AppConfig
	OutputFilter *filter.Chain
	Commands     *chat.CommandRegistry
	PluginHost   PluginEventSink // for pre-hooks
	ProcessTracker *chat.ProcessTracker

	// SessionEventWriter writes lifecycle rows to session_events for
	// diagnostic reconstruction. nil = PTY observability disabled.
	SessionEventWriter SessionEventWriter

	// Utility provider/model for autoTitle/autoTags.
	UtilityProvider string
	UtilityModel    string

	// Permissions engine — nil-safe (permissions disabled).
	Permissions *permission.Engine

	// Task tracking service — nil-safe (task tracking disabled).
	Tasks task.Service

	// EmbeddingStatus drives the first-turn memory warning envelope:
	// when non-empty and not "active", a dismissible warning is emitted the
	// first time a given session generates a response.
	EmbeddingStatus   string
	EmbeddingProvider string

	// ResultCache for the cache-and-pointer pattern (S4a) — nil-safe.
	ResultCache *tool.ResultCache

	// ModelCatalog provides per-model context window and pricing data from
	// models.dev. Nil-safe: when absent the service falls back to user_settings
	// and then the hardcoded DefaultContextWindowSize.
	ModelCatalog *modelsdev.Client

	// DBPath is the path to the SQLite database file. Passed into
	// sandbox.Populate so spawned CLI MCP servers receive a non-empty --db flag.
	DBPath string

	// AdapterRegistry holds registered CLIAgentAdapters used by sandbox.Populate
	// to write CLAUDE.md and .mcp.json on each chat turn. nil = sandbox file
	// writes are skipped.
	AdapterRegistry *agent.AdapterRegistry

	// StrategyLogger persists v1 strategy decisions to strategy_decisions
	// (CW-20260419-0026, Phase 5 / E3). nil-safe: when absent, strategy
	// planning still runs and applies its MaxTurns to the loop budget,
	// but no row is written. *store.Store satisfies the interface.
	StrategyLogger strategyDecisionLogger

	// Inspector is the I1 per-turn dev-mode aggregator (CW-20260426-0004).
	// nil-safe: when nil the inspector is disabled. Set when developer_mode=true.
	Inspector *inspectsvc.Service

	// LoopDetector is the I2 fingerprint-based loop detector (CW-20260420-0029).
	// nil-safe: when nil loop detection is disabled. Shared across all sessions.
	LoopDetector *loopdetect.Detector
}

// chatServiceImpl is the concrete ChatService implementation.
type chatServiceImpl struct {
	sessions  SessionService
	agents    AgentService
	tools     ToolService
	streams   *StreamManager
	context   ContextService
	events    EventEmitter
	providers *provider.Registry
	store     Store

	orchestrator        *chat.Orchestrator
	appConfig           *config.AppConfig
	outputFilter        *filter.Chain
	commands            *chat.CommandRegistry
	pluginHost          PluginEventSink
	processTracker      *chat.ProcessTracker
	tasks               task.Service
	workers             *worker.Manager
	sessionEventWriter  SessionEventWriter

	utilityProvider string
	utilityModel    string
	permissions    *permission.Engine

	embeddingStatus   string
	embeddingProvider string
	// embeddingWarnedSessions tracks which session IDs have already received
	// the first-turn memory warning. Process-local; resets on restart.
	// Bounded FIFO eviction prevents unbounded growth on long-lived hosts with
	// many sessions.
	embeddingWarnedMu       sync.Mutex
	embeddingWarnedSessions map[string]struct{}
	embeddingWarnedOrder    []string

	// argValidator caches compiled JSON Schemas for tool InputSchema validation.
	argValidator *argValidator
	// resultCache stores large tool results for the cache-and-pointer pattern.
	resultCache  *tool.ResultCache
	modelCatalog *modelsdev.Client

	// dbPath is the SQLite database path forwarded to sandbox.Populate.
	dbPath string
	// adapterRegistry is forwarded to sandbox.Populate on each CLI chat turn.
	adapterRegistry *agent.AdapterRegistry

	// strategyLogger persists v1 strategy decisions. nil-safe.
	// (CW-20260419-0026, Phase 5 / E3.)
	strategyLogger strategyDecisionLogger

	// inspector is the I1 per-turn dev-mode aggregator (CW-20260426-0004).
	// nil-safe: wired only when developer_mode=true.
	inspector *inspectsvc.Service

	// loopDetector is the I2 fingerprint-based loop detector (CW-20260420-0029).
	// nil-safe: disabled when nil.
	loopDetector *loopdetect.Detector

	// lifecycle tracks async generateResponse goroutines so Shutdown can
	// cancel them and wait for them to drain rather than orphan them.
	lifecycle *lifecycle.Manager

	// activeGenMu guards activeGen. CW-20260418-0043: when the user retries
	// or sends a new message while an older generateResponse is still
	// running for the same session, we must cancel the old one before
	// launching the new one. Without this, concurrent loops fight over the
	// same provider rate-limit budget and the session spirals into pacing
	// waits that look like stalls from the UI.
	activeGenMu sync.Mutex
	activeGen   map[string]*inFlightGen // sessionID -> current in-flight generation
}

// inFlightGen records the currently-running generateResponse for a session
// so a fresh launch can cancel it. Keyed by msgID so the deregister path
// only clears the slot if we're still the active one.
type inFlightGen struct {
	msgID  string
	cancel context.CancelFunc
}

// NewChatService creates a ChatService from its dependencies.
func NewChatService(cfg ChatServiceConfig) ChatService {
	up := cfg.UtilityProvider
	if up == "" {
		up = models.DefaultProvider()
	}
	um := cfg.UtilityModel
	if um == "" {
		um = models.DefaultChatModel()
	}
	return &chatServiceImpl{
		sessions:       cfg.Sessions,
		agents:         cfg.Agents,
		tools:          cfg.Tools,
		streams:        cfg.Streams,
		context:        cfg.Context,
		events:         cfg.Events,
		providers:      cfg.Providers,
		store:          cfg.Store,
		orchestrator:   cfg.Orchestrator,
		appConfig:      cfg.AppConfig,
		outputFilter:   cfg.OutputFilter,
		commands:       cfg.Commands,
		pluginHost:     cfg.PluginHost,
		processTracker: cfg.ProcessTracker,
		tasks:               cfg.Tasks,
		utilityProvider:     up,
		utilityModel:        um,
		permissions:         cfg.Permissions,
		embeddingStatus:         cfg.EmbeddingStatus,
		embeddingProvider:       cfg.EmbeddingProvider,
		embeddingWarnedSessions: make(map[string]struct{}),
		argValidator:            newArgValidator(),
		resultCache:             cfg.ResultCache,
		modelCatalog:            cfg.ModelCatalog,
		dbPath:                  cfg.DBPath,
		adapterRegistry:         cfg.AdapterRegistry,
		lifecycle:               lifecycle.NewManager("service.chat"),
		activeGen:               make(map[string]*inFlightGen),
		sessionEventWriter:  cfg.SessionEventWriter,
		strategyLogger:      cfg.StrategyLogger,
		inspector:           cfg.Inspector,
		loopDetector:        cfg.LoopDetector,
	}
}

// registerGeneration stores the cancel for a new in-flight generation and
// returns the prior cancel if any, which the caller must invoke to abort
// the stale goroutine. Separated from launchGeneration so the takeover
// semantics are unit-testable without spinning up generateResponse.
// CW-20260418-0043.
func (s *chatServiceImpl) registerGeneration(sessionID, msgID string, cancel context.CancelFunc) (prev context.CancelFunc) {
	s.activeGenMu.Lock()
	defer s.activeGenMu.Unlock()
	if cur := s.activeGen[sessionID]; cur != nil {
		prev = cur.cancel
	}
	s.activeGen[sessionID] = &inFlightGen{msgID: msgID, cancel: cancel}
	return prev
}

// deregisterGeneration clears the registry slot if and only if the caller
// is still the active generation. A takeover will have replaced the slot;
// in that case this is a no-op.
func (s *chatServiceImpl) deregisterGeneration(sessionID, msgID string) {
	s.activeGenMu.Lock()
	defer s.activeGenMu.Unlock()
	if cur := s.activeGen[sessionID]; cur != nil && cur.msgID == msgID {
		delete(s.activeGen, sessionID)
	}
}

// launchGeneration starts a cancellable generateResponse goroutine for the
// given target session. If another generateResponse is already running for
// this session, it is cancelled first — prevents concurrent duplicate loops
// when the user retries mid-stream (CW-20260418-0043).
//
// The cancel is wired into both our per-session registry (for takeover) and
// the lifecycle manager (for graceful Shutdown) via a small bridge goroutine.
func (s *chatServiceImpl) launchGeneration(name, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
	// Build cancel BEFORE launching so a near-simultaneous retry cannot
	// register its own cancel before this one — the window would let the
	// retry cancel itself. context.Background() is deliberate: lifecycle
	// shutdown is bridged in below.
	genCtx, cancel := context.WithCancel(context.Background())

	prev := s.registerGeneration(sessionID, assistantMsgID, cancel)
	if prev != nil {
		slog.Info("chat-service: cancelling prior in-flight generation for session",
			"session_id", sessionID, "new_msg_id", assistantMsgID)
		prev()
	}

	s.lifecycle.Go(name, func(bgCtx context.Context) {
		// Bridge lifecycle shutdown (bgCtx) into our takeover-ctx so
		// generateResponse still aborts on process Shutdown.
		stopBridge := make(chan struct{})
		go func() {
			select {
			case <-bgCtx.Done():
				cancel()
			case <-stopBridge:
			}
		}()
		defer close(stopBridge)
		defer cancel()
		defer s.deregisterGeneration(sessionID, assistantMsgID)

		s.generateResponse(genCtx, sessionID, assistantMsgID, userContent, ch)
	})
}

// maybeEmitEmbeddingWarning pushes a one-time dismissible warning onto the
// stream when the embedder is not "active". Silently no-ops for active /
// unknown states and for sessions that have already been warned in this
// process lifetime.
// maxEmbeddingWarnedSessions bounds the per-session dedupe map so long-lived
// hosts don't leak memory. When the cap is hit, the oldest entry is dropped —
// a worst-case duplicate warning is far cheaper than unbounded growth.
const maxEmbeddingWarnedSessions = 4096

func (s *chatServiceImpl) maybeEmitEmbeddingWarning(sessionID string, ch chan chat.StreamEvent) {
	if s.embeddingStatus == "" || s.embeddingStatus == EmbeddingStatusActive {
		return
	}
	s.embeddingWarnedMu.Lock()
	if _, seen := s.embeddingWarnedSessions[sessionID]; seen {
		s.embeddingWarnedMu.Unlock()
		return
	}
	s.embeddingWarnedSessions[sessionID] = struct{}{}
	s.embeddingWarnedOrder = append(s.embeddingWarnedOrder, sessionID)
	if len(s.embeddingWarnedOrder) > maxEmbeddingWarnedSessions {
		drop := s.embeddingWarnedOrder[0]
		s.embeddingWarnedOrder = s.embeddingWarnedOrder[1:]
		delete(s.embeddingWarnedSessions, drop)
	}
	s.embeddingWarnedMu.Unlock()
	var msg string
	switch s.embeddingStatus {
	case EmbeddingStatusDisabled:
		msg = "Memory similarity recall is off. Enable an embedding provider in Settings \u2192 Memory."
	case EmbeddingStatusMissingCredentials:
		msg = fmt.Sprintf("Memory similarity recall is disabled: %s credentials not found. Add the key in Settings \u2192 Providers.", s.embeddingProvider)
	case EmbeddingStatusUnreachable:
		msg = fmt.Sprintf("Memory similarity recall is disabled: %s is not reachable. Start it or switch providers in Settings \u2192 Memory.", s.embeddingProvider)
	default:
		msg = "Memory similarity recall is disabled."
	}
	payload := chat.ToolWarningPayload{
		ToolName: "memory",
		Error:    msg,
		Level:    "warning",
	}
	data, _ := json.Marshal(payload)
	ch <- chat.StreamEvent{Type: "tool_warning", Data: string(data)}
}

// HandleMessage implements ChatService.
func (s *chatServiceImpl) HandleMessage(ctx context.Context, sessionID, content string) (string, error) {
	// Persist user message.
	userMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		return "", fmt.Errorf("create user message: %w", err)
	}

	// Emit message.sent plugin event with the real user message ID.
	if s.pluginHost != nil {
		safego.Go(ctx, "service.chat.emit.message-sent", func() {
			s.pluginHost.EmitMessageSent(sessionID, userMsg.ID, content, "user", 0)
		})
	}

	// Create assistant message ID and stream.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, sessionID)

	// Start async generation on a per-session cancellable ctx. If a prior
	// generateResponse is still running for this session it will be
	// cancelled — concurrent loops on the same session fight over the
	// provider rate-limit budget and look like stalls from the UI
	// (CW-20260418-0043). The lifecycle manager's shutdown ctx is bridged
	// inside launchGeneration so process Shutdown still drains cleanly.
	s.launchGeneration("handleMessage.generateResponse", sessionID, assistantMsgID, content, ch)

	return assistantMsgID, nil
}

// RetryLastMessage implements ChatService.
func (s *chatServiceImpl) RetryLastMessage(ctx context.Context, sessionID string) (string, error) {
	// Reset circuit breaker on the session's provider.
	session, err := s.sessions.Get(ctx, sessionID)
	if err == nil {
		provName := session.Provider
		if provName == "" {
			provName = "anthropic"
		}
		if prov, ok := s.providers.Get(provName); ok {
			if ap, ok := prov.(*provider.Anthropic); ok && ap.CircuitBreaker != nil {
				ap.CircuitBreaker.Reset()
				slog.Info("chat-service: circuit breaker reset for retry", "session_id", sessionID)
			}
		}
	}

	// Find the last user message.
	msgs, err := s.store.ListMessages(sessionID, 50)
	if err != nil {
		return "", fmt.Errorf("list messages for retry: %w", err)
	}

	var userContent string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			userContent = msgs[i].Content
			break
		}
	}
	if userContent == "" {
		return "", fmt.Errorf("no user message found in session %s", sessionID)
	}

	// Create new assistant message and stream.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, sessionID)

	s.launchGeneration("retryLastMessage.generateResponse", sessionID, assistantMsgID, userContent, ch)

	return assistantMsgID, nil
}

// SendAgentMessage implements ChatService.
func (s *chatServiceImpl) SendAgentMessage(ctx context.Context, fromSessionID, toSessionID, content string) (string, error) {
	// Look up the sending agent.
	fromAgentID := "unknown"
	if agent, _, err := s.agents.ResolveForSession(ctx, fromSessionID); err == nil {
		fromAgentID = agent.ID
	}

	// Create the message in the target session with agent attribution.
	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: toSessionID,
		AgentID:   fromAgentID,
		Role:      "user",
		Content:   content,
		Metadata:  fmt.Sprintf(`{"source":"agent","from_session":"%s","from_agent":"%s"}`, fromSessionID, fromAgentID),
	}
	if err := s.store.CreateMessage(msg); err != nil {
		return "", fmt.Errorf("create agent message: %w", err)
	}

	// Start async generation in the target session.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, toSessionID)

	s.launchGeneration("sendAgentMessage.generateResponse", toSessionID, assistantMsgID, content, ch)

	return assistantMsgID, nil
}

// RecomposeSystemPrompt implements ChatService.
func (s *chatServiceImpl) RecomposeSystemPrompt(ctx context.Context, sessionID, agentID, newMode string) (string, error) {
	agent, err := s.agents.Get(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("get agent: %w", err)
	}

	mode := &store.AgentMode{}
	if modes, mErr := s.agents.ListModes(ctx, agentID); mErr == nil {
		for _, m := range modes {
			if m.Slug == newMode {
				mode = &m
				break
			}
		}
	}

	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}

	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, _ = s.store.GetWorkspace(session.WorkspaceID)
	}

	// Delegate to the existing context assembly helpers in the chat package.
	// assembleSystemPromptFromTemplates and buildSkillList are in chat/context.go.
	prompt, _, err := s.context.AssembleContext(ctx, session, agent, mode, workspace)
	if err != nil {
		return "", fmt.Errorf("assemble context: %w", err)
	}
	return prompt, nil
}

// GetStream implements ChatService.
func (s *chatServiceImpl) GetStream(messageID string) (<-chan chat.StreamEvent, bool) {
	return s.streams.GetStream(messageID)
}

// Shutdown implements ChatService. It kills tracked CLI processes and then
// cancels the service's lifecycle context, waiting up to chatShutdownMaxWait
// for in-flight generateResponse goroutines to exit. Any goroutines still
// running after the timeout are logged; see lifecycle.ShutdownTimeoutError.
func (s *chatServiceImpl) Shutdown() {
	if s.processTracker != nil {
		s.processTracker.KillAll()
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.Shutdown(chatShutdownMaxWait); err != nil {
			slog.Warn("chat-service: lifecycle shutdown", "err", err)
		}
	}
}

// SetWorkers injects the worker manager after construction to break the
// circular dependency (ChatService <-> WorkerManager).
func (s *chatServiceImpl) SetWorkers(w *worker.Manager) {
	s.workers = w
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// resolveProvider walks the provider fallback chain. Resolution order:
//  1. sessionProvider (explicit per-session)
//  2. agentProvider (agent profile default)
//  3. User's fallback chain (from user_settings)
//  4. chat.InferProvider(model) — map model name to provider
//  5. System default ("anthropic")
//
// When a preferred provider is unavailable and the chain falls through,
// a provider.fallback plugin event is emitted.
func (s *chatServiceImpl) resolveProvider(sessionID, sessionProvider, agentProvider, model string) (string, provider.Provider) {
	// Track the first requested provider so we can emit a fallback event
	// when a later candidate is selected instead.
	requested := sessionProvider
	if requested == "" {
		requested = agentProvider
	}

	if sessionProvider != "" {
		if p, ok := s.providers.Get(sessionProvider); ok {
			return sessionProvider, p
		}
		slog.Warn("chat-service: session provider not registered, falling through", "provider", sessionProvider)
	}

	if agentProvider != "" {
		if p, ok := s.providers.Get(agentProvider); ok {
			if requested != "" && requested != agentProvider && s.pluginHost != nil {
				s.pluginHost.EmitProviderFallback(sessionID, requested, agentProvider)
			}
			return agentProvider, p
		}
		slog.Warn("chat-service: agent provider not registered, falling through", "provider", agentProvider)
	}

	if us, err := s.store.GetUserSettings(); err == nil && len(us.ProviderFallbackChain) > 0 {
		for _, name := range us.ProviderFallbackChain {
			if p, ok := s.providers.Get(name); ok {
				if requested != "" && requested != name && s.pluginHost != nil {
					s.pluginHost.EmitProviderFallback(sessionID, requested, name)
				}
				return name, p
			}
		}
	}

	inferred := chat.InferProvider(model)
	if p, ok := s.providers.Get(inferred); ok {
		if requested != "" && requested != inferred && s.pluginHost != nil {
			safego.Go(context.Background(), "service.chat.emit.provider-fallback-inferred", func() {
				s.pluginHost.EmitProviderFallback(sessionID, requested, inferred)
			})
		}
		return inferred, p
	}

	if p, ok := s.providers.Get("anthropic"); ok {
		if requested != "" && requested != "anthropic" && s.pluginHost != nil {
			safego.Go(context.Background(), "service.chat.emit.provider-fallback-anthropic", func() {
				s.pluginHost.EmitProviderFallback(sessionID, requested, "anthropic")
			})
		}
		return "anthropic", p
	}

	return inferred, nil
}

