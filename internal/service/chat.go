package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/filter"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	"github.com/hollis-labs/nanite/internal/loopdetect"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/reminders"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/task"
	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/worker"
)

// chatShutdownMaxWait bounds how long chatServiceImpl.Shutdown waits for
// in-flight generateResponse goroutines to observe cancellation and exit.
const chatShutdownMaxWait = 10 * time.Second

// runtimeTurnCancelMaxWait bounds the provider-facing CancelTurn request.
// Cancellation is launched asynchronously so API/user-stop remains
// non-blocking; successor generations wait for both this bounded request and
// the predecessor's terminal return before issuing their first prompt.
const runtimeTurnCancelMaxWait = 2 * time.Second

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

	// DelegateTask spawns a worker session, sends the task, waits for completion,
	// and returns the result.
	DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error)

	// DelegateAndAggregate decomposes a complex task, delegates sub-tasks to
	// workers, and aggregates results.
	DelegateAndAggregate(ctx context.Context, parentSessionID, message, model string) (*chat.OrchestrationResult, error)

	// GetStream returns the event channel for a given message ID.
	GetStream(messageID string) (<-chan chat.StreamEvent, bool)

	// CancelActiveGeneration cancels the currently-streaming generateResponse
	// goroutine for the given session, if one is registered. Returns true
	// when a cancel was dispatched, false when no generation was active.
	// CW-20260512-0006: backstop for user-initiated stop now that the
	// 5-minute parent wall-clock deadline is gone.
	CancelActiveGeneration(sessionID string) bool

	// RebootSessionAgent tears down one session's live runtime agent so the
	// next user turn cold-boots a fresh agent (new boot dir from the current
	// binary) without touching other sessions. Returns ErrSessionBusy when a
	// turn is in flight for the session. CW-20260516-0057.
	RebootSessionAgent(ctx context.Context, sessionID string) (RebootResult, error)

	// RecoverSession evicts the live runtime like a reboot, but the next turn
	// cold-boots into auto-recovery (recovery pack + provider resume) instead
	// of a clean fresh boot. CW-20260525-0001 Slice 2. Returns ErrSessionBusy
	// when a turn is in flight.
	RecoverSession(ctx context.Context, sessionID string) (RebootResult, error)

	// Shutdown kills all tracked CLI processes and reports whether every
	// lifecycle-owned callback drained successfully.
	Shutdown() error
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
	Orchestrator   *chat.Orchestrator
	AppConfig      *config.TunablesConfig
	OutputFilter   *filter.Chain
	Commands       *chat.CommandRegistry
	PluginHost     PluginEventSink // for pre-hooks
	ProcessTracker *chat.ProcessTracker

	// SessionEventWriter writes lifecycle rows to session_events for
	// diagnostic reconstruction. nil = PTY observability disabled.
	SessionEventWriter SessionEventWriter

	// SubagentInbox reads/acks kind=subagent_result agent_messages for
	// the turn-start injection (CW-20260512-0019). nil = injection
	// disabled (pending subagent results are still reachable via the
	// message_inbox MCP tool, just not auto-surfaced at turn start).
	SubagentInbox SubagentResultInbox

	// Utility provider/model for autoTitle/autoTags.
	UtilityProvider string
	UtilityModel    string

	// Permissions engine — nil-safe (permissions disabled).
	Permissions *permission.Engine

	// PathGrants tracks session-scoped explicit-mention path grants for
	// the trust-agent permission redesign (CW-20260430-0009). nil-safe.
	PathGrants *permission.PathGrants

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

	// Inspector is the I1 per-turn dev-mode aggregator (CW-20260426-0004).
	// nil-safe: when nil the inspector is disabled. Set when developer_mode=true.
	Inspector *inspectsvc.Service

	// LoopDetector is the I2 fingerprint-based loop detector (CW-20260420-0029).
	// nil-safe: when nil loop detection is disabled. Shared across all sessions.
	LoopDetector *loopdetect.Detector

	// ReminderEngine is the deterministic trigger engine for agent-set reminders
	// (J11, CW-20260426-0009). nil-safe: when nil reminder eval is skipped.
	ReminderEngine *reminders.Engine

	// ReflexEngine is the FU-30 DB-backed agent reflex engine. nil-safe: when
	// nil, per-turn reflex evaluation is skipped. Evaluates agent_reflexes,
	// applies inject_reminder / force_tool_choice / halt actions per turn.
	ReflexEngine *reflexes.Engine

	// AgentDeps is the agent-runtime composition root (Phase 4c.1 of the
	// agent-boot adoption). Threaded through here so HandleMessage can
	// gate the long-lived PTY path on the session's CLIAdapter capabilities
	// (Caps.PTY=true). nil = the legacy chat-harness path is taken
	// universally; useful for tests and bootstrapping configurations that
	// haven't wired the runtime yet.
	AgentDeps *runtimeagent.Dependencies

	// AgentSessionManager is the single registry for wrapper-owned runtime
	// handles. Chat lookup and daemon shutdown share it with agent.Boot.
	AgentSessionManager *runtimeagent.SessionManager

	// AgentEventBridge is the per-session event router owned by the chat
	// service. driveBootSession (Phase 4c.4) uses it to bind a per-turn
	// turnCh that receives runtime events instead of broadcasting them as
	// SSE. nil-safe: when absent, CLI sessions cannot route events through
	// the chat-harness loop (and the long-lived path is unavailable).
	AgentEventBridge *agentEventBridge

	// AgentBootDirAdapter is the recovery.BootDirOps adapter that
	// satisfies repopulate-sandbox / regenerate-CLAUDE.md remediations.
	// driveBootSession Tracks each successful Boot so the broker has the
	// per-session bootDir + Options on hand at remediation time;
	// CloseAgentSession Untracks at archive. nil-safe: when absent the
	// chat service skips Track/Untrack and the broker degrades to
	// "Permanent for anything that needs sandbox repair" — the same
	// pre-Phase-9 behavior.
	AgentBootDirAdapter *agentBootDirAdapter

	// EnvelopeRenderExecutor is the B3 in-process executor pilot
	// (CW-20260429-0032 — internal/executor/envelope_render). Wired
	// here so chat_generate.go's dispatch seam (B2 — CW-20260429-0031)
	// can hand off non-chat-direct routes emitted by the classifier.
	// nil-safe: when absent, the route hint stays purely informative
	// and every turn runs the chat-direct loop.
	EnvelopeRenderExecutor dispatch.Executor
	// Lifecycle is supplied by the composition root so CompositeEmitter and
	// direct chat work share one shutdown/drain boundary. Nil creates a
	// private manager for focused tests and standalone construction.
	Lifecycle *lifecycle.Manager
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

	orchestrator       *chat.Orchestrator
	appConfig          *config.TunablesConfig
	outputFilter       *filter.Chain
	commands           *chat.CommandRegistry
	pluginHost         PluginEventSink
	processTracker     *chat.ProcessTracker
	tasks              task.Service
	workers            fullWorkerSpawner
	sessionEventWriter SessionEventWriter
	subagentInbox      SubagentResultInbox

	utilityProvider string
	utilityModel    string
	permissions     *permission.Engine
	pathGrants      *permission.PathGrants

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

	// inspector is the I1 per-turn dev-mode aggregator (CW-20260426-0004).
	// nil-safe: wired only when developer_mode=true.
	inspector *inspectsvc.Service

	// loopDetector is the I2 fingerprint-based loop detector (CW-20260420-0029).
	// nil-safe: disabled when nil.
	loopDetector *loopdetect.Detector

	// reminderEngine is the deterministic trigger engine for agent-set reminders
	// (J11, CW-20260426-0009). nil-safe: when nil reminder eval is skipped.
	reminderEngine *reminders.Engine

	// reflexEngine evaluates DB-backed agent reflexes per turn (FU-30) and
	// returns staged actions injected into the turn. nil-safe.
	reflexEngine *reflexes.Engine

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

	// agentDeps is the agent-runtime composition root (Phase 4c.1). nil
	// when the runtime is not wired (legacy chat-harness path universal).
	agentDeps *runtimeagent.Dependencies

	// activeSessions tracks long-lived runtime sessions keyed by chat
	// session id (Phase 4c). First HandleMessage call for a CLI-PTY-capable
	// session boots the runtime; subsequent calls SendInput on the existing
	// session. Map values are *runtimeagent.Session.
	activeSessionsOnce sync.Once
	activeSessions     *runtimeagent.SessionManager

	// freshBootSessions is a one-shot, in-memory set of session ids whose
	// NEXT cold-boot must skip auto-recovery (no recovery pack, no provider
	// resume) — i.e. an intentional "Reboot agent" stays a clean fresh boot.
	// CW-20260525-0001: because it's in-memory, a daemon restart loses the
	// flag, so an involuntary restart still auto-recovers; only an in-process
	// reboot suppresses it. driveBootSession LoadAndDeletes it (one-shot).
	freshBootSessions sync.Map

	// agentEventBridge owns per-session router state. driveBootSession
	// (Phase 4c.4) binds a per-turn turnCh via SetPerSessionRouter so the
	// chat-harness loop consumes the runtime's StreamEvent stream without
	// double-emitting SSE for inter-turn events. nil when the runtime is
	// not wired (matches agentDeps == nil).
	agentEventBridge *agentEventBridge

	// agentBootDirAdapter is the recovery.BootDirOps adapter (Phase 9 —
	// CW-20260510-0014). driveBootSession Tracks each successful Boot so
	// the recovery broker can repopulate the sandbox dir / regenerate
	// CLAUDE.md during remediation; CloseAgentSession Untracks at
	// archive. nil-safe: when absent the chat service skips Track/Untrack
	// calls and the broker degrades to "Permanent for anything that needs
	// sandbox repair".
	agentBootDirAdapter *agentBootDirAdapter

	// activeSessionSlots remembers the last slot-content hash applied per
	// session so driveBootSession only regenerates CLAUDE.md /
	// agent-context.md when System / Agent / Mode / Rules slots change.
	// Map values are uint64 (FNV-1a hash). Phase 4c.4 / 4c.5.
	activeSessionSlots sync.Map

	// toolPartitionStates carries per-session ToolPartitionState across
	// turns when NANITE_TOOLS_LAZY_LOAD is on. Map values are
	// chat.ToolPartitionState. Hysteresis-pin tools so brief usage gaps
	// don't bounce them back to lazy and thrash the Tools-slot CacheKey.
	// G-HOT-SWAP-DEAD activation.
	toolPartitionStates sync.Map

	// rebootingSessions flags exact runtime generations being
	// torn down on purpose by RebootSessionAgent (CW-20260516-0057). The
	// Wait-observer (observeSessionForRecovery) consults this set so a
	// deliberate reboot exit is not misclassified as a crash and routed to
	// the recovery broker. It is pointer-keyed so a fast successor cannot
	// consume a predecessor's intent flag. Values are struct{}; entries are
	// cleared by the observer via LoadAndDelete.
	rebootingSessions sync.Map

	// displacedSessions flags the exact *runtimeagent.Session objects
	// adoptReplacementSession (TASKS/agent-host-acp/21) is stopping on
	// purpose after displacing them from activeSessions in favor of a
	// broker-dispatched replacement. Deliberately keyed by session
	// POINTER, not chat session id: a
	// displaced-and-a-replacement can be live under the SAME chat
	// session id at once for a short window, each driven by its own
	// observeSessionForRecovery goroutine — a sessionID-keyed flag can't
	// tell which generation's exit it's meant for and risks either the
	// replacement stealing a flag meant for the old session (clobbering
	// itself out of activeSessions) or the old session's flag going
	// stale forever if it never actually exits. Keying by the exact
	// object being stopped removes that ambiguity entirely. Values are
	// struct{}; entries are cleared by the observer via LoadAndDelete.
	displacedSessions sync.Map

	// envelopeRenderExecutor is the B3 in-process executor pilot,
	// dispatched by chat_generate.go's route seam when the B2
	// classifier emits RouteExecutorEnvelopeRender. nil-safe: when nil
	// the route is purely informative and the chat-direct loop runs.
	envelopeRenderExecutor dispatch.Executor

	// activeSessionContextBlocks stamps the resolved output of a
	// session's agent's DB-configured cmd/http context resolvers (Phase
	// 2 item 02, TASKS/phase-2/02-port-forward-dynamic-resolver.md),
	// keyed by chat session id. Map values are map[string]string
	// (slot name -> resolved content). Resolved once at initial cold
	// boot (resolveAgentContextForBoot); regenerateBootDirSlots reads
	// the stash so a mid-session CLAUDE.md regen doesn't drop the
	// resolved content the way a bare re-derive from the agent profile
	// would. Cleared in CloseAgentSession alongside the other
	// per-session maps.
	activeSessionContextBlocks sync.Map

	// dispatcher is the single agent-dispatch door
	// (CW-20260512-0121 / SP-20260512-0011). launchGeneration routes
	// the user → chat call-site through this; ChatRunner (subagent
	// path) routes through the same instance; the agent-flavored
	// background-job path (reserved CallerType) will join when its
	// caller lands. Always non-nil — initialized in NewChatService
	// with a self-referencing runner adapter so the "one door"
	// invariant is structural.
	dispatcher *dispatcher.Dispatcher
}

func (s *chatServiceImpl) runtimeSessions() *runtimeagent.SessionManager {
	s.activeSessionsOnce.Do(func() {
		if s.activeSessions == nil && s.agentDeps != nil {
			s.activeSessions = s.agentDeps.Manager
		}
		if s.activeSessions == nil {
			s.activeSessions = runtimeagent.NewSessionManager()
		}
	})
	return s.activeSessions
}

// inFlightGen records the currently-running generateResponse for a session
// so a fresh launch can cancel it. The registry uses pointer identity so a
// predecessor's deregister path cannot clear its successor.
type inFlightGen struct {
	msgID        string
	cancel       context.CancelFunc
	done         chan struct{}
	cancelIssued chan struct{}
	cancelOnce   sync.Once
	turnMu       sync.Mutex
	turn         *runtimeTurnBinding
	cancelAsked  bool
	cancelSafe   bool
}

// runtimeTurnBinding is captured at the same admission boundary that publishes
// the per-turn router, before Session.SendInput. Cancellation owns this exact
// wrapper generation and router token; it never resolves either by session ID
// after asynchronous work begins.
type runtimeTurnBinding struct {
	session *runtimeagent.Session
	router  *sessionRouter
}

type inFlightGenContextKey struct{}

func generationFromContext(ctx context.Context) *inFlightGen {
	gen, _ := ctx.Value(inFlightGenContextKey{}).(*inFlightGen)
	return gen
}

// admitRuntimeTurn serializes prompt/router admission with cancellation. bind
// must publish router and must not block; it runs under turnMu so a concurrent
// cancellation either captures this exact binding or prevents it entirely.
func (g *inFlightGen) admitRuntimeTurn(ctx context.Context, binding *runtimeTurnBinding, bind func()) bool {
	if g == nil || binding == nil || binding.session == nil || binding.router == nil {
		return false
	}
	g.turnMu.Lock()
	defer g.turnMu.Unlock()
	if g.cancelAsked || ctx.Err() != nil {
		return false
	}
	bind()
	g.turn = binding
	return true
}

func newInFlightGen(msgID string, cancel context.CancelFunc) *inFlightGen {
	return &inFlightGen{
		msgID:        msgID,
		cancel:       cancel,
		done:         make(chan struct{}),
		cancelIssued: make(chan struct{}),
	}
}

// goTracked schedules asynchronous chat work on the service lifecycle. Bare
// chatServiceImpl values used by focused unit tests have no composition-root
// lifecycle; in that case execute inline rather than creating an unowned
// goroutine. Production construction always supplies a manager.
func (s *chatServiceImpl) goTracked(label string, fn func(context.Context)) {
	if s.lifecycle == nil {
		fn(context.Background())
		return
	}
	s.lifecycle.Go(label, fn)
}

func (s *chatServiceImpl) trackedDone() <-chan struct{} {
	if s.lifecycle == nil {
		return nil
	}
	return s.lifecycle.Context().Done()
}

// NewChatService creates a ChatService from its dependencies.
func NewChatService(cfg ChatServiceConfig) ChatService {
	// Utility provider/model default to the system default when not
	// configured. Resolution goes through the store so user_settings and
	// providers.default_model are honored (CW-20260526-0003). If the chain
	// is dry, leave both empty — utility callers handle the missing case.
	up := cfg.UtilityProvider
	um := cfg.UtilityModel
	if (up == "" || um == "") && cfg.Store != nil {
		if resolvedProv, resolvedModel, err := cfg.Store.ResolveProviderAndModel(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, up, um); err == nil {
			up = resolvedProv
			um = resolvedModel
		}
	}
	activeSessions := cfg.AgentSessionManager
	if activeSessions == nil && cfg.AgentDeps != nil {
		activeSessions = cfg.AgentDeps.Manager
	}
	if activeSessions == nil {
		activeSessions = runtimeagent.NewSessionManager()
	}
	impl := &chatServiceImpl{
		sessions:                cfg.Sessions,
		agents:                  cfg.Agents,
		tools:                   cfg.Tools,
		streams:                 cfg.Streams,
		context:                 cfg.Context,
		events:                  cfg.Events,
		providers:               cfg.Providers,
		store:                   cfg.Store,
		orchestrator:            cfg.Orchestrator,
		appConfig:               cfg.AppConfig,
		outputFilter:            cfg.OutputFilter,
		commands:                cfg.Commands,
		pluginHost:              cfg.PluginHost,
		processTracker:          cfg.ProcessTracker,
		tasks:                   cfg.Tasks,
		utilityProvider:         up,
		utilityModel:            um,
		permissions:             cfg.Permissions,
		pathGrants:              cfg.PathGrants,
		embeddingStatus:         cfg.EmbeddingStatus,
		embeddingProvider:       cfg.EmbeddingProvider,
		embeddingWarnedSessions: make(map[string]struct{}),
		argValidator:            newArgValidator(),
		resultCache:             cfg.ResultCache,
		modelCatalog:            cfg.ModelCatalog,
		dbPath:                  cfg.DBPath,
		adapterRegistry:         cfg.AdapterRegistry,
		lifecycle:               cfg.Lifecycle,
		activeGen:               make(map[string]*inFlightGen),
		sessionEventWriter:      cfg.SessionEventWriter,
		subagentInbox:           cfg.SubagentInbox,
		inspector:               cfg.Inspector,
		loopDetector:            cfg.LoopDetector,
		reminderEngine:          cfg.ReminderEngine,
		reflexEngine:            cfg.ReflexEngine,
		agentDeps:               cfg.AgentDeps,
		activeSessions:          activeSessions,
		agentEventBridge:        cfg.AgentEventBridge,
		agentBootDirAdapter:     cfg.AgentBootDirAdapter,
		envelopeRenderExecutor:  cfg.EnvelopeRenderExecutor,
	}
	if impl.lifecycle == nil {
		impl.lifecycle = lifecycle.NewManager("service.chat")
	}
	// CW-20260512-0121 (SP-20260512-0011): wire the single dispatcher
	// door. The Dispatcher delegates to chatServiceImpl.generateResponse
	// (the unified agent-sessions runner) via a thin adapter. Two-step
	// build because the adapter needs the impl pointer; constructed
	// here so launchGeneration and ChatRunner.invokeChat both route
	// through the same Dispatcher instance.
	impl.dispatcher = dispatcher.New(newChatRunnerAdapter(impl))
	return impl
}

// Dispatcher returns the chat service's single agent-dispatch door
// (CW-20260512-0121). Exposed so the service container can hand the
// same Dispatcher instance to other call-sites (ChatRunner / future
// agent-background) — every CallerType MUST share one Dispatcher so
// the "identical structural shape across CallerTypes" invariant
// holds. Returning the field rather than re-constructing keeps the
// runner-adapter binding stable across the service lifetime.
func (s *chatServiceImpl) Dispatcher() *dispatcher.Dispatcher {
	return s.dispatcher
}

// registerGeneration stores the cancel for a new in-flight generation and
// returns the prior cancel if any, which the caller must invoke to abort
// the stale goroutine. Separated from launchGeneration so the takeover
// semantics are unit-testable without spinning up generateResponse.
// CW-20260418-0043.
func (s *chatServiceImpl) registerGeneration(sessionID, msgID string, cancel context.CancelFunc) (prev, current *inFlightGen) {
	s.activeGenMu.Lock()
	defer s.activeGenMu.Unlock()
	if cur := s.activeGen[sessionID]; cur != nil {
		prev = cur
	}
	current = newInFlightGen(msgID, cancel)
	s.activeGen[sessionID] = current
	return prev, current
}

// deregisterGeneration clears the registry slot if and only if the caller
// is still the active generation. A takeover will have replaced the slot;
// in that case this is a no-op.
func (s *chatServiceImpl) deregisterGeneration(sessionID string, gen *inFlightGen) {
	if gen == nil {
		return
	}
	// User cancellation is intentionally asynchronous, but the registry entry
	// remains a takeover barrier until its exact CancelTurn/Stop settles. A new
	// request arriving after generateResponse returns must still inherit it.
	gen.turnMu.Lock()
	cancelAsked := gen.cancelAsked
	gen.turnMu.Unlock()
	if cancelAsked && gen.cancelIssued != nil {
		<-gen.cancelIssued
	}
	s.activeGenMu.Lock()
	defer s.activeGenMu.Unlock()
	if cur := s.activeGen[sessionID]; cur == gen {
		delete(s.activeGen, sessionID)
	}
}

// registerGenerationIfIdle atomically registers a new in-flight generation
// for sessionID ONLY if none is already registered — unlike
// registerGeneration, it never takes over an existing slot. Returns false
// (and registers nothing) if the session is already busy.
//
// This is the reject-if-busy counterpart to registerGeneration's
// takeover semantics, used by TriggerHarnessTurn (CW-20260520-0001) to
// close a TOCTOU race: a caller that first checks IsGenerating() and then
// separately calls launchGeneration has a window between the two calls
// where a real user turn can start — launchGeneration's takeover would
// then silently cancel that user's in-flight generation. Folding the
// check and the registration into one mutex-held step removes the
// window entirely (PR #247 review).
func (s *chatServiceImpl) registerGenerationIfIdle(sessionID, msgID string, cancel context.CancelFunc) (*inFlightGen, bool) {
	s.activeGenMu.Lock()
	defer s.activeGenMu.Unlock()
	if cur := s.activeGen[sessionID]; cur != nil {
		return nil, false
	}
	current := newInFlightGen(msgID, cancel)
	s.activeGen[sessionID] = current
	return current, true
}

// requestGenerationCancellation cancels Nanite's generation context and
// requests provider turn cancellation against the exact wrapper generation
// captured when its prompt/router was admitted. The provider call is bounded
// and asynchronous. cancelIssued
// closes only after it returns, allowing a takeover to order its next Prompt
// behind this request without blocking the initiating API call.
func (s *chatServiceImpl) requestGenerationCancellation(sessionID string, gen *inFlightGen) {
	if gen == nil {
		return
	}
	gen.cancelOnce.Do(func() {
		if gen.cancelIssued == nil {
			gen.cancelIssued = make(chan struct{})
		}
		gen.turnMu.Lock()
		gen.cancelAsked = true
		binding := gen.turn
		gen.turnMu.Unlock()
		if gen.cancel != nil {
			gen.cancel()
		}
		if binding != nil && binding.router != nil {
			if s.agentEventBridge != nil {
				s.agentEventBridge.ReleasePerSessionRouter(sessionID, binding.router)
			} else {
				binding.router.closeOnce()
			}
		}
		request := func(ownerCtx context.Context) {
			defer close(gen.cancelIssued)
			var sess *runtimeagent.Session
			if binding != nil {
				sess = binding.session
			}
			if sess == nil {
				gen.turnMu.Lock()
				gen.cancelSafe = true
				gen.turnMu.Unlock()
				return
			}
			cancelCtx, cancel := context.WithTimeout(ownerCtx, runtimeTurnCancelMaxWait)
			err := s.runtimeSessions().CancelSession(cancelCtx, sess)
			safe := false
			switch {
			case err == nil:
				// ACP Cancel acknowledges the request before the terminal turn
				// event necessarily arrives. Do not release a successor Prompt
				// until wrapper's authoritative state leaves Processing.
				safe = sess.WaitTurnTerminal(cancelCtx) == nil
			case errors.Is(err, runtimeagent.ErrTurnCancelUnsupported):
				// Native runtimes truthfully have no turn cancel. Stop this exact
				// wrapper so takeover cold-boots instead of racing a second turn.
				stopErr := sess.Stop(cancelCtx)
				waitErr := sess.Wait(cancelCtx)
				safe = !errors.Is(waitErr, context.Canceled) && !errors.Is(waitErr, context.DeadlineExceeded)
				if stopErr != nil && !safe {
					err = errors.Join(err, stopErr)
				}
			}
			cancel()
			if !safe && ownerCtx.Err() == nil {
				stopCtx, stopCancel := context.WithTimeout(ownerCtx, runtimeTurnCancelMaxWait)
				stopErr := sess.Stop(stopCtx)
				waitErr := sess.Wait(stopCtx)
				stopCancel()
				safe = !errors.Is(waitErr, context.Canceled) && !errors.Is(waitErr, context.DeadlineExceeded)
				if !safe && !errors.Is(stopErr, context.Canceled) && !errors.Is(stopErr, context.DeadlineExceeded) {
					slog.Warn("chat-service: cancel runtime turn could not establish terminal boundary",
						"session_id", sessionID, "cancel_err", err, "stop_err", stopErr, "wait_err", waitErr)
				}
			}
			gen.turnMu.Lock()
			gen.cancelSafe = safe
			gen.turnMu.Unlock()
		}
		if s.lifecycle != nil {
			s.lifecycle.Go("cancel-runtime-turn", request)
			return
		}
		go request(context.Background())
	})
}

// CancelActiveGeneration cancels the in-flight generateResponse goroutine
// registered for sessionID, if any. Returns true when a cancel was
// dispatched (the goroutine will observe ctx.Err() on its next loop
// iteration and exit cleanly), false when no generation was active.
// CW-20260512-0006: this is the user-stop endpoint backstop now that the
// 5-minute wall-clock deadline has been removed. The registry slot is
// NOT cleared here — deregisterGeneration handles that when the canceled
// goroutine returns, preserving the takeover semantics in
// registerGeneration.
func (s *chatServiceImpl) CancelActiveGeneration(sessionID string) bool {
	s.activeGenMu.Lock()
	cur, ok := s.activeGen[sessionID]
	s.activeGenMu.Unlock()
	if !ok || cur == nil {
		return false
	}
	s.requestGenerationCancellation(sessionID, cur)
	return true
}

// launchGeneration starts a cancellable generateResponse goroutine for the
// given target session. If another generateResponse is already running for
// this session, it is canceled first — prevents concurrent duplicate loops
// when the user retries mid-stream (CW-20260418-0043).
//
// The cancel is wired into both our per-session registry (for takeover) and
// the lifecycle manager (for graceful Shutdown) via a small bridge goroutine.
//
// CW-20260512-0121 (SP-20260512-0011): the actual runner invocation is
// routed through the single dispatcher door (s.dispatcher.Run). Per-site
// assembly is gone — the dispatcher stamps CallerChat on ctx so the
// runner's request_build telemetry reports caller=chat. Dispatcher
// validation errors are programmer-only failure modes (empty SessionID,
// invalid CallerType, etc.); the goroutine logs them and closes the
// stream channel so the caller's stream-drain unblocks. launchGeneration
// itself returns void and has already returned to the caller by the
// time the goroutine runs — dispatcher errors never surface synchronously.
func (s *chatServiceImpl) launchGeneration(name, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent, callerType dispatcher.CallerType) {
	// Build cancel BEFORE launching so a near-simultaneous retry cannot
	// register its own cancel before this one — the window would let the
	// retry cancel itself. context.Background() is deliberate: lifecycle
	// shutdown is bridged in below.
	genCtx, cancel := context.WithCancel(context.Background())

	prev, current := s.registerGeneration(sessionID, assistantMsgID, cancel)
	if prev != nil {
		slog.Info("chat-service: canceling prior in-flight generation for session",
			"session_id", sessionID, "new_msg_id", assistantMsgID)
		s.requestGenerationCancellation(sessionID, prev)
	}

	s.runGeneration(name, sessionID, assistantMsgID, userContent, ch, callerType, genCtx, cancel, current, prev)
}

// runGeneration is the shared goroutine body launchGeneration (takeover)
// and TriggerHarnessTurn's reject-if-busy path both dispatch through —
// registration in the activeGen map has already happened by the time this
// is called; this only owns running the turn and cleaning up afterward.
func (s *chatServiceImpl) runGeneration(name, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent, callerType dispatcher.CallerType, genCtx context.Context, cancel context.CancelFunc, current, predecessor *inFlightGen) {
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
		defer s.deregisterGeneration(sessionID, current)
		if current != nil && current.done != nil {
			defer close(current.done)
		}

		// A takeover must not race its Prompt ahead of the exact predecessor's
		// CancelTurn request or terminal return. Native wrappers truthfully
		// report unsupported cancellation, so this naturally waits for their
		// turn to finish instead of pretending the process was interrupted.
		if !waitForPredecessor(genCtx, bgCtx, predecessor) {
			close(ch)
			return
		}

		// CW-20260512-0121: route through the single dispatcher door.
		// On dispatcher validation failure (programmer error — should
		// be unreachable in production), close the channel so the
		// caller's defer doesn't deadlock waiting for a stream that
		// will never come.
		dispatchCtx := context.WithValue(genCtx, inFlightGenContextKey{}, current)
		if err := s.dispatcher.Run(dispatchCtx, dispatcher.Request{
			SessionID:      sessionID,
			AssistantMsgID: assistantMsgID,
			UserContent:    userContent,
			CallerType:     callerType,
		}, ch); err != nil {
			slog.Error("chat-service: dispatcher.Run rejected request",
				"session_id", sessionID,
				"assistant_msg_id", assistantMsgID,
				"err", err,
			)
			// Runner did not run, so it did not close ch — do so here
			// to release the caller. Mirrors the defer-close contract
			// the runner would have honored.
			close(ch)
		}
	})
}

func waitForPredecessor(genCtx, ownerCtx context.Context, predecessor *inFlightGen) bool {
	if predecessor == nil {
		return true
	}
	for _, barrier := range []<-chan struct{}{predecessor.cancelIssued, predecessor.done} {
		if barrier == nil {
			continue
		}
		select {
		case <-barrier:
		case <-ownerCtx.Done():
			return false
		}
	}
	predecessor.turnMu.Lock()
	safe := predecessor.cancelSafe
	predecessor.turnMu.Unlock()
	if predecessor.cancelIssued != nil && !safe {
		return false
	}
	return genCtx.Err() == nil
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
	if err := s.store.CreateMessage(ctx, userMsg); err != nil {
		return "", fmt.Errorf("create user message: %w", err)
	}

	// Trust-agent path-mention parser (CW-20260430-0009 Q1-Q3). Scan the
	// user's message for strict-prefix path tokens (^~/, ^/, ^./) and
	// register session-scoped grants for the literal path AND its parent
	// directory. Loose patterns and tool names do NOT auto-grant — those
	// fall through to the notify-pause path.
	if s.pathGrants != nil {
		granted := s.pathGrants.RegisterFromUserMessage(sessionID, content)
		if len(granted) > 0 {
			// INFO carries a count only — the granted slice contains user
			// filesystem paths from chat input and shouldn't land in
			// production logs. DEBUG sibling carries the full list for
			// opt-in diagnostics.
			slog.Info("permission: explicit-mention grants registered",
				"session_id", sessionID, "count", len(granted))
			slog.Debug("permission: explicit-mention grants registered (paths)",
				"session_id", sessionID, "paths", granted)
		}
	}

	// Emit message.sent plugin event with the real user message ID.
	if s.pluginHost != nil {
		s.goTracked("emit.message-sent", func(context.Context) {
			s.pluginHost.EmitMessageSent(sessionID, userMsg.ID, content, "user", 0)
		})
	}

	// Create assistant message ID and stream.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, sessionID)

	// Start async generation on a per-session cancellable ctx. If a prior
	// generateResponse is still running for this session it will be
	// canceled — concurrent loops on the same session fight over the
	// provider rate-limit budget and look like stalls from the UI
	// (CW-20260418-0043). The lifecycle manager's shutdown ctx is bridged
	// inside launchGeneration so process Shutdown still drains cleanly.
	//
	// HandleMessage is shared by two real callers with two different
	// correct CallerType values: internal/api/harness_v1.go and
	// internal/api/messages.go (real end-user HTTP handlers — always
	// CallerChat, and they stamp nothing on ctx) and
	// chatDurableAgentRuntimeController.SendMessage (a durable agent's
	// scheduled wake delivery — background work, not a user typing into
	// chat, so it stamps dispatcher.CallerBackground onto ctx before
	// calling in). Prefer whatever valid CallerType arrives on ctx and
	// fall back to CallerChat — mirrors the existing ambient-ctx +
	// documented-fallback convention chat_generate.go's request_build
	// slog already uses for dispatcher.CallerTypeFromContext, except the
	// fallback here must be a *valid* CallerType (not "unknown") because
	// this value is actually dispatched, not just logged — Dispatcher.Run
	// rejects an empty/invalid CallerType outright.
	callerType := dispatcher.CallerChat
	if ct := dispatcher.CallerTypeFromContext(ctx); ct.Valid() {
		callerType = ct
	}
	s.launchGeneration("handleMessage.generateResponse", sessionID, assistantMsgID, content, ch, callerType)

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
			if ap, ok := prov.(*nllmanthropic.Client); ok && ap.CircuitBreaker != nil {
				ap.CircuitBreaker.Reset()
				slog.Info("chat-service: circuit breaker reset for retry", "session_id", sessionID)
			}
		}
	}

	// Find the last user message.
	msgs, err := s.store.ListMessages(ctx, sessionID, 50)
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

	s.launchGeneration("retryLastMessage.generateResponse", sessionID, assistantMsgID, userContent, ch, dispatcher.CallerChat)

	return assistantMsgID, nil
}

// SendAgentMessage implements ChatService.
func (s *chatServiceImpl) SendAgentMessage(ctx context.Context, fromSessionID, toSessionID, content string) (string, error) {
	// Look up the sending agent.
	fromAgentID := "unknown"
	if agent, err := s.agents.ResolveForSession(ctx, fromSessionID); err == nil {
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
	if err := s.store.CreateMessage(ctx, msg); err != nil {
		return "", fmt.Errorf("create agent message: %w", err)
	}

	// Start async generation in the target session.
	assistantMsgID := uuid.New().String()
	ch := s.streams.CreateStream(assistantMsgID, toSessionID)

	s.launchGeneration("sendAgentMessage.generateResponse", toSessionID, assistantMsgID, content, ch, dispatcher.CallerChat)

	return assistantMsgID, nil
}

// IsGenerating reports whether a generateResponse goroutine is currently
// registered for sessionID. Exported read-only wrapper around
// hasActiveGeneration (CW-20260520-0001) — the Layer-2 completion reactor
// uses this to skip triggering a harness turn while the session already
// has a turn in flight ("backoff-aware: do not trigger during an active
// in-flight turn"). The turn-start injection (CW-20260512-0019) remains
// the delivery guarantee for whatever turn is already running or comes
// next.
func (s *chatServiceImpl) IsGenerating(sessionID string) bool {
	return s.hasActiveGeneration(sessionID)
}

// TriggerHarnessTurn enqueues a harness-initiated turn on sessionID — the
// "react" half of CW-20260520-0001's emit→react model. Unlike
// SendAgentMessage (a genuine agent-to-agent message), this synthesizes a
// short instruction prompt; the actual subagent-result content reaches the
// model via the CW-20260512-0019 turn-start injection, not via this
// message body, so it isn't duplicated here. A sibling to SendAgentMessage
// rather than a modification of it — SendAgentMessage's signature has its
// own callers (internal/api/messages.go) this must not disturb.
//
// Reject-if-busy, not takeover: the caller (subagentCompletionReactor)
// already checks IsGenerating() before calling this, but that check and
// this call are not atomic — a real user turn can start in between. Using
// launchGeneration's takeover semantics here would let that race silently
// cancel the user's in-flight generation (PR #247 review). Registering via
// registerGenerationIfIdle BEFORE creating the message/stream closes the
// window: on a losing race this returns ErrSessionBusy and creates
// nothing, leaving the turn-start injection (CW-20260512-0019) as the
// delivery guarantee once the user's turn — or the next one — runs.
//
// The created message and the CallerBackground caller-type stamp (rather
// than CallerChat) are how a harness-triggered turn identifies itself
// downstream — request_build telemetry, the idle-timeout window
// (chat_loop_state.go), and any future per-CallerType behavior all key off
// this. A session_events row (EventHarnessTriggeredTurn) is also written
// so audits can distinguish user-initiated from harness-initiated turns
// (the ticket's "triggered_by provenance marker" acceptance criterion) —
// the reason and runID travel in that row's payload since the message
// row's Metadata JSON is prose-only ("source":"harness" is enough there
// to keep it out of user-authored-message heuristics).
func (s *chatServiceImpl) TriggerHarnessTurn(ctx context.Context, sessionID, reason, runID string) (string, error) {
	assistantMsgID := uuid.New().String()
	genCtx, cancel := context.WithCancel(context.Background())
	current, registered := s.registerGenerationIfIdle(sessionID, assistantMsgID, cancel)
	if !registered {
		cancel()
		return "", ErrSessionBusy
	}

	content := "A dispatched subagent has completed. Review the result below and summarize it for the user, noting any concerns."
	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		Metadata:  fmt.Sprintf(`{"source":"harness","triggered_by":%q,"run_id":%q}`, reason, runID),
	}
	if err := s.store.CreateMessage(ctx, msg); err != nil {
		s.deregisterGeneration(sessionID, current)
		cancel()
		return "", fmt.Errorf("create harness-triggered message: %w", err)
	}

	ch := s.streams.CreateStream(assistantMsgID, sessionID)

	s.runGeneration("triggerHarnessTurn.generateResponse", sessionID, assistantMsgID, content, ch, dispatcher.CallerBackground, genCtx, cancel, current, nil)

	if s.sessionEventWriter != nil {
		payload := fmt.Sprintf(`{"triggered_by":%q,"run_id":%q,"assistant_msg_id":%q}`, reason, runID, assistantMsgID)
		s.sessionEventWriter.WriteSessionEvent(ctx, sessionID, EventHarnessTriggeredTurn, "", payload)
	}

	return assistantMsgID, nil
}

// TriggerMessageWake enqueues a harness-initiated turn on sessionID in
// reaction to an inbound go-messaging/mailbox A2A message — the
// CW-20260816-0065 sibling to TriggerHarnessTurn (subagent completions)
// and SendAgentMessage (direct agent-to-agent sends), called by
// messagingWakeReactor (messaging_reactor.go) rather than invented at a
// new call site.
//
// Unlike TriggerHarnessTurn's synthetic "review the subagent result"
// prompt — whose actual content reaches the model via the
// kind=subagent_result turn-start injection (CW-20260512-0019), which
// generic A2A messages are NOT eligible for (that injection filters
// strictly on Kind) — this passes the arriving message's own body
// directly as the turn's content, matching SendAgentMessage's
// content-bearing approach. Without this, the recipient would wake to a
// prompt referencing a message it has no way to see.
//
// Reject-if-busy, not takeover, for the same reason TriggerHarnessTurn
// uses it (PR #247 review): an inbound peer message must never cancel a
// real user turn already in flight. registerGenerationIfIdle closes the
// TOCTOU window between messagingWakeReactor's IsGenerating pre-check
// and this call. On a losing race this returns ErrSessionBusy and
// creates nothing — the message is still durably in the recipient's
// inbox (message_inbox/message_thread) either way, so nothing is lost,
// only the proactive nudge is skipped.
func (s *chatServiceImpl) TriggerMessageWake(ctx context.Context, sessionID string, msg *messaging.Message) (string, error) {
	assistantMsgID := uuid.New().String()
	genCtx, cancel := context.WithCancel(context.Background())
	current, registered := s.registerGenerationIfIdle(sessionID, assistantMsgID, cancel)
	if !registered {
		cancel()
		return "", ErrSessionBusy
	}

	content := fmt.Sprintf("New message from agent %q (session %s):\n\n%s", msg.FromAgentID, msg.FromSessionID, msg.Body)
	newMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		AgentID:   msg.FromAgentID,
		Role:      "user",
		Content:   content,
		Metadata:  fmt.Sprintf(`{"source":"agent_message","from_session":%q,"from_agent":%q,"message_id":%q}`, msg.FromSessionID, msg.FromAgentID, msg.ID),
	}
	if err := s.store.CreateMessage(ctx, newMsg); err != nil {
		s.deregisterGeneration(sessionID, current)
		cancel()
		return "", fmt.Errorf("create message-wake turn: %w", err)
	}

	ch := s.streams.CreateStream(assistantMsgID, sessionID)

	s.runGeneration("triggerMessageWake.generateResponse", sessionID, assistantMsgID, content, ch, dispatcher.CallerBackground, genCtx, cancel, current, nil)

	if s.sessionEventWriter != nil {
		payload := fmt.Sprintf(`{"triggered_by":"a2a_message","message_id":%q,"assistant_msg_id":%q}`, msg.ID, assistantMsgID)
		s.sessionEventWriter.WriteSessionEvent(ctx, sessionID, EventHarnessTriggeredTurn, "", payload)
	}

	return assistantMsgID, nil
}

// GetStream implements ChatService.
func (s *chatServiceImpl) GetStream(messageID string) (<-chan chat.StreamEvent, bool) {
	return s.streams.GetStream(messageID)
}

// Shutdown implements ChatService. It kills tracked CLI processes, drains
// active runtime sessions, and then cancels the service's lifecycle
// context, waiting up to chatShutdownMaxWait for in-flight goroutines to
// exit. Any incomplete drain is logged and returned so the container can
// decline plugin unload; see lifecycle.ShutdownTimeoutError.
func (s *chatServiceImpl) Shutdown() error {
	return s.shutdownWithMaxWait(chatShutdownMaxWait)
}

func (s *chatServiceImpl) shutdownWithMaxWait(maxWait time.Duration) error {
	var shutdownErrs []error
	sessions := s.runtimeSessions()
	if sessions != nil {
		sessions.CloseAdmission()
	}
	if s.processTracker != nil {
		s.processTracker.KillAll()
	}
	// Session admission is already closed above. Capture and cancel exact
	// runtime generations before SessionManager stops them; lifecycle shutdown
	// then drains all generation and bounded CancelTurn work.
	s.activeGenMu.Lock()
	active := make(map[string]*inFlightGen, len(s.activeGen))
	for sessionID, gen := range s.activeGen {
		active[sessionID] = gen
	}
	s.activeGenMu.Unlock()
	for sessionID, gen := range active {
		s.requestGenerationCancellation(sessionID, gen)
	}
	if sessions != nil {
		ctx, cancel := context.WithTimeout(context.Background(), maxWait)
		if err := sessions.Shutdown(ctx); err != nil {
			slog.Warn("chat-service: agent sessions shutdown", "err", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("agent sessions shutdown: %w", err))
		}
		cancel()
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.Shutdown(maxWait); err != nil {
			slog.Warn("chat-service: lifecycle shutdown", "err", err)
			shutdownErrs = append(shutdownErrs, fmt.Errorf("lifecycle shutdown: %w", err))
		}
	}
	return errors.Join(shutdownErrs...)
}

// SetWorkers injects the worker manager after construction to break the
// circular dependency (ChatService <-> WorkerManager).
func (s *chatServiceImpl) SetWorkers(w *worker.Manager) {
	s.workers = w
}

// CloseAgentSession stops + drops any long-lived runtime session bound to
// the supplied chat session id. Phase 4c.8 (CW-20260508-0002): wired as the
// SessionService archive hook so closing a chat session releases the
// underlying claude-code (or other CLI) PTY process immediately instead of
// waiting for the IdleKill=15min supervisor timeout.
//
// Idempotent. nil-safe when the runtime is not wired (no-op).
func (s *chatServiceImpl) CloseAgentSession(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	sess, ok := s.runtimeSessions().LoadAndDelete(sessionID)
	if !ok {
		return
	}
	s.activeSessionSlots.Delete(sessionID)
	s.toolPartitionStates.Delete(sessionID)
	s.activeSessionContextBlocks.Delete(sessionID)
	if s.agentEventBridge != nil {
		s.agentEventBridge.SetPerSessionRouter(sessionID, nil)
	}
	if s.agentBootDirAdapter != nil {
		s.agentBootDirAdapter.Untrack(sessionID)
	}
	if err := sess.Stop(ctx); err != nil {
		slog.Warn("chat-service: CloseAgentSession Stop", "session_id", sessionID, "err", err)
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// resolveProvider reads the role->agent->task composition cascade's
// already-resolved provider as its primary path (architecture/
// 02-agent-launching.md: "Once an agents row has a real, cascade-resolved
// model_id, provider/model resolution is 'read the already-resolved
// value,' not a second independent walk"). agentProvider arrives here as
// agent.DefaultProvider from chat_generate.go's
// s.agents.ResolveForSession(...) call, which already ran
// applyScalarCascade/ResolveAgentCascade (Phase 1 item 01) before
// returning — reading it here is not a second, independent lookup, it's
// consuming that cascade's output.
//
// Phase 3 item 01 (this task, TASKS/phase-3/01-collapse-resolveprovider-
// into-cascade.md) folded runtimeKind into the walk itself, closing a
// real, confirmed bug found during Phase 2's close-out review: this
// function used to have zero awareness of agent.RuntimeKind, so a
// runtime_kind='cli' agent configured with a bare (non-"pty-"/"sub-"-
// prefixed) default_provider — e.g. "claude" instead of the legacy
// "pty-claude" alias shape — could walk all the way to a real, registered
// HTTP provider (via user_settings.default_provider, the fallback chain,
// or chat.InferProvider(model)) and never reach chat_generate.go's
// classifyNilProvider at all, where runtime_kind is otherwise consulted.
// The turn then silently routed through the HTTP/API path with no error.
//
// Fixed by making runtimeKind=="cli" authoritative and checked once, per
// architecture/02-agent-launching.md's "CLI-vs-API routing is an explicit
// typed field" design: as soon as the session-level or cascade-resolved
// (agent-level) candidate is known to not already be CLI-shaped, a cli
// runtime commits to a (name, nil) result right there and never walks
// into steps 3/4/5 below — none of which have any way to produce a
// CLI-safe result, since they only ever resolve names that are either
// operator-configured HTTP providers or InferProvider's HTTP-provider
// routing floor. See chat_resolve_provider_test.go's
// TestResolveProvider_CLIRuntimeKind_BareDefaultProvider_NeverRoutesHTTP
// for the literal reproduction of the pre-fix bug.
//
// Resolution order:
//  1. sessionProvider (explicit per-session override — narrowest,
//     task/invocation-shaped tier)
//  2. agentProvider (cascade-resolved composition default — primary path
//     once Phase 1's cascade is populated; today still "" for every
//     pre-existing row, since none have role_id/model_id/a non-empty
//     default_provider set — see this task's Work Log for the real-data
//     check)
//  3. user_settings.default_provider (operator-level installation-wide
//     default — NOT part of the role->agent->task composition cascade;
//     kept as a narrow, still-load-bearing fallback tier rather than
//     declared dead, because every one of the 28 real rows in the
//     production DB backup resolves through this step today. See Work
//     Log for the full rationale.)
//  4. user_settings.ProviderFallbackChain (resilience list, same tier as 3)
//  5. chat.InferProvider(model) — the genuine "no resolvable composition
//     anywhere" floor
//
// Steps 3-5 are UNREACHABLE whenever runtimeKind=="cli" — see above.
//
// CW-20260812-0001 investigation: step 3 was missing entirely before that
// fix. user_settings.default_provider was never consulted here — it's a
// separate resolver used only for the *model* dimension elsewhere (see
// store.ResolveProviderAndModel in chat_generate.go). A session with no
// explicit provider fell straight through to step 4
// (ProviderFallbackChain), so a stale/legacy CLI-shaped entry left there
// from before this app's API-first default (e.g. "pty") won over the
// operator's real default_provider setting — silently routing ordinary
// API sessions into a CLI boot attempt that then crashed downstream
// (bootdir for provider "" — see applyLegacyCLIProviderToBootOpts).
//
// The unconditional "if all else fails, try anthropic" catch-all that
// used to follow step 5 has been removed: a fully unconfigured
// installation (no provider anywhere in the chain, and no model-name
// match) now surfaces a clear configuration error instead of silently
// defaulting — the (name, nil) + non-CLI-shaped terminal case below
// already makes chat_generate.go's classifyNilProvider return
// nilProviderRouteFatal, which the caller turns into "Provider %q not
// available — check configuration and restart the server."
//
// When a preferred provider is unavailable and the chain falls through,
// a provider.fallback plugin event is emitted.
//
// CLI providers ("pty", "pty-*", "sub-*") intentionally have no
// llmcontracts.Provider registered after Phase 4c.6 (CW-20260508-0002) —
// their turns are driven by internal/runtime/agent.Boot, not StreamChat.
// At every registry-miss point below, if the requested name is a CLI
// alias we return (name, nil) so chat_generate's classifyNilProvider can
// route the turn to driveBootSession. Without these guards, a CLI
// session silently inherits the HTTP fallback (Anthropic) and the API
// rejects the CLI model id with a 404 (c195 regression).

// tryProviderCandidate checks one candidate provider name during
// resolveProvider's fallback walk. A registry hit returns (name, provider,
// true); a registry miss on a CLI-shaped name returns (name, nil, true) so
// the caller can route to the CLI boot path; any other miss logs warnMsg
// and returns ("", nil, false) so the caller falls through to the next
// candidate. requested is the originally-requested provider (for the
// session or agent step); when the resolved name differs from it, a
// provider.fallback plugin event is emitted before returning.
//
// Only ever called from resolveProvider's steps 3/4 (operator-level
// user_settings tiers) after this task's rewrite — those steps are
// structurally unreachable once runtimeKind=="cli" is known (see
// resolveProvider's doc comment), so this helper itself doesn't need its
// own runtimeKind parameter: by the time it can run, the caller has
// already confirmed the current turn is not CLI-authoritative.
func (s *chatServiceImpl) tryProviderCandidate(sessionID, requested, name, warnMsg string) (string, llmcontracts.Provider, bool) {
	if p, ok := s.providers.Get(name); ok {
		if requested != "" && requested != name && s.pluginHost != nil {
			s.pluginHost.EmitProviderFallback(sessionID, requested, name)
		}
		return name, p, true
	}
	if chat.IsCLIProvider(name) {
		if requested != "" && requested != name && s.pluginHost != nil {
			s.pluginHost.EmitProviderFallback(sessionID, requested, name)
		}
		return name, nil, true
	}
	slog.Warn(warnMsg, "provider", name)
	return "", nil, false
}

func (s *chatServiceImpl) resolveProvider(sessionID, sessionProvider, agentProvider, model, runtimeKind string) (string, llmcontracts.Provider) {
	// Track the first requested provider so we can emit a fallback event
	// when a later candidate is selected instead.
	requested := sessionProvider
	if requested == "" {
		requested = agentProvider
	}

	// cliRuntime is authoritative once true: this turn's session-bound
	// agent has agents.runtime_kind='cli' (architecture/
	// 02-agent-launching.md's "CLI-vs-API routing is an explicit typed
	// field" — one typed field, checked once, not re-derived from string
	// shape at every step). See this function's doc comment for the real
	// bug this closes.
	cliRuntime := runtimeKind == "cli"

	// --- Step 1: sessionProvider (explicit per-session override) ---
	if sessionProvider != "" {
		runtimeProvider := sessionProvider
		if stored, err := s.store.GetProvider(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionProvider); err == nil && stored != nil && stored.ProviderType != "" {
			runtimeProvider = stored.ProviderType
		}
		if chat.IsCLIProvider(runtimeProvider) {
			return runtimeProvider, nil
		}
		if cliRuntime {
			// A non-CLI-shaped explicit session provider must not win
			// over an authoritatively-cli agent — probing the HTTP
			// registry here would risk exactly the bug this task fixes.
			// Fall through to the cascade-resolved (agent) tier instead
			// of resolving a real HTTP provider for this session.
			slog.Warn("chat-service: session provider is not CLI-shaped but agent runtime_kind is cli, ignoring in favor of the cascade-resolved provider",
				"provider", sessionProvider, "runtime_provider", runtimeProvider)
		} else if p, ok := s.providers.Get(runtimeProvider); ok {
			return runtimeProvider, p
		} else {
			slog.Warn("chat-service: session provider not registered, falling through",
				"provider", sessionProvider, "runtime_provider", runtimeProvider)
		}
	}

	// --- Step 2: agentProvider (cascade-resolved composition default —
	// primary path; see doc comment above) ---
	if agentProvider != "" {
		if chat.IsCLIProvider(agentProvider) {
			if requested != "" && requested != agentProvider && s.pluginHost != nil {
				s.pluginHost.EmitProviderFallback(sessionID, requested, agentProvider)
			}
			return agentProvider, nil
		}
		if cliRuntime {
			// The real, confirmed bug this task fixes: a bare (non-
			// "pty-"/"sub-"-prefixed) cascade-resolved default_provider
			// on a runtime_kind='cli' agent must still route CLI, not
			// fall through to tryProviderCandidate's registry probe
			// below (which is exactly what silently produced a real
			// HTTP provider before this fix).
			return agentProvider, nil
		}
		if name, p, ok := s.tryProviderCandidate(sessionID, requested, agentProvider,
			"chat-service: agent provider not registered, falling through"); ok {
			return name, p
		}
	}

	if cliRuntime {
		// runtime_kind='cli' is still authoritative even when neither
		// step above produced a CLI-shaped name (e.g. agentProvider=="",
		// or it missed IsCLIProvider and got intercepted above). Commit
		// to the CLI route here rather than falling through to the
		// operator-level tiers below (3/4/5), none of which can ever
		// resolve to a value safe to hand back as a non-CLI-shaped
		// (name, nil): those tiers only ever produce HTTP-registered
		// provider names.
		name := agentProvider
		if name == "" {
			name = sessionProvider
		}
		return name, nil
	}

	// --- Steps 3/4: user_settings.default_provider / ProviderFallbackChain
	// — operator-level, installation-wide floor. Not part of the
	// role->agent->task composition cascade (roles/agent_profiles have no
	// equivalent of this — it's a single global preference, not a
	// per-role/per-agent value), but deliberately kept as a real,
	// documented fallback rather than declared dead: see this task's Work
	// Log for the real production-data check that every one of the 28
	// existing agent rows resolves through this tier today (all have
	// default_provider=""), so removing it would be a behavior change,
	// not a resolution-mechanism swap. ---
	if us, err := s.store.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */); err == nil {
		if us.DefaultProvider != "" {
			if name, p, ok := s.tryProviderCandidate(sessionID, requested, us.DefaultProvider,
				"chat-service: user_settings.default_provider not registered, falling through"); ok {
				return name, p
			}
		}

		for _, name := range us.ProviderFallbackChain {
			if resolvedName, p, ok := s.tryProviderCandidate(sessionID, requested, name,
				"chat-service: fallback-chain provider not registered, skipping"); ok {
				return resolvedName, p
			}
		}
	}

	// --- Step 5: chat.InferProvider(model) — the genuine "no resolvable
	// composition anywhere" floor. ---
	inferred := chat.InferProvider(model)
	if p, ok := s.providers.Get(inferred); ok {
		if requested != "" && requested != inferred && s.pluginHost != nil {
			s.goTracked("emit.provider-fallback-inferred", func(context.Context) {
				s.pluginHost.EmitProviderFallback(sessionID, requested, inferred)
			})
		}
		return inferred, p
	}
	if chat.IsCLIProvider(inferred) {
		if requested != "" && requested != inferred && s.pluginHost != nil {
			s.goTracked("emit.provider-fallback-inferred", func(context.Context) {
				s.pluginHost.EmitProviderFallback(sessionID, requested, inferred)
			})
		}
		return inferred, nil
	}

	// No unconditional "try anthropic" catch-all: an installation with
	// nothing configured anywhere in the chain must surface a
	// configuration error, not a silent default. The caller
	// (chat_generate.go's classifyNilProvider) turns this terminal
	// non-CLI-shaped (name, nil) into "Provider %q not available — check
	// configuration and restart the server."
	return inferred, nil
}

// nilProviderRoute classifies what to do when resolveProvider returns a
// nil llmcontracts.Provider. CW-20260514-0045: introduced for the CLI
// dropdown-route fix so the chat_generate.go nil-provider handler can
// surface a distinct error for each outcome without an inline
// three-way conditional. Tested directly in chat_generate_cli_bypass_test.go.
type nilProviderRoute int

const (
	// nilProviderRouteFatal is the legacy path: the provider name is not
	// a CLI alias, so the lack of a registered provider is a real
	// misconfiguration. Emits the original "Provider not available"
	// error and aborts.
	nilProviderRouteFatal nilProviderRoute = iota
	// nilProviderRouteCLI signals the chat-harness loop to fall through
	// to driveBootSession (CW-20260508-0002 / Phase 4c.6 architecture).
	// The provider name passes IsCLIProvider AND the agent runtime has
	// an adapter registered for it; we don't need an llmcontracts.Provider
	// because the CLI path doesn't go through Provider.StreamChat.
	nilProviderRouteCLI
	// nilProviderRouteCLINoAdapter is the case where the dropdown sent
	// a CLI alias but no runtime adapter is registered (e.g. dev forgot
	// to wire CLIAdapters into ContainerConfig). Emits a CLI-specific
	// error so the operator gets a pointed message instead of the
	// generic "Provider not available" footer.
	nilProviderRouteCLINoAdapter
)

// classifyNilProvider returns the nilProviderRoute case for a resolved
// (providerName, nil-provider) pair — i.e. the CLI bypass chat_generate.go
// hits when resolveProvider comes back with no registered
// llmcontracts.Provider. Returns nilProviderRouteFatal when CLI routing
// isn't warranted (see below) — the original behavior. Returns
// nilProviderRouteCLI when CLI routing IS warranted AND the agent runtime
// has a registered adapter for the resolved name. Returns
// nilProviderRouteCLINoAdapter when CLI routing is warranted but no
// adapter is registered (misconfiguration).
//
// Phase 2 item 01 (TASKS/phase-2/01-wire-runtime-kind-routing.md): CLI
// routing is decided primarily by runtimeKind — agent_profiles.runtime_kind
// on the resolved session agent (architecture/02-agent-launching.md, "CLI-
// vs-API routing is an explicit typed field"), not by re-deriving the
// classification from providerName's "pty"/"sub-" prefix shape. The
// chat.IsCLIProvider(providerName) check is still OR'd in, deliberately,
// for one remaining case where runtimeKind is not yet the reliable single
// source of truth: a file-discovered agent profile with no agent_profiles
// DB row yet — Definition.ToProfile() has no frontmatter representation
// for runtime_kind at all, so runtimeKind arrives here as "". Falling back
// to the legacy provider-name classification reproduces prior behavior
// exactly. TASKS/adhoc/01-eliminate-file-based-agent-runtime.md removed
// the in-memory-registry resolution path that used to produce a "no DB row
// yet" resolved session agent at all (every agent, including the 9
// internal builtin profiles, is DB-backed by the time ResolveForSession
// returns one) -- this OR is very likely fully dead now too, but left
// untouched here since chat routing is outside this task's own scope; a
// future cleanup pass can confirm and remove it.
//
// TASKS/phase-2/04-retire-boot-profile-catalog.md removed the second case
// this OR used to cover — the boot-profile catalog's own
// cliRoutableProvider, which synthesized a "pty-<adapter>" alias to force
// CLI routing independent of the session's bound agent. That whole
// mechanism (and its provider-id encoding) is gone; this OR now exists
// solely for the file-discovered-agent case above.
//
// Adapter lookup goes through agentDeps.ProviderAdapter which already
// applies the CW-20260514-0045 alias normalization (stripRegistryPrefix
// → chat.NormalizeCLIProvider), so the caller passes the dropdown-shape
// name verbatim.
func (s *chatServiceImpl) classifyNilProvider(runtimeKind, providerName string) nilProviderRoute {
	if runtimeKind != "cli" && !chat.IsCLIProvider(providerName) {
		return nilProviderRouteFatal
	}
	if s.agentDeps == nil || s.agentDeps.ProviderAdapter == nil {
		// CLI-routable but no runtime composition wired —
		// behave as fatal so the operator sees the legacy error.
		return nilProviderRouteFatal
	}
	if s.agentDeps.ProviderAdapter(providerName) == nil {
		return nilProviderRouteCLINoAdapter
	}
	return nilProviderRouteCLI
}
