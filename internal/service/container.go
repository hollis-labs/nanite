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

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/builtin"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/filter"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/loopdetect"
	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/memory"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/plugin"
	adapterclaude "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-claude"
	adaptercodex "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-codex"
	adaptergemini "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-gemini"
	nanitenative "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-nanite-native"
	adapteropencode "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-opencode"
	"github.com/hollis-labs/nanite/internal/skill"
	skillbuiltin "github.com/hollis-labs/nanite/internal/skill/builtin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
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

	// Messaging service — validates, persists, and fans out
	// agent-to-agent messages plus handoff state transitions.
	Messaging *messaging.Service

	// Subagent service — inline spawn / status / cancel for
	// primary-agent-dispatched child agents (T9).
	Subagent *subagent.Service

	// Background service — non-session-bound, async dispatch for
	// long-running work (P9 BackgroundJob, CW-20260420-0016). Result
	// envelopes ride the Messaging service back to the originating
	// session as channel=inbox notifications.
	Background *background.Service

	// Elicitation service — MCP elicitation/create mid-tool user prompts
	// (CW-20260420-0018, G4). Write tools that need user confirmation call
	// into this service; the service pushes an elicitation-prompt envelope
	// to the chat surface and blocks until the user responds or the timeout
	// fires. Nil-safe: tools auto-approve when not wired.
	Elicitation *elicitation.Service

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

	// PathGrants tracks session-scoped explicit-mention path grants for
	// the trust-agent permission redesign (CW-20260430-0009). Always
	// non-nil; per-session state lives inside.
	PathGrants *permission.PathGrants

	// Workflow run store and SSE broadcaster. nil = workflow system disabled.
	RunStore            *workflow.RunStore
	WorkflowBroadcaster *workflow.Broadcaster

	// AdapterRegistry holds registered CLIAgentAdapters for discovery and sandbox ops.
	AdapterRegistry *agent.AdapterRegistry

	// Inspector is the I1 per-turn dev-mode aggregator (CW-20260426-0004).
	// nil when developer_mode is false.
	Inspector *inspectsvc.Service

	// LoopDetector is the I2 fingerprint-based loop detector (CW-20260420-0029).
	// Always non-nil; instantiated once at container boot.
	LoopDetector *loopdetect.Detector

	// ReminderEngine is the deterministic trigger engine for agent-set reminders
	// (J11, CW-20260426-0009). Always non-nil; per-session state is keyed
	// by sessionID inside the Engine.
	ReminderEngine *reminders.Engine

	// stopModelCatalog cancels the model catalog background refresher.
	stopModelCatalog context.CancelFunc
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

	// CLIAdapters is the slice of go-providers CLI adapters the agent-runtime
	// composition root resolves by Name(). Threaded from main.go so the
	// dev-mode `--dangerously-skip-permissions` wrapping (and other
	// per-process customizations) is preserved. nil = the runtime has no
	// adapters and Boot fails for any provider; callers should populate
	// at least claude/codex/opencode.
	CLIAdapters []provider.CLIAdapter
}

func newRuntimeAdapterRegistry() *agent.AdapterRegistry {
	reg := agent.NewAdapterRegistry()
	reg.Register(adapterclaude.New().Adapter())
	reg.Register(adaptercodex.New().Adapter())
	reg.Register(adaptergemini.New().Adapter())
	reg.Register(adapteropencode.New().Adapter())
	reg.Register(nanitenative.New().Adapter())
	return reg
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
	adapterRegistry := newRuntimeAdapterRegistry()

	// Ensure ~/.nanite/agents/ exists on first run (J6, CW-20260421-0006).
	// Silently continue on error — a missing home dir is non-fatal at startup.
	if err := agent.EnsureHomeDirs(""); err != nil {
		slog.Warn("service container: ensure agent home dirs", "err", err)
	}

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
	// POC CW-20260420-0047: mux orchestrator agent profile.
	// MuxOrchestratorAgent returns (nil, nil) in non-devmode builds; guard
	// the nil-def case so we don't append a nil pointer to agentDefs.
	if muxDef, muxErr := builtin.MuxOrchestratorAgent(); muxErr != nil {
		slog.Warn("service container: built-in mux orchestrator agent", "err", muxErr)
	} else if muxDef != nil {
		agentDefs = append(agentDefs, muxDef)
	}
	slog.Info("service container: discovered file-based agents", "count", len(agentDefs))

	// J7 (CW-20260421-0011): auto-ingest discovered agent definitions into DB.
	// File → parse → DB upsert. H1 trust: user/plugin sources → untrusted tier.
	// Built-in definitions (Source != "user"/"plugin") retain 'normal' tier.
	// Errors per-def are logged non-fatal via AutoIngestAgents.
	if n := AutoIngestAgents(cfg.Store, agentDefs); n > 0 {
		slog.Info("service container: auto-ingested agents into DB", "count", n)
	}

	agents := NewAgentService(AgentServiceConfig{
		Agents:     cfg.Store,
		Writers:    cfg.Store,
		Settings:   cfg.Store,
		Events:     events,
		FileAgents: agentDefs,
		Overrides:  cfg.Store,
	})

	// Wire the toolclient's file-agent permission resolver. File-based agents
	// have synthetic IDs ("file-<slug>") and live on disk, not in
	// agent_profiles — a store-backed permission lookup would miss every
	// time. Resolving through the AgentService lets a file agent's
	// frontmatter (or implicit Tools allowlist) flow into the broker.
	if cfg.ToolClient != nil {
		cfg.ToolClient.PermissionResolver = newFileAgentPermissionResolver(agentDefs)
	}

	// Messaging service. Uses the AgentService as its resolver so both
	// DB-backed and file-based agents validate uniformly. Takes the
	// SQLite-backed messaging Store plus the underlying *sql.DB so
	// handoff transactions (which span session_handoffs +
	// session_agents) can run as a single txn.
	msgStore := messaging.NewSQLiteStore(cfg.Store.DB)
	// cfg.Store satisfies messaging.AgentRegistrar via its CreateAgent
	// method — enables T6 auto-register-on-first-send.
	messagingSvc := messaging.NewService(msgStore, cfg.Store.DB, agents, cfg.Store)
	// Wire the session_events writer into the composite emitter so
	// EmitPreCompact / EmitPostCompact persist context_pre_compact /
	// context_post_compact rows for P8 part C (CW-20260426-0002).
	events.WithSessionWriter(messagingSvc)
	slog.Info("service container: messaging service enabled")

	// Ensure ~/.nanite/skills/ exists on first run (J6, CW-20260421-0006).
	// Silently continue on error — a missing home dir is non-fatal at startup.
	if err := skill.EnsureHomeDirs(""); err != nil {
		slog.Warn("service container: ensure skill home dirs", "err", err)
	}

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

	// J7 (CW-20260421-0011): auto-ingest discovered skill definitions into DB.
	// Skills from ~/.nanite/skills/ (Source="user") land as non-builtin rows.
	// Errors per-def are logged non-fatal via AutoIngestSkills.
	if n := AutoIngestSkills(cfg.Store, skillDefs); n > 0 {
		slog.Info("service container: auto-ingested skills into DB", "count", n)
	}

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
		// C2 (CW-20260429-0008): wire the LLM-augmented repair pipeline.
		// The repair model is selectable via NANITE_REPAIR_MODEL; the
		// provider is picked from the user's utility provider (which
		// is what already runs cheap classifier / summarizer calls).
		// Nil-safe: when the provider is missing, the repair config is
		// left nil and Execute returns the C1 envelope directly.
		if rc := buildRepairConfig(cfg.Providers, cfg.Store, cfg.UtilityProvider); rc != nil {
			impl.SetRepairConfig(rc)
		}
	}

	// Phase 5 / D3 (CW-20260419-0011): wire the reasoning-augmented broker
	// signals onto the toolclient. Both are nil-safe — when memorySvc is
	// nil or the skills directory is missing, the broker behaves exactly
	// as before (keyword + token budget). Wiring at this seam keeps the
	// toolclient package independent of memory + filesystem details.
	if cfg.ToolClient != nil {
		if memorySvc != nil {
			cfg.ToolClient.SetMemoryRecaller(toolclient.NewMemoryRecaller(memorySvc))
		}
		skillsDir := cfg.ToolClient.Config.SkillsDir
		if skillsDir == "" {
			skillsDir = toolclient.DefaultSkillsPath()
		}
		if skillsDir != "" && skillsDir != "off" {
			if loaded, err := toolclient.LoadSkillsFromDir(skillsDir); err == nil && len(loaded) > 0 {
				cfg.ToolClient.SetSkills(loaded)
			} else if err != nil {
				slog.Warn("service container: failed to load tool-preference skills", "dir", skillsDir, "err", err)
			}
		}
	}

	// --- Orchestration (Wave 2) ---

	streams := NewStreamManager()
	if cfg.AppConfig != nil && cfg.AppConfig.Presence.CLIActiveThrottleSeconds > 0 {
		streams.CLIActiveThrottleInterval = time.Duration(cfg.AppConfig.Presence.CLIActiveThrottleSeconds) * time.Second
	}

	// T7: wire the messaging → SSE notification bridge. Now that
	// streams exists, every SendMessage broadcasts a
	// message_received StreamEvent into the target session's active
	// SSE stream. Nil-safe — when no stream is attached the broadcast
	// drops silently, which is the intended MVP behavior.
	messagingSvc.SetNotificationSink(&messagingStreamSink{streams: streams})

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
	// B2 (CW-20260428-0010): bind /mode, /chat, /plan, /work to the store's
	// session-mode setter. Must run after NewCommandRegistry so it overwrites
	// the placeholder /mode entry created at construction time.
	commands.RegisterModeCommands(cfg.Store)
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

	// Permission engine. Yolo mode when developer_mode=1 so dev-mode sessions
	// never hit approval prompts.
	permissions := permission.NewEngine(permission.ModeDefault, nil)
	if us, err := cfg.Store.GetUserSettings(); err == nil && us.DeveloperMode {
		permissions.SetMode(permission.ModeYolo)
	}

	// Trust-agent path grants (CW-20260430-0009). Session-scoped store
	// for explicit-mention auto-grants registered at user-message ingest.
	// Threaded onto the tool-execution context so dev_tools resolveAllowed
	// can fall back to it when the static AllowedPaths list rejects.
	pathGrants := permission.NewPathGrants()

	// Model catalog — fetches pricing and context-window data from models.dev.
	// After each successful fetch the OnRefresh hook pushes the data into the
	// pkg/models overlay so all callers of Pricing/MaxOutputFor/ContextWindowFor
	// automatically see live values without threading the catalog through the stack.
	catalogCtx, stopCatalog := context.WithCancel(context.Background())
	modelCatalog := modelsdev.New(modelsdev.WithOnRefresh(syncCatalogToRegistry))
	// Sync from disk cache immediately (warm cache path) so the registry is
	// enriched before accepting traffic even when no network fetch is needed.
	syncCatalogToRegistry(modelCatalog)
	modelCatalog.StartRefresher(catalogCtx)

	// I1 (CW-20260426-0004): inspector service — dev-mode only.
	// Created unconditionally but only populated/queried when developer_mode=true.
	var inspectorSvc *inspectsvc.Service
	if us, err := cfg.Store.GetUserSettings(); err == nil && us.DeveloperMode {
		inspectorSvc = inspectsvc.NewService()
		slog.Info("service container: inspector service enabled (developer_mode=true)")
	}

	// I2 (CW-20260420-0029): loop detector — always-on, per-session windows.
	// Instantiated once at container boot; shared across all sessions.
	loopDetector := loopdetect.New()
	slog.Info("service container: loop detector enabled (I2, fingerprint-based)")

	// J11 (CW-20260426-0009): reminder engine — deterministic trigger evaluation.
	// A single engine is shared across sessions; per-session state lives inside
	// the engine (keyed by sessionID / reminderID). Always instantiated so the
	// SelfToolsTransport can register creation turns even before the first eval.
	reminderEngine := reminders.NewEngine(cfg.Store)
	slog.Info("service container: reminder engine enabled (J11, CW-20260426-0009)")

	// Phase 4c.1 (CW-20260508-0002): construct *agent.Dependencies +
	// agentsessions.Manager once, after the core deps (store, pathGrants,
	// streams) exist. Threaded through ChatServiceConfig so HandleMessage
	// + subagent runner + future background dispatcher reuse the singleton.
	// CLIAdapters fallback covers older main.go versions until the slice is
	// populated; agent.Boot fails clean when no adapter matches.
	cliAdapters := cfg.CLIAdapters
	if len(cliAdapters) == 0 {
		cliAdapters = []provider.CLIAdapter{
			provider.NewClaudeAdapter(),
			provider.NewCodexAdapter(),
			provider.NewOpencodeAdapter(),
		}
	}
	agentDeps, agentManager, agentBridge, agentDepsErr := BuildAgentDependencies(AgentDepsConfig{
		Store:       cfg.Store,
		PathGrants:  pathGrants,
		Streams:     streams,
		CLIAdapters: cliAdapters,
		DBPath:      cfg.Store.DBPath(),
	})
	if agentDepsErr != nil {
		stopCatalog()
		return nil, fmt.Errorf("service container: build agent dependencies: %w", agentDepsErr)
	}
	slog.Info("service container: agent runtime dependencies built",
		"adapters", len(cliAdapters),
		"workspaces_root", agentDeps.WorkspacesRoot)

	chatSvc := NewChatService(ChatServiceConfig{
		Sessions:           sessions,
		Agents:             agents,
		Tools:              tools,
		Streams:            streams,
		Context:            ctxService,
		Events:             events,
		Providers:          cfg.Providers,
		Store:              cfg.Store,
		Orchestrator:       orchestrator,
		AppConfig:          cfg.AppConfig,
		OutputFilter:       cfg.OutputFilter,
		Commands:           commands,
		PluginHost:         pluginSink,
		ProcessTracker:     processTracker,
		UtilityProvider:    cfg.UtilityProvider,
		UtilityModel:       cfg.UtilityModel,
		Permissions:        permissions,
		PathGrants:         pathGrants,
		Tasks:              tasks,
		EmbeddingStatus:    embeddingStatus,
		EmbeddingProvider:  embeddingProviderID,
		ResultCache:        buildResultCache(cfg.Store),
		ModelCatalog:       modelCatalog,
		SessionEventWriter: messagingSvc,
		DBPath:             cfg.Store.DBPath(),
		AdapterRegistry:    adapterRegistry,
		// CW-20260419-0026 (E3): wire the strategy decision logger.
		// *store.Store satisfies strategyDecisionLogger via
		// internal/store/strategy_log.go.
		StrategyLogger: cfg.Store,
		// I1 (CW-20260426-0004): inspector — nil when developer_mode=false.
		Inspector: inspectorSvc,
		// I2 (CW-20260420-0029): loop detector — always-on.
		LoopDetector: loopDetector,
		// J11 (CW-20260426-0009): reminder engine — always-on.
		ReminderEngine: reminderEngine,
		// Phase 4c.1 (CW-20260508-0002): agent-runtime composition root.
		AgentDeps:            agentDeps,
		AgentSessionsManager: agentManager,
		AgentEventBridge:     agentBridge,
	})

	// G-3 + G-5: subagent service with the real chat-engine-backed
	// runner. ChatRunner spawns a persisted child session per run and
	// drives one assistant turn through chatServiceImpl.generateResponse.
	// subagentStreamSink emits subagent_run_status_changed events on
	// the parent session's SSE stream for each transition.
	chatSvcImpl, ok := chatSvc.(*chatServiceImpl)
	if !ok {
		stopCatalog()
		return nil, fmt.Errorf("service container: chatSvc is %T, expected *chatServiceImpl for ChatRunner", chatSvc)
	}
	subagentRunner := NewChatRunner(chatSvcImpl, agentReader, cfg.Store, cfg.Store.DB, pathGrants)
	approvalEmitter := NewApprovalEmitter(cfg.Store, streams)
	subagentSvc := subagent.NewService(cfg.Store.DB, subagentRunner, messagingSvc, approvalEmitter, cfg.Store)
	subagentSvc.SetStreamSink(&subagentStreamSink{streams: streams})
	// H1 (CW-20260421-0014): wire trust resolver + audit event logger.
	subagentSvc.SetTrustResolver(cfg.Store)
	subagentSvc.SetEventLogger(cfg.Store)

	// G-4: register the subagent-spawn-approval typed response handler so
	// POST /api/envelopes/:id/respond dispatches to Approve/Reject.
	chat.RegisterResponseHandler("subagent-spawn-approval", chat.NewSubagentApprovalHandler(subagentSvc))

	slog.Info("service container: subagent service enabled (real chat-engine runner + status sink + approval handler + H1 trust)")

	// G1 (CW-20260420-0016): background-job service. Async, non-session-
	// bound dispatch for long-running tasks. Backend = PTY MVP (D2);
	// agent-mux swap (D3) is a wiring change behind the same Backend
	// interface. Result envelopes ride the messaging service back to the
	// originating session as channel=inbox notifications.
	backgroundSvc := background.NewService(background.NewPTYBackend(), messagingSvc)
	slog.Info("service container: background-job service enabled (PTY backend)")

	// G4 (CW-20260420-0018): elicitation service — MCP elicitation/create
	// mid-tool user prompts. The emitter persists an elicitation-prompt
	// envelope and streams it to the chat UI; the response handler routes
	// user responses back to the waiting tool call.
	elicitEmitter := NewElicitationEmitter(cfg.Store, streams)
	elicitSvc := elicitation.New(elicitEmitter, 0) // 0 → picks up env / default (5 min)
	chat.RegisterResponseHandler("elicitation-prompt", chat.NewElicitationResponseHandler(elicitSvc))
	slog.Info("service container: elicitation service enabled (G4, CW-20260420-0018)")

	// F5 follow-up (CW-20260420-0022): wire the HintDispatcher adapter
	// into ContextClient so NANITE_THINK_BLOCK_V2_ENABLED=true actually
	// fires v2 dynamic hints in production. Without this assignment the
	// production path falls through to v1 static hints (matching the
	// pre-Phase-7 behavior). Adapter is dispatch.Spawner-backed, slug
	// "hint-selector"; sub-millisecond cost when v2 is disabled because
	// the IsThinkBlockV2Enabled gate runs before the dispatcher is
	// consulted.
	hintSpawner := NewDispatchSpawner(subagentSvc, cfg.Store)
	contextClient.HintDispatcher = NewHintDispatchAdapter(hintSpawner)
	slog.Info("service container: hint dispatcher wired (F5 production wiring, CW-20260420-0022)")

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
		Messaging:           messagingSvc,
		Subagent:            subagentSvc,
		Background:          backgroundSvc,
		Elicitation:         elicitSvc,
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
		PathGrants:          pathGrants,
		AdapterRegistry:     adapterRegistry,
		Inspector:           inspectorSvc,
		LoopDetector:        loopDetector,
		ReminderEngine:      reminderEngine,
		RunStore:            runStore,
		WorkflowBroadcaster: workflowBroadcaster,
		AppConfig:           cfg.AppConfig,
		stopModelCatalog:    stopCatalog,
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

	if c.stopModelCatalog != nil {
		c.stopModelCatalog()
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
	if c.Messaging != nil {
		run("messaging", func() {
			if err := c.Messaging.Close(); err != nil {
				slog.Warn("shutdown: messaging close", "err", err)
			}
		})
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

// syncCatalogToRegistry builds a CatalogInput from the models.dev client and
// pushes it into the pkg/models overlay so all callers of Pricing,
// MaxOutputFor, and ContextWindowFor see live values without the catalog being
// threaded through the call stack.
func syncCatalogToRegistry(c *modelsdev.Client) {
	refs := c.List()
	if len(refs) == 0 {
		return
	}
	input := models.CatalogInput{
		ContextWindows:  make(map[string]int, len(refs)),
		MaxOutputTokens: make(map[string]int, len(refs)),
		InputPricing:    make(map[string]float64, len(refs)),
		OutputPricing:   make(map[string]float64, len(refs)),
	}
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		if ref.Limit.ContextWindow > 0 {
			input.ContextWindows[ref.ID] = ref.Limit.ContextWindow
		}
		if ref.Limit.MaxOutputTokens > 0 {
			input.MaxOutputTokens[ref.ID] = ref.Limit.MaxOutputTokens
		}
		if ref.Cost.Input > 0 {
			input.InputPricing[ref.ID] = ref.Cost.Input
		}
		if ref.Cost.Output > 0 {
			input.OutputPricing[ref.ID] = ref.Cost.Output
		}
	}
	models.SyncFromCatalog(input)
}
