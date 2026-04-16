package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	conduit "github.com/hollis-labs/vanta-conduit"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/builtin"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/memory"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service/a2a"
	"github.com/hollis-labs/nanite/internal/skill"
	skillbuiltin "github.com/hollis-labs/nanite/internal/skill/builtin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/task"
	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/tool/stash"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/internal/worker"
	"github.com/hollis-labs/nanite/internal/workflow"
	"github.com/hollis-labs/nanite/internal/worktree"
	"github.com/hollis-labs/nanite/pkg/models"
)

// Container holds all service instances and shared subsystems. It is the
// single wiring point — created once in main.go and passed to the API layer.
type Container struct {
	Sessions  SessionService
	Agents    AgentService
	Skills    SkillService
	Tools     ToolService
	Chat      ChatService
	Context   ContextService
	Streams   *StreamManager
	Events    EventEmitter
	Providers *provider.Registry
	Commands  *chat.CommandRegistry
	Plugins   *plugin.Host
	MCP       *mcp.Manager

	// A2A messaging service — validates, persists, and fans out
	// agent-to-agent messages plus handoff state transitions.
	A2A *a2a.Service

	// Internal todo/plan system.
	Todos TodoService

	// Memory system (embedded Conduit).
	Conduit *conduit.Conduit
	Memory  *memory.Service
	// EmbeddingStatus is the resolved state of the embedder at container build
	// time: "active" | "disabled" | "missing_credentials" | "unreachable".
	// Surfaced by the settings API and consumed by the first-turn warning.
	EmbeddingStatus   string
	EmbeddingProvider string
	EmbeddingModel    string

	// Multi-agent orchestration.
	Coord     coordination.CoordStore
	Tasks     task.Service
	Workers   *worker.Manager
	Worktrees worktree.Manager

	// Subsystems exposed for API handlers that need direct access.
	// These will shrink as more domain services are added.
	Store          *store.Store
	ToolClient     *toolclient.ToolClient
	ProcessTracker *chat.ProcessTracker
	Orchestrator   *chat.Orchestrator
	Activity       *chat.ActivityEmitter

	// AppConfig exposes the parsed nanite.yaml app config to handlers that
	// need it (artifact storage root, http caps, etc.). May be nil in
	// lightweight test setups — handlers must nil-check.
	AppConfig *config.AppConfig

	// Utility provider/model for lightweight calls (autotitle, etc.).
	UtilityProvider string
	UtilityModel    string

	// ModelSelector resolves provider+model for operations (summarization, etc.).
	ModelSelector *provider.StaticModelSelector

	// Permissions is the per-invocation permission engine. nil = permissions disabled.
	Permissions *permission.Engine

	// Workflow run store and SSE broadcaster. nil = workflow system disabled.
	RunStore            *workflow.RunStore
	WorkflowBroadcaster *workflow.Broadcaster

	// AdapterRegistry holds registered CLIAgentAdapters for discovery and sandbox ops.
	AdapterRegistry *agent.AdapterRegistry
}

// ContainerConfig holds all the external dependencies needed to construct
// a Container. Everything that main.go sets up before wiring goes here.
type ContainerConfig struct {
	Store      *store.Store
	Providers  *provider.Registry
	MCP        *mcp.Manager
	ToolClient *toolclient.ToolClient
	Plugins    *plugin.Host
	AppConfig  *config.AppConfig

	// Optional subsystems — nil-safe.
	Activity     *chat.ActivityEmitter
	OutputFilter *filter.Chain

	// Coordination store for multi-agent orchestration. nil = disabled.
	CoordStore coordination.CoordStore

	// Worktree manager for worker filesystem isolation. nil = disabled.
	Worktrees worktree.Manager

	// Worker concurrency limit (0 = default 5).
	MaxConcurrentWorkers int

	// Utility provider/model — read from DB or env by main.go.
	UtilityProvider string
	UtilityModel    string

	// CLI process concurrency limit (0 = unlimited).
	MaxCLIProcesses int
}

// NewContainer wires all services together and returns a ready Container.
func NewContainer(cfg ContainerConfig) (*Container, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("service.NewContainer: Store is required")
	}
	if cfg.Providers == nil {
		return nil, fmt.Errorf("service.NewContainer: Providers is required")
	}

	// --- Foundation (Wave 0) ---

	// Event emitter: fans out to activity + plugin sinks.
	var pluginSink PluginEventSink
	if cfg.Plugins != nil {
		pluginSink = cfg.Plugins
	}
	events := NewCompositeEmitter(cfg.Activity, pluginSink)

	// --- Domain services (Wave 1) ---

	sessions := NewSessionService(SessionServiceDeps{
		Sessions: cfg.Store,
		Writer:   cfg.Store,
		Agents:   cfg.Store,
		Settings: cfg.Store,
		Events:   events,
	})

	// Adapter registry — adapters self-register via plugin loading.
	// For now, the registry is created and passed through; adapter plugins
	// will be wired when the plugin host supports adapter registration.
	adapterRegistry := agent.NewAdapterRegistry()

	// Discover file-based agent definitions from all priority locations.
	agentDefs, err := agent.Discover(agent.DiscoverOptions{
		WorkingDir: ".",
		PluginsDir: "plugins",
		Adapters:   adapterRegistry,
	})
	if err != nil {
		slog.Warn("service container: agent discovery", "err", err)
	}
	// Append built-in default agent as lowest priority.
	if defaultDef, defErr := builtin.DefaultAgent(); defErr == nil {
		agentDefs = append(agentDefs, defaultDef)
	} else {
		slog.Warn("service container: built-in default agent", "err", defErr)
	}
	slog.Info("service container: discovered file-based agents", "count", len(agentDefs))

	agents := NewAgentService(AgentServiceConfig{
		Agents:     cfg.Store,
		Writers:    cfg.Store,
		Settings:   cfg.Store,
		Events:     events,
		FileAgents: agentDefs,
		Overrides:  cfg.Store,
	})

	// A2A messaging service. Uses the AgentService as its resolver so both
	// DB-backed and file-based agents validate uniformly.
	a2aSvc := a2a.NewService(cfg.Store, agents)
	slog.Info("service container: A2A service enabled")

	// Discover file-based skill definitions from all 5 priority locations.
	skillDefs, err := skill.Discover(skill.DiscoverOptions{
		WorkingDir: ".",
		PluginsDir: "plugins",
	})
	if err != nil {
		slog.Warn("service container: skill discovery", "err", err)
	}
	// Append built-in skills as lowest priority.
	if builtinDefs, bErr := skillbuiltin.BuiltinSkills(); bErr == nil {
		skillDefs = append(skillDefs, builtinDefs...)
	} else {
		slog.Warn("service container: built-in skills", "err", bErr)
	}
	slog.Info("service container: discovered file-based skills", "count", len(skillDefs))

	skills := NewSkillService(SkillServiceConfig{
		Skills:     cfg.Store,
		FileSkills: skillDefs,
	})

	// Internal todo/plan service (SQLite-backed, always available).
	todos := NewTodoService(TodoServiceConfig{
		Todos: cfg.Store,
		Plans: cfg.Store,
	})
	slog.Info("service container: todo/plan service enabled")

	// Task tracking service — requires coordination store.
	var tasks task.Service
	if cfg.CoordStore != nil && cfg.CoordStore.Available() {
		local := task.NewLocalBackend(cfg.CoordStore, &task.SQLiteSnapshot{DB: cfg.Store.DB})
		tasks = task.NewService(task.ServiceConfig{
			Local: local,
			Settings: func() string {
				us, err := cfg.Store.GetUserSettings()
				if err != nil {
					return task.BackendLocal
				}
				return us.TaskBackend
			},
		})
		slog.Info("service container: task tracking enabled (badger-backed)")
	} else {
		slog.Info("service container: task tracking disabled (no coordination store)")
	}

	// Embedded Conduit instance for memory storage.
	var conduitInstance *conduit.Conduit
	var memorySvc *memory.Service
	var embeddingStatus string
	var embeddingProviderID, embeddingModel string
	{
		homeDir, _ := os.UserHomeDir()
		conduitRoot := filepath.Join(homeDir, ".conduit")

		// Embedder selection: resolve from user settings via selectEmbedder.
		// No configured embedder = no-op (similarity recall unavailable).
		var embedder provider.Embedder
		us, usErr := cfg.Store.GetUserSettings()
		if usErr != nil {
			slog.Warn("service container: user_settings read failed; embedder disabled", "err", usErr)
			embeddingStatus = EmbeddingStatusDisabled
		} else {
			embedder, embeddingModel, embeddingStatus = SelectEmbedder(
				context.Background(),
				EmbedderSettings{
					Mode:     us.EmbeddingMode,
					Provider: us.EmbeddingProvider,
					Model:    us.EmbeddingModel,
				},
				DefaultEmbedderSelectDeps(),
			)
			embeddingProviderID = us.EmbeddingProvider
		}
		slog.Info("service container: embedder status",
			"status", embeddingStatus,
			"provider", embeddingProviderID,
			"model", embeddingModel,
		)

		var conduitOpts []conduit.Option
		if embedder != nil {
			conduitOpts = append(conduitOpts, conduit.WithEmbedder(embedder))
			conduitOpts = append(conduitOpts, conduit.WithEmbeddingModel(embeddingModel))
		}
		// Bridge Conduit's printf-style logger callback into slog. Conduit
		// formats its own messages, so we emit them verbatim at Info level —
		// structured attrs aren't available on this callback boundary.
		conduitOpts = append(conduitOpts, conduit.WithLogger(func(format string, args ...any) {
			slog.Info("conduit: " + fmt.Sprintf(format, args...))
		}))

		var conduitErr error
		conduitInstance, conduitErr = conduit.Open(context.Background(), conduit.Config{
			RootDir: conduitRoot,
		}, conduitOpts...)
		if conduitErr != nil {
			slog.Warn("service container: failed to open Conduit", "err", conduitErr)
		} else {
			memorySvc = memory.NewService(conduitInstance.MemoryStore())
			slog.Info("service container: memory service enabled (embedded Conduit)")
		}
	}

	var agentReader AgentReader = cfg.Store
	tools := NewToolService(cfg.ToolClient, cfg.MCP, agentReader)
	if impl, ok := tools.(*toolServiceImpl); ok {
		impl.SetDecisionLogger(cfg.Store)
	}

	// --- Orchestration (Wave 2) ---

	streams := NewStreamManager()
	if cfg.AppConfig != nil && cfg.AppConfig.Presence.CLIActiveThrottleSeconds > 0 {
		streams.CLIActiveThrottleInterval = time.Duration(cfg.AppConfig.Presence.CLIActiveThrottleSeconds) * time.Second
	}

	contextClient := chat.NewContextClient(cfg.Store)

	// --- ContextBroker: universal context retrieval ---
	{
		var sources []contextbroker.ContextSource

		// MemorySource — requires memory service (activation ranking by default).
		if memorySvc != nil {
			sources = append(sources, contextbroker.NewMemorySource(memorySvc))
		}

		// ConduitSource — requires MCP manager (calls Conduit tools).
		if cfg.MCP != nil {
			sources = append(sources, contextbroker.NewConduitSource(cfg.MCP))
		}

		// EngineSource — requires MCP manager (calls Engine tools).
		if cfg.MCP != nil {
			sources = append(sources, contextbroker.NewEngineSource(cfg.MCP))
		}

		// PCCSource — reads filesystem, always available.
		sources = append(sources, contextbroker.NewPCCSource(".nanite/pcc/global"))

		// SessionSource — reads message history, always available.
		sources = append(sources, contextbroker.NewSessionSource(func(sessionID string, limit int) ([]contextbroker.MessageSummary, error) {
			msgs, err := cfg.Store.ListMessages(sessionID, limit)
			if err != nil {
				return nil, err
			}
			out := make([]contextbroker.MessageSummary, len(msgs))
			for i, m := range msgs {
				out[i] = contextbroker.MessageSummary{Role: m.Role, Content: m.Content}
			}
			return out, nil
		}))

		broker := contextbroker.New(contextbroker.DefaultBudget(), sources...)
		contextClient.ContextBroker = broker
		slog.Info("service container: context broker enabled", "sources", len(sources))
	}

	// S3b tool-slot cache pipeline: stash manager + intent classifier. The
	// classifier's LLM fallback layer reuses the summarizer provider/model
	// unless UserSettings pins a different one.
	stashManager := stash.NewManager(stash.BuiltinCategorizer())
	overrideStore := newToolCacheOverrideStore()
	classifier := buildToolIntentClassifier(cfg.Providers, cfg.Store)

	ctxService := NewContextService(ContextServiceConfig{
		Client:       contextClient,
		StashManager: stashManager,
		Classifier:   classifier,
		Overrides:    overrideStore,
		SettingsFunc: func() *store.UserSettings {
			us, err := cfg.Store.GetUserSettings()
			if err != nil {
				return nil
			}
			return us
		},
	})

	// Command registry.
	commands := chat.NewCommandRegistry()
	commands.RegisterServerCommands(cfg.Store, cfg.Providers)
	RegisterToolCacheCommand(commands, overrideStore)

	// Register file-based skills as slash commands.
	RegisterSkillCommands(commands, skills)

	// Process tracker.
	processTracker := chat.NewProcessTracker()
	if cfg.MaxCLIProcesses > 0 {
		processTracker.MaxProcesses = cfg.MaxCLIProcesses
	}
	// 0 = unlimited (leave at ProcessTracker's zero-value default)

	orchestrator := chat.NewOrchestrator(cfg.Providers, cfg.MCP)

	// Permission engine with default mode. Rules loaded from project/user config at runtime.
	permissions := permission.NewEngine(permission.ModeDefault, nil)

	chatSvc := NewChatService(ChatServiceConfig{
		Sessions:        sessions,
		Agents:          agents,
		Tools:           tools,
		Streams:         streams,
		Context:         ctxService,
		Events:          events,
		Providers:       cfg.Providers,
		Store:           cfg.Store,
		Orchestrator:    orchestrator,
		AppConfig:       cfg.AppConfig,
		OutputFilter:    cfg.OutputFilter,
		Commands:        commands,
		PluginHost:      pluginSink,
		ProcessTracker:  processTracker,
		UtilityProvider:   cfg.UtilityProvider,
		UtilityModel:      cfg.UtilityModel,
		Permissions:       permissions,
		Tasks:             tasks,
		EmbeddingStatus:   embeddingStatus,
		EmbeddingProvider: embeddingProviderID,
		ResultCache:       buildResultCache(cfg.Store),
	})

	// Worker manager — requires ChatService for delegation.
	// Uses SetWorkers to break the circular dependency (ChatService <-> WorkerManager).
	var workers *worker.Manager
	if cfg.CoordStore != nil && cfg.CoordStore.Available() {
		workers = worker.NewManager(worker.ManagerConfig{
			MaxConcurrentWorkers: cfg.MaxConcurrentWorkers,
			Chat:                 chatSvc,
			Coord:                cfg.CoordStore,
			Tasks:                tasks,
			Worktrees:            cfg.Worktrees,
		})
		if impl, ok := chatSvc.(*chatServiceImpl); ok {
			impl.SetWorkers(workers)
		}
		slog.Info("service container: worker manager enabled")
	} else {
		slog.Info("service container: worker manager disabled (no coordination store)")
	}

	// Memory extraction hooks + agent tools.
	if memorySvc != nil && cfg.Plugins != nil {
		// Build a utility call function for memory extraction.
		utilityProvider := cfg.UtilityProvider
		if utilityProvider == "" {
			utilityProvider = models.DefaultProvider()
		}
		utilityModel := cfg.UtilityModel
		if utilityModel == "" {
			utilityModel = models.DefaultChatModel()
		}

		var utilityCall memory.UtilityCallFunc
		if prov, ok := cfg.Providers.Get(utilityProvider); ok {
			utilityCall = func(ctx context.Context, prompt string) (string, error) {
				msgs := []provider.ChatMessage{{Role: "user", Content: prompt}}
				return prov.Complete(ctx, provider.ChatRequest{
					SystemPrompt: "You are a memory extraction assistant. Follow instructions precisely.",
					Messages:     msgs,
					Model:        utilityModel,
				})
			}
		}

		extractor := memory.NewExtractor(memorySvc, utilityCall)

		// Register per-turn extraction hook (message.received).
		perTurnHook := extractor.PerTurnHook()
		if err := cfg.Plugins.RegisterEventHook(perTurnHook.EventTypes(), perTurnHook); err != nil {
			slog.Warn("service container: failed to register per-turn memory hook", "err", err)
		}

		// Register post-compaction extraction hook (context.compacted).
		postCompactHook := extractor.PostCompactHook()
		if err := cfg.Plugins.RegisterEventHook(postCompactHook.EventTypes(), postCompactHook); err != nil {
			slog.Warn("service container: failed to register post-compact memory hook", "err", err)
		}

		slog.Info("service container: memory extraction hooks registered")
	}

	// Register memory tools as a built-in MCP transport.
	if memorySvc != nil && cfg.MCP != nil {
		memoryTransport := mcp.NewMemoryToolsTransport(memorySvc)
		if err := cfg.MCP.AddServer("nanite-memory", memoryTransport, mcp.TierBuiltin); err != nil {
			slog.Warn("service container: failed to register memory MCP server", "err", err)
		} else {
			slog.Info("service container: memory agent tools registered")
		}
	}

	// Model selector for operation-specific model resolution (e.g., cheap model for summarization).
	modelSelector := provider.NewStaticModelSelector(cfg.UtilityProvider, cfg.UtilityModel)

	// Workflow run store and SSE broadcaster — always enabled.
	runStore := workflow.NewRunStore(50)
	workflowBroadcaster := workflow.NewBroadcaster()
	slog.Info("service container: workflow engine enabled")

	slog.Info("service container: all services wired")

	return &Container{
		Sessions:            sessions,
		Agents:              agents,
		Skills:              skills,
		Tools:               tools,
		Chat:                chatSvc,
		Context:             ctxService,
		Streams:             streams,
		Events:              events,
		Providers:           cfg.Providers,
		Commands:            commands,
		Plugins:             cfg.Plugins,
		MCP:                 cfg.MCP,
		A2A:                 a2aSvc,
		Todos:               todos,
		Conduit:             conduitInstance,
		Memory:              memorySvc,
		EmbeddingStatus:     embeddingStatus,
		EmbeddingProvider:   embeddingProviderID,
		EmbeddingModel:      embeddingModel,
		Coord:               cfg.CoordStore,
		Tasks:               tasks,
		Workers:             workers,
		Worktrees:           cfg.Worktrees,
		Store:               cfg.Store,
		ToolClient:          cfg.ToolClient,
		ProcessTracker:      processTracker,
		Orchestrator:        orchestrator,
		Activity:            cfg.Activity,
		UtilityProvider:     cfg.UtilityProvider,
		UtilityModel:        cfg.UtilityModel,
		ModelSelector:       modelSelector,
		Permissions:         permissions,
		AdapterRegistry:     adapterRegistry,
		RunStore:            runStore,
		WorkflowBroadcaster: workflowBroadcaster,
		AppConfig:           cfg.AppConfig,
	}, nil
}

// RefreshUtilitySettings updates the utility provider/model on the running
// container. Called by the settings API after a user changes preferences.
func (c *Container) RefreshUtilitySettings(prov, model string) {
	if prov != "" {
		c.UtilityProvider = prov
	}
	if model != "" {
		c.UtilityModel = model
	}
}

// containerShutdownMaxWait bounds total time spent shutting down subsystems.
// Individual subsystems may use a share of this — they are run in parallel so
// the ceiling applies to the slowest one, not the sum.
const containerShutdownMaxWait = 10 * time.Second

// Shutdown performs graceful shutdown of all services. Subsystems are shut
// down in parallel under a single max-wait ceiling so one stuck component
// cannot stall the others indefinitely. Returns when all subsystems have
// exited or the ceiling is hit, whichever comes first.
func (c *Container) Shutdown() {
	var wg sync.WaitGroup

	run := func(label string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				// A subsystem shutdown panicking should not abort the others.
				if r := recover(); r != nil {
					slog.Error("shutdown: subsystem panic", "label", label, "panic", r)
				}
			}()
			fn()
		}()
	}

	if c.Workers != nil {
		run("workers", func() {
			if err := c.Workers.Shutdown(containerShutdownMaxWait); err != nil {
				slog.Warn("shutdown: workers", "err", err)
			}
		})
	}
	run("chat", func() { c.Chat.Shutdown() })
	if c.Tasks != nil {
		run("tasks", func() {
			ctx, cancel := context.WithTimeout(context.Background(), containerShutdownMaxWait)
			defer cancel()
			if err := c.Tasks.Snapshot(ctx); err != nil {
				slog.Warn("shutdown: task snapshot", "err", err)
			}
		})
	}
	if c.Coord != nil {
		run("coord", func() { c.Coord.Close() })
	}
	if c.Conduit != nil {
		run("conduit", func() {
			if err := c.Conduit.Close(); err != nil {
				slog.Warn("shutdown: conduit close", "err", err)
			}
		})
	}
	if c.MCP != nil {
		run("mcp", func() { c.MCP.Close() })
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(containerShutdownMaxWait):
		slog.Warn("shutdown: timeout — some subsystems may still be running", "timeout", containerShutdownMaxWait.String())
	}
}

// buildResultCache creates a ResultCache from UserSettings or defaults.
func buildResultCache(s *store.Store) *tool.ResultCache {
	cfg := tool.ResultCacheConfig{}
	if us, err := s.GetUserSettings(); err == nil {
		cfg.SoftTruncBytes = us.ToolResultSoftTruncBytes
		cfg.HardCapBytes = us.ToolResultHardCapBytes
		cfg.CacheTTLSeconds = us.ToolResultCacheTTLSeconds
	}
	return tool.NewResultCache(s.DB, cfg)
}
