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
	naniteotel "github.com/hollis-labs/nanite/internal/otel"
	"github.com/hollis-labs/nanite/internal/worktree"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpserver"
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
		fmt.Fprintln(os.Stderr, "commands: serve, plugin, mcp, a2a, version (framework-injection moved to `nanite-agent init`)")
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
	case "a2a":
		cmdA2A(os.Args[2:])
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
	registry := initProviders()

	slog.Info("app config loaded",
		"cli_active_throttle_seconds", appCfg.Presence.CLIActiveThrottleSeconds,
		"auto_detect_tools", len(appCfg.Artifacts.AutoDetectTools))

	// Configure output filters. Default: strip emoji from LLM responses.
	outputFilters := filter.NewChain()
	outputFilters.Add("no_emoji", filter.NoEmoji)
	slog.Info("output filters registered", "filters", outputFilters.Names())

	// Set up MCP manager, tool broker, and self-service tools.
	mcpManager, tb, selfTools := initMCP(s)

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

	// Wire todo/plan store into the self-tools transport.
	selfTools.TodoStore = s
	selfTools.A2A = container.A2A

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
func initProviders() *provider.Registry {
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
	cliAdapters := []provider.CLIAdapter{
		provider.NewClaudeAdapter(),
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
	// Backwards-compat alias: "pty" → Claude adapter (if available).
	if ptyBridge := provider.NewPTYBridge(); ptyBridge != nil {
		registry.Register("pty", ptyBridge)
	}

	return registry
}

// initMCP sets up the MCP manager with built-in and user-configured servers,
// runs auto-discovery, and creates the tool broker.
func initMCP(s *store.Store) (*mcp.Manager, *toolclient.ToolClient, *mcp.SelfToolsTransport) {
	mcpManager := mcp.NewManager()

	homeDir, _ := os.UserHomeDir()
	if err := mcpManager.AddServer("dev", mcp.NewDevToolsTransport([]string{
		filepath.Join(homeDir, "Projects-apps"),
		filepath.Join(homeDir, "Projects"),
	})); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "dev", "err", err)
	}
	if err := mcpManager.AddServer("general", mcp.NewGeneralToolsTransport()); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "general", "err", err)
	}
	if err := mcpManager.AddServer("code", mcp.NewCodeExecTransport("")); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "code", "err", err)
	}
	selfTools := mcp.NewSelfToolsTransport(s)
	if err := mcpManager.AddServer("self", selfTools); err != nil {
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
	selfToolDefs := mcp.SelfToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("self-service", selfToolDefs)
	slog.Info("registered self-service built-in tools", "count", len(selfToolDefs))

	return mcpManager, tb, selfTools
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
			if err := m.AddStdioServer(cfg.Name, cfg.Command, args, envVars); err != nil {
				slog.Warn("mcp: failed to register persisted stdio server", "name", cfg.Name, "err", err)
			}
		case "sse":
			if err := m.AddHTTPServer(cfg.Name, cfg.URL); err != nil {
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

	srv := mcpserver.New(s, *sessionID)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s mcp: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
}
