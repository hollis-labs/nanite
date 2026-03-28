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
	"syscall"
	"time"

	"database/sql"

	_ "github.com/lib/pq" // Postgres driver for Nexus messaging

	feotel "github.com/hollis-labs/otel"

	"github.com/hollis-labs/conduit/internal/config"

	"github.com/hollis-labs/conduit/internal/api"
	"github.com/hollis-labs/conduit/internal/chat"
	"github.com/hollis-labs/conduit/internal/filter"
	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/hollis-labs/conduit/internal/mcpserver"
	"github.com/hollis-labs/conduit/internal/plugin"
	_ "github.com/hollis-labs/conduit/internal/plugin/allplugins" // registers all built-in plugins
	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/secrets"
	"github.com/hollis-labs/conduit/internal/server"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/conduit/internal/toolclient"
	"github.com/hollis-labs/conduit/internal/truncate"
	"github.com/hollis-labs/conduit/internal/workflow"
	"github.com/hollis-labs/nexus/messaging"
	"github.com/hollis-labs/tool-broker/broker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: conduit <command>")
		fmt.Fprintln(os.Stderr, "commands: serve, plugin")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "plugin":
		cmdPlugin(os.Args[2:])
	case "mcp":
		cmdMCP(os.Args[2:])
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
	dbPath := fs.String("db", "./conduit.db", "SQLite database path")
	dev := fs.Bool("dev", false, "Development mode (skip embedded SPA)")
	workflowDir := fs.String("workflows", "./workflows", "Directory containing workflow YAML files")
	fs.Parse(args)

	// Initialise OpenTelemetry tracing (otel).
	otelCtx := context.Background()
	otelShutdown, otelErr := feotel.Init(otelCtx, feotel.WithServiceName("conduit"))
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
	if err := s.SeedBuiltinSkills(); err != nil {
		log.Fatalf("failed to seed skills: %v", err)
	}
	if err := s.SeedBuiltinPromptTemplates(); err != nil {
		log.Fatalf("failed to seed prompt templates: %v", err)
	}
	if err := s.SeedBuiltinModes(); err != nil {
		log.Fatalf("failed to seed modes: %v", err)
	}
	if err := s.SeedAgentSkillBindings(); err != nil {
		log.Fatalf("failed to seed agent skill bindings: %v", err)
	}

	// Set up provider registry.
	// Key resolution: OS keychain → environment variable → skip.
	registry := provider.NewRegistry()

	// resolveKey reads the API key from the OS keychain only.
	resolveKey := func(providerID string) string {
		return secrets.Get(secrets.ProviderKeyName(providerID))
	}

	// API providers: register if a key is available from keychain or env.
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
			// PTY bridge (unix only, higher fidelity).
			ptyName := "pty-" + adapter.Name()
			registry.Register(ptyName, provider.NewPTYBridgeWithAdapter(adapter, path))
			log.Printf("pty provider registered: %s (%s)", ptyName, path)

			// Subprocess bridge (all platforms, pipe-based fallback).
			subName := "sub-" + adapter.Name()
			registry.Register(subName, provider.NewSubprocessBridge(adapter, path))
			log.Printf("subprocess provider registered: %s (%s)", subName, path)
		}
	}
	// Backwards-compat alias: "pty" → Claude adapter (if available).
	if ptyBridge := provider.NewPTYBridge(); ptyBridge != nil {
		registry.Register("pty", ptyBridge)
	}

	// Load app-level config (tunables like presence throttle, artifact detection).
	appCfg, err := config.LoadAppConfig("config/conduit.yaml")
	if err != nil {
		log.Printf("warning: failed to load app config: %v (using defaults)", err)
		appCfg = config.DefaultAppConfig()
	}
	log.Printf("app config loaded (cli_active_throttle=%ds, auto_detect_tools=%d)",
		appCfg.Presence.CLIActiveThrottleSeconds, len(appCfg.Artifacts.AutoDetectTools))

	// Create chat engine. Utility provider/model read from DB settings first,
	// then env vars, then defaults. See Engine.NewEngine for cascade.
	engine := chat.NewEngine(s, registry)
	engine.AppConfig = appCfg

	// Configure output filters. Default: strip emoji from LLM responses.
	outputFilters := filter.NewChain()
	outputFilters.Add("no_emoji", filter.NoEmoji)
	log.Printf("output filters: %v", outputFilters.Names())
	engine.OutputFilters = outputFilters

	// Configure CLI process concurrency limit. Default: 10. Set to 0 for unlimited.
	if maxProcs := os.Getenv("CONDUIT_MAX_CLI_PROCESSES"); maxProcs != "" {
		if n, err := strconv.Atoi(maxProcs); err == nil && n >= 0 {
			engine.ProcessTracker.MaxProcesses = n
			log.Printf("CLI process concurrency limit: %d", n)
		}
	} else {
		engine.ProcessTracker.MaxProcesses = 10
	}

	// Set up MCP manager with stdio transports (matching ~/.claude.json config).
	mcpManager := mcp.NewManager()
	setupMCPServers(mcpManager)

	// Register built-in dev tools (grep, read, write) scoped to common project dirs.
	homeDir, _ := os.UserHomeDir()
	devTools := mcp.NewDevToolsTransport([]string{
		filepath.Join(homeDir, "Projects-apps"),
		filepath.Join(homeDir, "Projects"),
	})
	mcpManager.AddServer("dev", devTools)

	// Register built-in general utility tools.
	generalTools := mcp.NewGeneralToolsTransport()
	mcpManager.AddServer("general", generalTools)

	// Register self-service tools (skill/agent/workflow CRUD).
	selfTools := mcp.NewSelfToolsTransport(s)
	mcpManager.AddServer("self", selfTools)

	// Load user-configured MCP servers from the database.
	loadPersistedMCPServers(s, mcpManager)

	// Initialize the tool broker with default rules before discovery.
	mcpManager.Broker = broker.NewLocalBroker(nil, broker.DefaultRules())

	// Discover tools from MCP servers and auto-sync with skills table.
	diff, err := mcpManager.AutoDiscover(context.Background(), s)
	if err != nil {
		log.Printf("WARNING: MCP auto-discovery failed: %v", err)
	} else {
		log.Printf("MCP auto-discovery: %d tools total, %d added, %d removed",
			diff.Total, len(diff.Added), len(diff.Removed))
	}

	engine.MCPManager = mcpManager

	// Create tool broker for permission-checked tool access.
	// Share the MCPManager's broker so the ToolClient has all discovered tools.
	// Without this, the ToolClient creates its own empty broker and tool selection
	// falls back to direct MCP discovery, bypassing intent scoring.
	tb := toolclient.New(mcpManager, s, nil)
	if mcpManager.Broker != nil {
		tb.LocalBroker = mcpManager.Broker
		log.Printf("toolclient: sharing MCPManager broker (%d tool summaries)", len(mcpManager.Broker.AllTools()))
	}

	// Register self-service tools as built-in (always available).
	selfToolDefs := mcp.SelfToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("self-service", selfToolDefs)
	log.Printf("registered %d self-service built-in tools", len(selfToolDefs))

	engine.ToolClient = tb

	// Set up orchestrator for task decomposition and delegation.
	orchestrator := chat.NewOrchestrator(registry, mcpManager)
	engine.Orchestrator = orchestrator
	log.Println("orchestrator initialized (decomposition + delegation enabled)")

	// Set up activity emitter to push events to Engine's activity feed.
	// Configured via ENGINE_ACTIVITY_URL env var; disabled when unset.
	engine.Activity = chat.NewActivityEmitter("")

	// Clean up MCP subprocesses and CLI processes on shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		engine.Shutdown()
		mcpManager.Close()
		os.Exit(0)
	}()

	// Periodic cleanup of saved tool outputs (every hour, removes files older than 7 days).
	go func() {
		truncate.Cleanup() // run once on startup
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			truncate.Cleanup()
		}
	}()

	// Periodic stale process reaper — kills CLI processes idle for over 5 minutes.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if killed := engine.ProcessTracker.KillStale(5 * time.Minute); killed > 0 {
				log.Printf("stale process reaper: killed %d hung CLI processes", killed)
			}
		}
	}()

	// Load workflow definitions from files and database.
	wfLoader := workflow.NewLoader(*workflowDir)
	wfLoader.SetDB(s.DB)
	if err := wfLoader.LoadAll(); err != nil {
		log.Printf("WARNING: failed to load workflows: %v", err)
	} else {
		log.Printf("loaded %d workflow(s)", len(wfLoader.List()))
	}
	wfEngine := workflow.NewEngine(registry, s)
	wfEngine.MCPManager = mcpManager

	// Wire workflow engine into chat engine for /workflow triggers.
	engine.WorkflowEngine = wfEngine
	engine.WorkflowLoader = wfLoader

	// Create API layer.
	a := api.New(s, engine)
	a.MCPManager = mcpManager
	a.ToolClient = tb
	a.WorkflowLoader = wfLoader
	a.WorkflowEngine = wfEngine

	// Connect to Engine Postgres for Nexus A2A messaging.
	// Uses ENGINE_POSTGRES_DSN env var, then a default local DSN.
	// When unavailable, A2A falls back to SQLite.
	nexusDSN := os.Getenv("ENGINE_POSTGRES_DSN")
	if nexusDSN == "" {
		nexusDSN = "postgres://localhost/engine?sslmode=disable"
	}
	engineDB, pgErr := sql.Open("postgres", nexusDSN)
	if pgErr != nil {
		log.Printf("WARNING: cannot open Engine Postgres (%s): %v — A2A will use SQLite fallback", nexusDSN, pgErr)
	} else if pgErr := engineDB.Ping(); pgErr != nil {
		log.Printf("WARNING: cannot reach Engine Postgres (%s): %v — A2A will use SQLite fallback", nexusDSN, pgErr)
		engineDB.Close()
		engineDB = nil
	}
	if engineDB != nil {
		engineDB.SetMaxOpenConns(5)
		engineDB.SetMaxIdleConns(2)
		engineDB.SetConnMaxLifetime(5 * time.Minute)
		a.NexusMsg = messaging.NewPostgresStore(engineDB)
		defer engineDB.Close()
		log.Printf("nexus messaging: connected to Engine Postgres (%s)", nexusDSN)
	}

	// Create plugin host.
	logger := plugin.NewLogger("conduit-plugin")
	pluginHost := plugin.NewHost(nil, logger)

	// Wire DB store for plugin config persistence.
	pluginHost.SetStore(s)

	// Register core services for plugin access.
	pluginHost.RegisterService("store", s)
	pluginHost.RegisterService("engine", engine)
	pluginHost.RegisterService("mcp", mcpManager)
	pluginHost.RegisterService("toolclient", tb)
	log.Println("plugin host initialized")

	// Start HTTP server — this sets the router on the plugin host.
	srv := server.New(s, a, *port, *dev, pluginHost)

	// Resolve plugins directory for discovery and management API.
	// Auto-discover and load plugins from the plugins/ directory.
	// Plugins self-register via init() in the allplugins import above.
	pluginsDir := filepath.Join(filepath.Dir(*dbPath), "plugins")
	if envDir := os.Getenv("CONDUIT_PLUGINS_DIR"); envDir != "" {
		pluginsDir = envDir
	}
	discovered, discErr := plugin.DiscoverPlugins(pluginsDir)
	if discErr != nil {
		log.Printf("WARNING: plugin discovery failed: %v", discErr)
	} else {
		loaded, loadErrs := plugin.LoadDiscovered(pluginHost, discovered)
		for _, e := range loadErrs {
			log.Printf("WARNING: %v", e)
		}
		log.Printf("plugins: discovered %d, loaded %d", len(discovered), len(loaded))
	}

	// Re-discover tools after plugins — plugins may register new MCP servers
	// (e.g., support-kb) that weren't present during initial auto-discovery.
	postPluginDiff, err := mcpManager.AutoDiscover(context.Background(), s)
	if err != nil {
		log.Printf("WARNING: post-plugin MCP discovery failed: %v", err)
	} else if len(postPluginDiff.Added) > 0 {
		log.Printf("post-plugin MCP discovery: %d new tools added: %v", len(postPluginDiff.Added), postPluginDiff.Added)
	}

	// Register plugin management API routes (install/uninstall/disable/enable).
	srv.SetPluginsDir(pluginsDir)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// setupMCPServers configures MCP server connections.
// Uses stdio transports matching the ~/.claude.json MCP server config.
func setupMCPServers(m *mcp.Manager) {
	home, _ := os.UserHomeDir()

	// Engine — task/sprint/project management (formerly Volon)
	engineBin := home + "/go/bin/engine"
	if _, err := os.Stat(engineBin); err == nil {
		m.AddStdioServer("engine", engineBin, []string{
			"mcp",
		}, []string{
			"ENGINE_REPO=" + home + "/Projects-apps/fragments-engine/engine",
			"ENGINE_POSTGRES_DSN=postgres://localhost/engine?sslmode=disable",
		})
	} else {
		log.Printf("mcp: engine binary not found at %s, skipping", engineBin)
	}

	// Hadron — blueprint/automation engine
	hadronBin := home + "/Projects-apps/hadron/bin/hadrond"
	if _, err := os.Stat(hadronBin); err == nil {
		m.AddStdioServer("hadron", hadronBin, []string{
			"mcp",
			"-db", home + "/.hadron/state/hadron.db",
			"-logs", home + "/.hadron/logs",
			"-data", home + "/.hadron",
			"-token", "conduit-local-dev",
			"-token-scopes", "run.write,schedule.write,pipeline.write",
		}, nil)
	} else {
		log.Printf("mcp: hadron binary not found at %s, skipping", hadronBin)
	}

	// Cortex — context/memory registry
	cortexBin := home + "/Projects-apps/cortex/contextd"
	cortexToken := os.Getenv("CORTEX_MCP_TOKEN")
	if cortexToken == "" {
		cortexToken = "5c116cf443a1e70de66f6ac242e1f06161837dca1f7a62a3bd63494bb0b0d4bb"
	}
	if _, err := os.Stat(cortexBin); err == nil {
		m.AddStdioServer("cortex", cortexBin, []string{
			"mcp",
			"-token", cortexToken,
		}, []string{
			"CONTEXTD_ROOT=" + home + "/.cortex",
		})
	} else {
		log.Printf("mcp: cortex binary not found at %s, skipping", cortexBin)
	}
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

// cmdMCP runs the Conduit MCP server on stdio. Claude CLI (or any MCP client)
// spawns this as a subprocess and communicates via JSON-RPC on stdin/stdout.
func cmdMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	dbPath := fs.String("db", "./conduit.db", "SQLite database path")
	sessionID := fs.String("session", "", "Conduit session ID")
	fs.Parse(args)

	s, err := store.New(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conduit mcp: open db: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	srv := mcpserver.New(s, *sessionID)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "conduit mcp: %v\n", err)
		os.Exit(1)
	}
}

