package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/task"
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

	// Utility provider/model for autoTitle/autoTags.
	UtilityProvider string
	UtilityModel    string

	// Permissions engine — nil-safe (permissions disabled).
	Permissions *permission.Engine

	// Task tracking service — nil-safe (task tracking disabled).
	Tasks task.Service
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

	orchestrator   *chat.Orchestrator
	appConfig      *config.AppConfig
	outputFilter   *filter.Chain
	commands       *chat.CommandRegistry
	pluginHost     PluginEventSink
	processTracker *chat.ProcessTracker
	tasks          task.Service
	workers        *worker.Manager

	utilityProvider string
	utilityModel    string
	permissions    *permission.Engine

	// lifecycle tracks async generateResponse goroutines so Shutdown can
	// cancel them and wait for them to drain rather than orphan them.
	lifecycle *lifecycle.Manager
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
		tasks:          cfg.Tasks,
		utilityProvider: up,
		utilityModel:    um,
		permissions:    cfg.Permissions,
		lifecycle:      lifecycle.NewManager("service.chat"),
	}
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

	// Start async generation on the service's lifecycle-owned context. The
	// HTTP request context is cancelled when the handler returns
	// (202 Accepted), so we cannot use it — but we also must not orphan the
	// goroutine. The lifecycle manager's context is detached from the
	// request and cancelled on Shutdown, giving us both properties.
	s.lifecycle.Go("handleMessage.generateResponse", func(bgCtx context.Context) {
		s.generateResponse(bgCtx, sessionID, assistantMsgID, content, ch)
	})

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
				log.Printf("chat-service: circuit breaker reset for retry on session %s", sessionID)
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

	s.lifecycle.Go("retryLastMessage.generateResponse", func(bgCtx context.Context) {
		s.generateResponse(bgCtx, sessionID, assistantMsgID, userContent, ch)
	})

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

	s.lifecycle.Go("sendAgentMessage.generateResponse", func(bgCtx context.Context) {
		s.generateResponse(bgCtx, toSessionID, assistantMsgID, content, ch)
	})

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
			log.Printf("chat-service: lifecycle shutdown: %v", err)
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
		log.Printf("chat-service: session provider %q not registered, falling through", sessionProvider)
	}

	if agentProvider != "" {
		if p, ok := s.providers.Get(agentProvider); ok {
			if requested != "" && requested != agentProvider && s.pluginHost != nil {
				s.pluginHost.EmitProviderFallback(sessionID, requested, agentProvider)
			}
			return agentProvider, p
		}
		log.Printf("chat-service: agent provider %q not registered, falling through", agentProvider)
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

