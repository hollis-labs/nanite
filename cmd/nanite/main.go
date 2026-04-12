package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	feotel "github.com/hollis-labs/go-otel"
	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/coordination"
	"github.com/hollis-labs/nanite/internal/worktree"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/filter"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpserver"
	"github.com/hollis-labs/nanite/pkg/models"
	"github.com/hollis-labs/nanite/internal/plugin"
	_ "github.com/hollis-labs/nanite/internal/plugin/allplugins" // registers all built-in plugins
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/server"
	"github.com/hollis-labs/nanite/internal/service"
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
	// Load agentrc config (user-level + project-level, merged).
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		log.Printf("warning: failed to load agentrc config: %v", cfgErr)
	} else {
		name := cfg.Project.Name
		if name == "" {
			name = "(unnamed)"
		}
		log.Printf("config loaded — project: %s, role: %s, root: %s", name, cfg.Role, cfg.ProjectRoot())
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8090, "HTTP listen port")
	dbPath := fs.String("db", "./"+brand.DefaultDBName, "SQLite database path")
	dev := fs.Bool("dev", false, "Development mode (skip embedded SPA)")
	fs.Parse(args)

	// Initialise OpenTelemetry tracing (otel).
	otelCtx := context.Background()
	otelShutdown, otelErr := feotel.Init(otelCtx, feotel.WithServiceName(brand.OTelService))
	if otelErr != nil {
		log.Printf("warning: OTel init failed: %v", otelErr)
	} else {
		defer otelShutdown(otelCtx)
	}

	// Open store and run migrations.
	s, err := store.New(*dbPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	// Seed default data.
	if err := s.Seed(); err != nil {
		log.Fatalf("failed to seed database: %v", err)
	}
	if err := s.SeedProviders(); err != nil {
		log.Fatalf("failed to seed providers: %v", err)
	}
	if err := s.SeedBuiltinTemplates(); err != nil {
		log.Fatalf("failed to seed templates: %v", err)
	}
	if err := s.SeedBuiltinPromptTemplates(); err != nil {
		log.Fatalf("failed to seed prompt templates: %v", err)
	}
	if err := s.SeedBuiltinModes(); err != nil {
		log.Fatalf("failed to seed modes: %v", err)
	}

	// Load core envelope types from manifest.
	if coreTypes := loadEnvelopeManifest("config/envelopes.yaml"); len(coreTypes) > 0 {
		chat.InitCoreTypes(coreTypes)
		log.Printf("envelope manifest: loaded %d core type(s)", len(coreTypes))
	}

	// Set up provider registry (API keys, Ollama, CLI adapters).
	registry := initProviders()

	// Load app-level config (tunables like presence throttle, artifact detection).
	appCfg, err := config.LoadAppConfig("config/" + brand.ConfigFileName + ".yaml")
	if err != nil {
		log.Printf("warning: failed to load app config: %v (using defaults)", err)
		appCfg = config.DefaultAppConfig()
	}
	log.Printf("app config loaded (cli_active_throttle=%ds, auto_detect_tools=%d)",
		appCfg.Presence.CLIActiveThrottleSeconds, len(appCfg.Artifacts.AutoDetectTools))

	// Configure output filters. Default: strip emoji from LLM responses.
	outputFilters := filter.NewChain()
	outputFilters.Add("no_emoji", filter.NoEmoji)
	log.Printf("output filters: %v", outputFilters.Names())

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
	pluginHost.RegisterService("store", s)
	pluginHost.RegisterService("mcp", mcpManager)
	pluginHost.RegisterService("toolclient", tb)
	log.Println("plugin host initialized")

	// --- Coordination store (Badger KV for multi-agent state) ---
	coordDir := filepath.Join(filepath.Dir(*dbPath), "coordination")
	coordStore, coordErr := coordination.NewBadgerStore(coordDir)
	if coordErr != nil {
		log.Printf("WARNING: coordination store failed to open: %v — multi-agent features disabled", coordErr)
		coordStore = nil
	} else {
		defer coordStore.Close()
		log.Println("coordination store: badger initialized at", coordDir)
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
		log.Printf("WARNING: worktree manager init failed: %v — worktree isolation disabled", wtErr)
		wtMgr = worktree.NewNoopManager()
	} else {
		log.Println("worktree manager: initialized at", wtBaseDir)
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
		log.Fatalf("failed to create service container: %v", err)
	}

	// Wire todo/plan store into the self-tools transport.
	selfTools.TodoStore = s
	selfTools.A2A = container.A2A

	// Restore non-terminal tasks from SQLite snapshot into coordination store.
	if container.Tasks != nil {
		if err := container.Tasks.Restore(context.Background()); err != nil {
			log.Printf("WARNING: task restore: %v", err)
		}
	}

	// Clean up orphaned worktrees from previous runs.
	if container.Worktrees != nil {
		if cleaned, wtCleanErr := container.Worktrees.CleanupOrphaned(nil); wtCleanErr != nil {
			log.Printf("WARNING: worktree orphan cleanup: %v", wtCleanErr)
		} else if cleaned > 0 {
			log.Printf("worktree cleanup: removed %d orphaned worktrees", cleaned)
		}
	}

	// Wire plugin host command registry from the container.
	pluginHost.SetCommandRegistry(container.Commands)
	pluginHost.RegisterService("container", container)
	if container.Tasks != nil {
		pluginHost.RegisterService("tasks", container.Tasks)
	}
	plugin.RegisterAutoTriggerHandler(pluginHost)

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

	// Shutdown handler.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		container.Shutdown()
		os.Exit(0)
	}()

	// Start periodic background workers (cleanup, snapshots, reapers).
	startBackgroundWorkers(container)

	// Start HTTP server.
	srv := server.New(s, a, *port, *dev, pluginHost)

	// Discover, load plugins, and re-discover MCP tools.
	pluginsDir := discoverAndLoadPlugins(pluginHost, *dbPath, mcpManager, s)
	srv.SetPluginsDir(pluginsDir)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
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
			log.Printf("%s provider registered (key from keychain)", spec.name)
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
		log.Println("azure-openai provider registered")
		registeredAPI = append(registeredAPI, "azure-openai")
	}

	if len(missingAPI) > 0 && len(registeredAPI) == 0 {
		log.Println("╔══════════════════════════════════════════════════════════════╗")
		log.Println("║  WARNING: No API providers configured — chat will not work! ║")
		log.Println("║  Set API keys in Settings → Providers or via environment.   ║")
		log.Println("╚══════════════════════════════════════════════════════════════╝")
	}

	// Always register Ollama — it requires no API key (local service).
	registry.Register("ollama", provider.NewOllama())
	log.Println("ollama provider registered (default host: http://localhost:11434)")

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
			log.Printf("pty provider registered: %s (%s)", ptyName, path)

			subName := "sub-" + adapter.Name()
			registry.Register(subName, provider.NewSubprocessBridge(adapter, path))
			log.Printf("subprocess provider registered: %s (%s)", subName, path)
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
	mcpManager.AddServer("dev", mcp.NewDevToolsTransport([]string{
		filepath.Join(homeDir, "Projects-apps"),
		filepath.Join(homeDir, "Projects"),
	}))
	mcpManager.AddServer("general", mcp.NewGeneralToolsTransport())
	mcpManager.AddServer("code", mcp.NewCodeExecTransport(""))
	selfTools := mcp.NewSelfToolsTransport(s)
	mcpManager.AddServer("self", selfTools)

	loadPersistedMCPServers(s, mcpManager)
	mcpManager.Broker = broker.NewLocalBroker(nil, broker.DefaultRules())
	if diff, err := mcpManager.AutoDiscover(context.Background(), s); err != nil {
		log.Printf("WARNING: MCP auto-discovery failed: %v", err)
	} else {
		log.Printf("MCP auto-discovery: %d tools total, %d added, %d removed",
			diff.Total, len(diff.Added), len(diff.Removed))
	}

	tb := toolclient.New(mcpManager, s, nil)
	if mcpManager.Broker != nil {
		tb.LocalBroker = mcpManager.Broker
		log.Printf("toolclient: sharing MCPManager broker (%d tool summaries)", len(mcpManager.Broker.AllTools()))
	}
	selfToolDefs := mcp.SelfToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("self-service", selfToolDefs)
	log.Printf("registered %d self-service built-in tools", len(selfToolDefs))

	return mcpManager, tb, selfTools
}

// startBackgroundWorkers launches periodic goroutines for cleanup, snapshots, and reapers.
func startBackgroundWorkers(container *service.Container) {
	// Periodic cleanup of saved tool outputs.
	go func() {
		truncate.Cleanup()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			truncate.Cleanup()
		}
	}()

	// Periodic task snapshot (flush Badger state to SQLite).
	if container.Tasks != nil {
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if err := container.Tasks.Snapshot(context.Background()); err != nil {
					log.Printf("task snapshot: %v", err)
				}
			}
		}()
	}

	// Periodic stale process reaper.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if killed := container.ProcessTracker.KillStale(5 * time.Minute); killed > 0 {
				log.Printf("stale process reaper: killed %d hung CLI processes", killed)
			}
		}
	}()

	// Periodic stale worker reaper.
	if container.Workers != nil {
		go func() {
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if stale := container.Workers.ReapStale(60 * time.Second); len(stale) > 0 {
					log.Printf("stale worker reaper: reaped %d workers", len(stale))
				}
			}
		}()
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
		log.Printf("WARNING: plugin discovery failed: %v", discErr)
	} else {
		loaded, loadErrs := plugin.LoadDiscovered(pluginHost, discovered)
		for _, e := range loadErrs {
			log.Printf("WARNING: %v", e)
		}
		log.Printf("plugins: discovered %d, loaded %d", len(discovered), len(loaded))
	}
	if builtins, builtinErrs := plugin.LoadRegisteredBuiltins(pluginHost); len(builtins) > 0 {
		for _, e := range builtinErrs {
			log.Printf("WARNING: %v", e)
		}
		log.Printf("plugins: loaded %d builtin(s)", len(builtins))
	}

	// Re-discover tools after plugins (they may register new MCP servers).
	if postDiff, err := mcpManager.AutoDiscover(context.Background(), s); err != nil {
		log.Printf("WARNING: post-plugin MCP discovery failed: %v", err)
	} else if len(postDiff.Added) > 0 {
		log.Printf("post-plugin MCP discovery: %d new tools added: %v", len(postDiff.Added), postDiff.Added)
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
		log.Fatalf("failed to read envelope manifest %s: %v", path, err)
	}

	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		log.Fatalf("failed to parse envelope manifest %s: %v", path, err)
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
		log.Printf("WARNING: failed to load persisted MCP servers: %v", err)
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
			m.AddStdioServer(cfg.Name, cfg.Command, args, envVars)
		case "sse":
			m.AddHTTPServer(cfg.Name, cfg.URL)
		default:
			log.Printf("mcp: unknown transport type %q for server %s, skipping", cfg.TransportType, cfg.Name)
		}
	}

	if len(servers) > 0 {
		log.Printf("mcp: loaded %d user-configured server(s) from database", len(servers))
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
