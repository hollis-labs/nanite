package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/learnings"
	naniteotel "github.com/hollis-labs/nanite/internal/otel"
	"github.com/hollis-labs/nanite/internal/reflex"
	"github.com/hollis-labs/nanite/internal/worktree"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpserver"
	"github.com/hollis-labs/nanite/internal/muxproxy"
	"github.com/hollis-labs/nanite/pkg/models"
	"github.com/hollis-labs/nanite/internal/plugin"
	_ "github.com/hollis-labs/nanite/internal/plugin/allplugins" // registers all built-in plugins
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/server"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/internal/truncate"
	"github.com/hollis-labs/nanite/internal/version"
	"github.com/hollis-labs/go-toolbroker/broker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <command>\n", brand.BinaryName)
		fmt.Fprintln(os.Stderr, "commands: serve, plugin, mcp, message, version (framework-injection moved to `nanite-agent init`)")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "plugin":
		cmdPlugin(os.Args[2:])
	case "mcp":
		cmdMCP(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "message":
		cmdMessage(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(brand.BinaryName + " " + version.Full())
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8090, "HTTP listen port")
	dbPath := fs.String("db", "./"+brand.DefaultDBName, "SQLite database path")
	dev := fs.Bool("dev", false, "Development mode (skip embedded SPA)")
	fs.Parse(args)

	// Load app-level config first so the logging handler and OTel init
	// both observe the same settings. A missing or malformed config
	// falls back to safe defaults rather than aborting startup.
	appCfg, appCfgErr := config.LoadAppConfig("config/" + brand.ConfigFileName + ".yaml")
	if appCfgErr != nil {
		appCfg = config.DefaultAppConfig()
	}

	// Install the structured logging handler before anything else
	// emits a log record. All slog-based sites flow through the PII
	// redactor and JSON handler.
	_, logCloser, logErr := slogx.Init(slogx.Config{
		Format:    slogx.ParseFormat(appCfg.Logging.Format),
		Level:     slogx.ParseLevel(appCfg.Logging.Level),
		RedactPII: appCfg.Logging.RedactPII,
		AddSource: appCfg.Logging.AddSource,
	})
	if logErr != nil {
		slog.Warn("slog handler init failed, continuing with stdlib log", "err", logErr)
	} else {
		defer logCloser.Close()
	}

	// Load agentrc config (user-level + project-level, merged).
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		slog.Warn("failed to load agentrc config", "err", cfgErr)
	} else {
		name := cfg.Project.Name
		if name == "" {
			name = "(unnamed)"
		}
		slog.Info("config loaded", "project", name, "role", cfg.Role, "root", cfg.ProjectRoot())
	}

	if appCfgErr != nil {
		slog.Warn("failed to load app config, using defaults", "err", appCfgErr)
	}

	// Initialise OpenTelemetry tracing via the internal/otel wrapper.
	// The wrapper honours NANITE_OTEL_DISABLED=1 (env takes precedence)
	// and AppConfig.OTel.Disabled — either installs a no-op tracer
	// provider and returns a no-op shutdown.
	otelCtx := context.Background()
	otelShutdown, otelErr := naniteotel.Init(otelCtx, naniteotel.Config{
		ServiceName: brand.OTelService,
		Disabled:    appCfg.OTel.Disabled,
	})
	if otelErr != nil {
		slog.Warn("OTel init failed", "err", otelErr)
	} else {
		defer otelShutdown(otelCtx)
	}

	// Open store and run migrations.
	s, err := store.New(*dbPath)
	if err != nil {
		slogx.Fatal("failed to open store", "err", err)
	}
	defer s.Close()

	// Seed default data.
	if err := s.Seed(); err != nil {
		slogx.Fatal("failed to seed database", "err", err)
	}
	if err := s.SeedProviders(); err != nil {
		slogx.Fatal("failed to seed providers", "err", err)
	}
	if err := s.SeedBuiltinTemplates(); err != nil {
		slogx.Fatal("failed to seed templates", "err", err)
	}
	if err := s.SeedBuiltinPromptTemplates(); err != nil {
		slogx.Fatal("failed to seed prompt templates", "err", err)
	}
	if err := s.SeedBuiltinModes(); err != nil {
		slogx.Fatal("failed to seed modes", "err", err)
	}

	// Load core envelope types from manifest.
	if coreTypes := loadEnvelopeManifest("config/envelopes.yaml"); len(coreTypes) > 0 {
		chat.InitCoreTypes(coreTypes)
		slog.Info("envelope manifest loaded", "count", len(coreTypes))
	}

	// Wire the plugin→chat envelope registrar hook (B.4). Without this, the
	// yaml-authoritative loader still records envelopes in the host side-map
	// but chat validation won't accept them until the plugin calls through.
	plugin.SetEnvelopeTypeRegistrar(chat.RegisterEnvelopeType)
	plugin.SetEnvelopeTypeUnregistrar(chat.UnregisterEnvelopeType)

	// B.11 strict envelope validation: cache developer_mode once at startup
	// rather than querying the settings store on every FilterPluginEnvelopes
	// call. Envelope filtering runs on every plugin command / event / MCP
	// response, so a DB hit per envelope would be avoidable latency. Changes
	// to developer_mode require a process restart to take effect in the
	// envelope validator — acceptable because this is a developer-tooling
	// knob, not a runtime-tunable behavior.
	envelopeValidatorDevMode := false
	if settings, err := s.GetUserSettings(); err != nil {
		slog.Warn("envelope validator dev mode: failed to read user settings at startup; defaulting to production-strict", "error", err)
	} else {
		envelopeValidatorDevMode = settings.DeveloperMode
	}
	plugin.SetEnvelopeValidatorDevModeFunc(func() bool { return envelopeValidatorDevMode })

	// Set up provider registry (API keys, Ollama, CLI adapters).
	registry := initProviders(envelopeValidatorDevMode)

	slog.Info("app config loaded",
		"cli_active_throttle_seconds", appCfg.Presence.CLIActiveThrottleSeconds,
		"auto_detect_tools", len(appCfg.Artifacts.AutoDetectTools))

	// Configure output filters. Default: strip emoji from LLM responses.
	outputFilters := filter.NewChain()
	outputFilters.Add("no_emoji", filter.NoEmoji)
	slog.Info("output filters registered", "filters", outputFilters.Names())

	// Set up MCP manager, tool broker, and self-service tools. cfg may be
	// nil if the agentrc loader failed; initMCP falls back to the hardcoded
	// default allow-list in that case so dev tools still work for the
	// running user.
	mcpManager, tb, selfTools, muxMgr, muxSvc := initMCP(s, cfg)

	// Set up activity emitter (Volon GUI events).
	activity := chat.NewActivityEmitter("")

	// Resolve utility provider/model from DB → env → defaults.
	utilityProvider, utilityModel := "", ""
	if settings, err := s.GetUserSettings(); err == nil {
		utilityProvider = settings.UtilityProvider
		utilityModel = settings.UtilityModel
	}
	if utilityProvider == "" {
		utilityProvider = models.DefaultProvider()
	}
	if utilityModel == "" {
		utilityModel = models.DefaultChatModel()
	}

	// CLI process concurrency limit.
	maxCLIProcs := 10
	if maxProcs := os.Getenv(brand.Env("MAX_CLI_PROCESSES")); maxProcs != "" {
		if n, err := strconv.Atoi(maxProcs); err == nil && n >= 0 {
			maxCLIProcs = n
		}
	}

	// Create plugin host (before container so it can be wired as a dependency).
	logger := plugin.NewLogger(brand.ID + "-plugin")
	pluginHost := plugin.NewHost(nil, logger)
	pluginHost.SetStore(s)
	pluginHost.SetMCPRegistrar(mcpManager)
	pluginHost.RegisterService("store", s)
	pluginHost.RegisterService("mcp", mcpManager)
	pluginHost.RegisterService("toolclient", tb)
	slog.Info("plugin host initialized")

	// --- Coordination store (Badger KV for multi-agent state) ---
	coordDir := filepath.Join(filepath.Dir(*dbPath), "coordination")
	coordStore, coordErr := coordination.NewBadgerStore(coordDir)
	if coordErr != nil {
		slog.Warn("coordination store failed to open, multi-agent features disabled", "err", coordErr)
		coordStore = nil
	} else {
		defer coordStore.Close()
		slog.Info("coordination store: badger initialized", "dir", coordDir)
	}
	var coord coordination.CoordStore
	if coordStore != nil {
		coord = coordStore
	} else {
		coord = coordination.NewNoopStore()
	}

	// --- Worktree manager (git worktree isolation for workers) ---
	wtBaseDir := filepath.Join(filepath.Dir(*dbPath), "worktrees")
	wtMgr, wtErr := worktree.NewManager(wtBaseDir)
	if wtErr != nil {
		slog.Warn("worktree manager init failed, worktree isolation disabled", "err", wtErr)
		wtMgr = worktree.NewNoopManager()
	} else {
		slog.Info("worktree manager initialized", "dir", wtBaseDir)
	}

	// --- Service container: single wiring point ---
	container, err := service.NewContainer(service.ContainerConfig{
		Store:           s,
		Providers:       registry,
		MCP:             mcpManager,
		ToolClient:      tb,
		Plugins:         pluginHost,
		AppConfig:       appCfg,
		Activity:        activity,
		OutputFilter:    outputFilters,
		UtilityProvider: utilityProvider,
		UtilityModel:    utilityModel,
		MaxCLIProcesses: maxCLIProcs,
		CoordStore:      coord,
		Worktrees:       wtMgr,
	})
	if err != nil {
		slogx.Fatal("failed to create service container", "err", err)
	}

	// G5: wire mux Manager's StreamPublisher — devmode-only, no-op in production.
	// Run goroutine is started after daemonLifecycle is constructed below.
	wireMuxPublisher(muxMgr, container.Streams)

	// Wire todo/plan store into the self-tools transport.
	selfTools.TodoStore = s
	selfTools.Messaging = container.Messaging
	selfTools.Subagent = container.Subagent
	selfTools.Background = container.Background
	selfTools.Work = container.Streams
	// G4 (CW-20260420-0018): wire elicitation service so write tools
	// (e.g. nanite_message_send kind=directive) can request mid-call
	// user confirmation via elicitation/create.
	selfTools.Elicitation = container.Elicitation

	// CW-20260421-0010 (B3): wire the executeTask dispatch primitive.
	// Adapts subagent.Service.Spawn to dispatch.Spawner so the chat
	// agent's nanite_execute_task tool can drive role-based dispatch.
	if container.Subagent != nil {
		selfTools.Dispatch = service.NewDispatchSpawner(container.Subagent, s)
		// DispatchWrapper left nil — the transport falls back to
		// dispatch.DefaultEnvelopeWrapper when unset.
	}

	// CW-20260426-0006 (J8 v1): wire panel-control surface.
	//   - PanelSignalSink — push panel_signal SSE events on the originating session.
	//   - PanelLookup — enumerate plugin-registered panels for the access check.
	//   - TrustResolver — H1 gate for plugin-shipped panels (built-ins skip the gate).
	selfTools.PanelSignalSink = container.Streams
	selfTools.PanelLookup = func() []string {
		entries := pluginHost.GetPanels()
		ids := make([]string, len(entries))
		for i, e := range entries {
			ids[i] = e.ID
		}
		return ids
	}
	selfTools.TrustResolver = s
	// J11 (CW-20260426-0009): wire the reminder engine so RegisterTurnCount
	// calls from nanite_set_reminder hit the correct shared Engine instance.
	selfTools.ReminderEngine = container.ReminderEngine

	// D1 (CW-20260429-0009): wire the Vanta-backed learning recorder
	// + recaller used by nanite_remember and the lesson-recall slot
	// extension. memory.Service satisfies the learnings.LearningStore
	// interface; when it is nil (Conduit not initialised) both wires
	// stay nil and the self-tool returns a clear errorResult.
	if container.Memory != nil {
		selfTools.LearningRecorder = learnings.NewRecorder(container.Memory)
		selfTools.LearningRecaller = learnings.NewRecaller(container.Memory)
	}

	// Restore non-terminal tasks from SQLite snapshot into coordination store.
	if container.Tasks != nil {
		if err := container.Tasks.Restore(context.Background()); err != nil {
			slog.Warn("task restore", "err", err)
		}
	}

	// Clean up orphaned worktrees from previous runs.
	if container.Worktrees != nil {
		if cleaned, wtCleanErr := container.Worktrees.CleanupOrphaned(nil); wtCleanErr != nil {
			slog.Warn("worktree orphan cleanup", "err", wtCleanErr)
		} else if cleaned > 0 {
			slog.Info("worktree cleanup: removed orphaned worktrees", "count", cleaned)
		}
	}

	// Wire plugin host command registry from the container.
	pluginHost.SetCommandRegistry(container.Commands)
	pluginHost.SetEnvelopeConsumer(container.Streams)
	pluginHost.RegisterService("container", container)
	if container.Tasks != nil {
		pluginHost.RegisterService("tasks", container.Tasks)
	}

	// Create API layer.
	a := api.New(container)

	// Register existing custom actions as slash commands.
	if actions, err := s.ListCustomActions(); err == nil {
		for _, action := range actions {
			if action.SlashCommand != "" && action.Enabled {
				a.RegisterActionCommand(&action)
			}
		}
	}

	// Lifecycle manager for long-running daemon goroutines (cleanup,
	// snapshots, reapers). Owned by cmdServe; shut down on signal before
	// container.Shutdown so daemons stop referencing container state.
	daemonLifecycle := lifecycle.NewManager("cmd.nanite.daemons")

	// G5: start mux Manager goroutine — devmode-only, no-op in production.
	startMuxManager(daemonLifecycle, muxMgr, muxSvc)

	// Shutdown handler. Uses context.Background() because cmdServe has no
	// parent ctx at this scope; the goroutine lives until the process exits.
	safego.Go(context.Background(), "cmd.nanite.signal-handler", func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down")
		if err := daemonLifecycle.Shutdown(10 * time.Second); err != nil {
			slog.Error("daemon lifecycle shutdown", "err", err)
		}
		container.Shutdown()
		os.Exit(0)
	})

	// Start periodic background workers (cleanup, snapshots, reapers).
	startBackgroundWorkers(daemonLifecycle, container)

	// Start HTTP server.
	srv := server.New(s, a, *port, *dev, pluginHost, appCfg.HTTP)

	// Discover, load plugins, and re-discover MCP tools.
	pluginsDir := discoverAndLoadPlugins(pluginHost, *dbPath, mcpManager, s)
	srv.SetPluginsDir(pluginsDir)

	if err := srv.ListenAndServe(); err != nil {
		slogx.Fatal("server error", "err", err)
	}
}

// initProviders creates the provider registry with all available API providers,
// Ollama (local, no key required), and CLI adapters (PTY + subprocess).
// skipPermsAdapter wraps a CLIAdapter and appends --dangerously-skip-permissions
// to BuildArgs. Used when developer_mode is enabled.
type skipPermsAdapter struct{ provider.CLIAdapter }

func (a skipPermsAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	return append(a.CLIAdapter.BuildArgs(prompt, systemPrompt, cliSessionID), "--dangerously-skip-permissions")
}

func initProviders(devMode bool) *provider.Registry {
	registry := provider.NewRegistry()

	resolveKey := func(providerID string) string {
		return secrets.Get(secrets.ProviderKeyName(providerID))
	}

	type apiProvSpec struct {
		name, provID string
		create       func() provider.Provider
		setKey       func(provider.Provider, string)
	}
	apiProviders := []apiProvSpec{
		{"anthropic", "anthropic-001",
			func() provider.Provider { return provider.NewAnthropic() },
			func(p provider.Provider, k string) { p.(*provider.Anthropic).SetAPIKey(k) }},
		{"openai", "openai-001",
			func() provider.Provider { return provider.NewOpenAI() },
			func(p provider.Provider, k string) { p.(*provider.OpenAI).SetAPIKey(k) }},
		{"gemini", "gemini-api-001",
			func() provider.Provider { return provider.NewGemini() },
			func(p provider.Provider, k string) { p.(*provider.Gemini).SetAPIKey(k) }},
		{"mistral", "mistral-001",
			func() provider.Provider { return provider.NewMistral() },
			func(p provider.Provider, k string) { p.(*provider.Mistral).SetAPIKey(k) }},
		{"openrouter", "openrouter-001",
			func() provider.Provider { return provider.NewOpenRouter() },
			func(p provider.Provider, k string) { p.(*provider.OpenRouter).SetAPIKey(k) }},
		{"openzen", "openzen-001",
			func() provider.Provider { return provider.NewOpenZen() },
			func(p provider.Provider, k string) { p.(*provider.OpenZen).SetAPIKey(k) }},
	}

	var registeredAPI, missingAPI []string
	for _, spec := range apiProviders {
		key := resolveKey(spec.provID)
		if key != "" {
			p := spec.create()
			spec.setKey(p, key)
			registry.Register(spec.name, p)
			slog.Info("provider registered (key from keychain)", "provider", spec.name)
			registeredAPI = append(registeredAPI, spec.name)
		} else {
			missingAPI = append(missingAPI, spec.name)
		}
	}

	// Azure OpenAI — needs both key and endpoint.
	azureKey := resolveKey("azure-openai-001")
	if azureKey != "" && os.Getenv("AZURE_OPENAI_ENDPOINT") != "" {
		p := provider.NewAzureOpenAI()
		p.SetAPIKey(azureKey)
		registry.Register("azure-openai", p)
		slog.Info("provider registered", "provider", "azure-openai")
		registeredAPI = append(registeredAPI, "azure-openai")
	}

	if len(missingAPI) > 0 && len(registeredAPI) == 0 {
		slog.Warn("no API providers configured — chat will not work")
	}

	// Always register Ollama — it requires no API key (local service).
	registry.Register("ollama", provider.NewOllama())
	slog.Info("provider registered", "provider", "ollama", "host", "http://localhost:11434")

	// Register CLI adapters — PTY (unix) and subprocess (all platforms).
	var claudeAdapter provider.CLIAdapter = provider.NewClaudeAdapter()
	if devMode {
		claudeAdapter = skipPermsAdapter{claudeAdapter}
	}
	cliAdapters := []provider.CLIAdapter{
		claudeAdapter,
		provider.NewCodexAdapter(),
		provider.NewGeminiAdapter(),
		provider.NewCopilotAdapter(),
		provider.NewAiderAdapter(),
		provider.NewJunieAdapter(),
		provider.NewKiroAdapter(),
		provider.NewQwenAdapter(),
	}
	for _, adapter := range cliAdapters {
		if path, ok := adapter.Detect(); ok {
			ptyName := "pty-" + adapter.Name()
			registry.Register(ptyName, provider.NewPTYBridgeWithAdapter(adapter, path))
			slog.Info("pty provider registered", "name", ptyName, "path", path)

			subName := "sub-" + adapter.Name()
			registry.Register(subName, provider.NewSubprocessBridge(adapter, path))
			slog.Info("subprocess provider registered", "name", subName, "path", path)
		}
	}
	registerLegacyPTYAlias(registry)

	return registry
}

func registerLegacyPTYAlias(registry *provider.Registry) {
	// Backwards-compat alias: "pty" should mirror the registered Claude PTY
	// provider so it inherits the same adapter wrapping and sandbox flags.
	if claudePTY, ok := registry.Get("pty-claude"); ok {
		registry.Register("pty", claudePTY)
		return
	}
	if ptyBridge := provider.NewPTYBridge(); ptyBridge != nil {
		registry.Register("pty", ptyBridge)
	}
}

// defaultDevToolsAllowedPaths returns the hardcoded fallback allow-list used
// when no user/project config provides one. Entries are absolute, with ~
// already expanded. Keep this list narrow on purpose — it covers the common
// project/agent scratch locations and nothing else.
//
// CW-20260430-0005: widened from the original two-entry list (~/Projects-apps
// and ~/Projects) to also include ~/.nanite (agent framework state, role
// libraries, sandbox dirs) and ~/.claude (per-session worktrees, settings).
// The user-supplied list at config.dev_tools_allowed_paths still REPLACES
// these defaults rather than merging — set explicitly to widen or narrow.
func defaultDevToolsAllowedPaths() []string {
	homeDir, _ := os.UserHomeDir()
	return []string{
		filepath.Join(homeDir, "Projects-apps"),
		filepath.Join(homeDir, "Projects"),
		filepath.Join(homeDir, ".nanite"),
		filepath.Join(homeDir, ".claude"),
	}
}

// resolveDevToolsAllowedPaths returns the effective allow-list for the dev_*
// MCP tools. When cfg is nil or has no user-supplied list, the hardcoded
// defaults are used. When cfg.DevToolsAllowedPaths is set the user's list
// replaces the defaults wholesale; this matches the existing override
// semantics for WritePaths/ProtectedPaths and lets a user explicitly narrow
// the scope on a locked-down machine.
func resolveDevToolsAllowedPaths(cfg *config.Config) []string {
	if cfg != nil {
		if userPaths := cfg.ResolvedDevToolsAllowedPaths(); len(userPaths) > 0 {
			return userPaths
		}
	}
	return defaultDevToolsAllowedPaths()
}

// devAllowedSource returns a short string describing where the dev tools
// allow-list came from (user config vs builtin default), purely for log
// observability when sessions inevitably hit a path-escape error.
func devAllowedSource(cfg *config.Config) string {
	if cfg != nil && len(cfg.ResolvedDevToolsAllowedPaths()) > 0 {
		return "config:dev_tools_allowed_paths"
	}
	return "default"
}

// initMCP sets up the MCP manager with built-in and user-configured servers,
// runs auto-discovery, and creates the tool broker. Returns the mux Manager
// and MuxProxy service so the caller can wire a StreamPublisher, start Run,
// and call StopAll on shutdown. CW-20260420-0047.
//
// The mux-orchestrator transport and tool registration is gated behind the
// devmode build tag via registerMuxTransport (G5 — CW-20260421-0001).
// In production builds registerMuxTransport is a no-op and no mux_* tools
// appear in the tool surface.
func initMCP(s *store.Store, cfg *config.Config) (*mcp.Manager, *toolclient.ToolClient, *mcp.SelfToolsTransport, *muxproxy.Manager, *service.MuxProxy) {
	mcpManager := mcp.NewManager()

	devAllowed := resolveDevToolsAllowedPaths(cfg)
	slog.Info("dev tools allow-list", "paths", devAllowed, "source", devAllowedSource(cfg))
	if err := mcpManager.AddServer("dev", mcp.NewDevToolsTransport(devAllowed), mcp.TierBuiltin); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "dev", "err", err)
	}
	if err := mcpManager.AddServer("general", mcp.NewGeneralToolsTransport(), mcp.TierBuiltin); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "general", "err", err)
	}
	if err := mcpManager.AddServer("code", mcp.NewCodeExecTransport(""), mcp.TierBuiltin); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "code", "err", err)
	}
	selfTools := mcp.NewSelfToolsTransport(s)
	// E1 (CW-20260419-0027): wire reflex set + logger into the dispatch path.
	// LoadUserReflexes returns nil on a missing dir (not an error); merge with
	// builtins so user overrides with priority>=50 reliably beat built-ins.
	userReflexes, err := reflex.LoadUserReflexes("")
	if err != nil {
		slog.Warn("reflex: failed to load user overrides", "err", err)
	}
	selfTools.ReflexSet = reflex.MergeReflexes(reflex.BuiltinReflexes(), userReflexes)
	selfTools.ReflexLogger = s
	// B1 (CW-20260429-0006): wire the manager as the cross-server schema
	// registry so nanite_validate can pre-flight check args for any
	// registered tool, not just self-tools.
	selfTools.SchemaLookup = mcpManager
	if err := mcpManager.AddServer("self", selfTools, mcp.TierBuiltin); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "self", "err", err)
	}

	loadPersistedMCPServers(s, mcpManager)
	mcpManager.Broker = broker.NewLocalBroker(nil, broker.DefaultRules())
	if diff, err := mcpManager.AutoDiscover(context.Background(), s); err != nil {
		slog.Warn("MCP auto-discovery failed", "err", err)
	} else {
		slog.Info("MCP auto-discovery",
			"total", diff.Total,
			"added", len(diff.Added),
			"removed", len(diff.Removed))
	}

	tb := toolclient.New(mcpManager, s, nil)
	if mcpManager.Broker != nil {
		tb.LocalBroker = mcpManager.Broker
		slog.Info("toolclient: sharing MCPManager broker", "tool_summaries", len(mcpManager.Broker.AllTools()))
	}
	devToolDefs := mcp.DevToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("dev", devToolDefs)
	slog.Info("registered dev built-in tools", "count", len(devToolDefs))

	selfToolDefs := mcp.SelfToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("self-service", selfToolDefs)
	slog.Info("registered self-service built-in tools", "count", len(selfToolDefs))

	// G5 (CW-20260421-0001): mux transport + tool registration — devmode only.
	// registerMuxTransport is a no-op in non-devmode builds; mux_* tools are
	// absent from the production tool surface.
	muxMgr, muxSvc := registerMuxTransport(mcpManager, tb, s)

	// Register result-cache meta-tools (S4a). These let the LLM recall
	// truncated tool results via fetch_tool_result / search_tool_result.
	tb.Builtins.RegisterBuiltins("result-cache", []provider.ToolDefinition{
		toolclient.FetchToolResultMetaTool(),
		toolclient.SearchToolResultMetaTool(),
	})

	return mcpManager, tb, selfTools, muxMgr, muxSvc
}

// startBackgroundWorkers launches periodic goroutines for cleanup, snapshots,
// and reapers on the supplied lifecycle manager. Each daemon's inner loop
// selects on ctx.Done() so Shutdown drains them deterministically.
func startBackgroundWorkers(lc *lifecycle.Manager, container *service.Container) {
	// Periodic cleanup of saved tool outputs.
	lc.Go("truncate-cleanup", func(ctx context.Context) {
		truncate.Cleanup()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				truncate.Cleanup()
			}
		}
	})

	// Periodic task snapshot (flush Badger state to SQLite).
	if container.Tasks != nil {
		lc.Go("task-snapshot", func(ctx context.Context) {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := container.Tasks.Snapshot(ctx); err != nil {
						slog.Warn("task snapshot", "err", err)
					}
				}
			}
		})
	}

	// Periodic stale process reaper.
	lc.Go("stale-process-reaper", func(ctx context.Context) {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if killed := container.ProcessTracker.KillStale(5 * time.Minute); killed > 0 {
					slog.Info("stale process reaper: killed hung CLI processes", "count", killed)
				}
			}
		}
	})

	// Periodic stale worker reaper.
	if container.Workers != nil {
		lc.Go("stale-worker-reaper", func(ctx context.Context) {
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if stale := container.Workers.ReapStale(60 * time.Second); len(stale) > 0 {
						slog.Info("stale worker reaper: reaped workers", "count", len(stale))
					}
				}
			}
		})
	}
}

// discoverAndLoadPlugins finds plugins on disk, loads them and builtins,
// then re-runs MCP auto-discovery for any new servers plugins registered.
func discoverAndLoadPlugins(pluginHost *plugin.Host, dbPath string, mcpManager *mcp.Manager, s *store.Store) string {
	pluginsDir := filepath.Join(filepath.Dir(dbPath), "plugins")
	if envDir := os.Getenv(brand.Env("PLUGINS_DIR")); envDir != "" {
		pluginsDir = envDir
	}
	if discovered, discErr := plugin.DiscoverPlugins(pluginsDir); discErr != nil {
		slog.Warn("plugin discovery failed", "err", discErr)
	} else {
		loaded, loadErrs := plugin.LoadDiscovered(pluginHost, discovered)
		for _, e := range loadErrs {
			slog.Warn("plugin load", "err", e)
		}
		slog.Info("plugins discovered", "discovered", len(discovered), "loaded", len(loaded))
	}
	if builtins, builtinErrs := plugin.LoadRegisteredBuiltins(pluginHost); len(builtins) > 0 {
		for _, e := range builtinErrs {
			slog.Warn("plugin builtin load", "err", e)
		}
		slog.Info("plugins: loaded builtins", "count", len(builtins))
	}

	// Re-discover tools after plugins (they may register new MCP servers).
	if postDiff, err := mcpManager.AutoDiscover(context.Background(), s); err != nil {
		slog.Warn("post-plugin MCP discovery failed", "err", err)
	} else if len(postDiff.Added) > 0 {
		slog.Info("post-plugin MCP discovery: new tools added", "count", len(postDiff.Added), "tools", postDiff.Added)
	}

	return pluginsDir
}

// loadEnvelopeManifest reads config/envelopes.yaml and returns the list of
// core envelope type strings for registration.
func loadEnvelopeManifest(path string) []string {
	type entry struct {
		Type string `yaml:"type"`
	}
	type manifest struct {
		Core []entry `yaml:"core"`
	}

	data, err := os.ReadFile(path)
	if err != nil {
		slogx.Fatal("failed to read envelope manifest", "path", path, "err", err)
	}

	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		slogx.Fatal("failed to parse envelope manifest", "path", path, "err", err)
	}

	types := make([]string, 0, len(m.Core))
	for _, e := range m.Core {
		if strings.TrimSpace(e.Type) == "" {
			continue
		}
		types = append(types, e.Type)
	}
	return types
}

// loadPersistedMCPServers loads user-configured MCP servers from the database
// and registers them with the MCP manager.
func loadPersistedMCPServers(s *store.Store, m *mcp.Manager) {
	servers, err := s.ListMCPServers()
	if err != nil {
		slog.Warn("failed to load persisted MCP servers", "err", err)
		return
	}

	for _, cfg := range servers {
		if !cfg.Enabled {
			continue
		}
		switch cfg.TransportType {
		case "stdio":
			var args []string
			if cfg.Args != "" && cfg.Args != "[]" {
				json.Unmarshal([]byte(cfg.Args), &args)
			}
			var envVars []string
			if cfg.Env != "" && cfg.Env != "[]" {
				json.Unmarshal([]byte(cfg.Env), &envVars)
			}
			var envAllowlist []string
			if cfg.EnvAllowlist != "" && cfg.EnvAllowlist != "[]" {
				if err := json.Unmarshal([]byte(cfg.EnvAllowlist), &envAllowlist); err != nil {
					slog.Warn("mcp: malformed env_allowlist json — ignoring",
						"name", cfg.Name, "err", err)
					envAllowlist = nil
				}
			}
			if err := m.AddStdioServer(cfg.Name, cfg.Command, args, envVars, envAllowlist, mcp.TrustTier(cfg.TrustTier)); err != nil {
				slog.Warn("mcp: failed to register persisted stdio server", "name", cfg.Name, "err", err)
			}
		case "sse":
			if err := m.AddHTTPServer(cfg.Name, cfg.URL, mcp.TrustTier(cfg.TrustTier)); err != nil {
				slog.Warn("mcp: failed to register persisted http server", "name", cfg.Name, "err", err)
			}
		default:
			slog.Warn("mcp: unknown transport type, skipping", "transport", cfg.TransportType, "server", cfg.Name)
		}
	}

	if len(servers) > 0 {
		slog.Info("mcp: loaded user-configured servers from database", "count", len(servers))
	}
}

// cmdMCP dispatches MCP subcommands: serve (default), import, export.
func cmdMCP(args []string) {
	// Check for subcommand before falling through to serve mode.
	if len(args) > 0 {
		switch args[0] {
		case "import":
			mcpImport(args[1:])
			return
		case "export":
			mcpExport(args[1:])
			return
		}
	}

	// Default: run the MCP server on stdio.
	cmdMCPServe(args)
}

// cmdMCPServe runs the Nanite MCP server on stdio. Claude CLI (or any MCP client)
// spawns this as a subprocess and communicates via JSON-RPC on stdin/stdout.
func cmdMCPServe(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	dbPath := fs.String("db", "./"+brand.DefaultDBName, "SQLite database path")
	sessionID := fs.String("session", "", "Session ID")
	fs.Parse(args)

	s, err := store.New(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s mcp: open db: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	defer s.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Reuse the same allow-list resolution path as the main server so the
	// stdio MCP entry point honours config.dev_tools_allowed_paths and
	// inherits the widened defaults (~/.nanite, ~/.claude). Failures to
	// load the agentrc config fall back to defaults — the stdio path is
	// invoked by external clients (Claude CLI), not the running user, so
	// degrading gracefully here matters more than aborting.
	cfg, _ := config.Load()
	allowedPaths := resolveDevToolsAllowedPaths(cfg)
	srv := mcpserver.New(s, *sessionID, allowedPaths)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s mcp: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
}

