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
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	tiamatotel "github.com/hollis-labs/otel"

	"github.com/hollis-labs/mentat/internal/api"
	"github.com/hollis-labs/mentat/internal/chat"
	"github.com/hollis-labs/mentat/internal/filter"
	"github.com/hollis-labs/mentat/internal/mcp"
	"github.com/hollis-labs/mentat/internal/provider"
	"github.com/hollis-labs/mentat/internal/server"
	"github.com/hollis-labs/mentat/internal/store"
	"github.com/hollis-labs/mentat/internal/toolclient"
	"github.com/hollis-labs/mentat/internal/truncate"
	"github.com/hollis-labs/mentat/internal/workflow"
	"github.com/hollis-labs/tool-broker/broker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mentat <command>")
		fmt.Fprintln(os.Stderr, "commands: serve")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdServe(args []string) {
	// Load .env file if present (never overrides existing env vars).
	if err := godotenv.Load(); err == nil {
		log.Println("loaded .env file")
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8090, "HTTP listen port")
	dbPath := fs.String("db", "./mentat.db", "SQLite database path")
	dev := fs.Bool("dev", false, "Development mode (skip embedded SPA)")
	workflowDir := fs.String("workflows", "./workflows", "Directory containing workflow YAML files")
	fs.Parse(args)

	// Initialise OpenTelemetry tracing (tiamat-otel).
	otelCtx := context.Background()
	otelShutdown, otelErr := tiamatotel.Init(otelCtx, tiamatotel.WithServiceName("mentat"))
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
	registry := provider.NewRegistry()
	var missingProviders []string
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		registry.Register("anthropic", provider.NewAnthropic())
		log.Println("anthropic provider registered")
	} else {
		missingProviders = append(missingProviders, "ANTHROPIC_API_KEY")
	}

	if os.Getenv("OPENAI_API_KEY") != "" {
		registry.Register("openai", provider.NewOpenAI())
		log.Println("openai provider registered")
	}

	if len(missingProviders) > 0 {
		log.Println("╔══════════════════════════════════════════════════════════════╗")
		log.Println("║  WARNING: Missing API keys — chat will not work!            ║")
		for _, k := range missingProviders {
			log.Printf("║  • %s not set                                  ║", k)
		}
		log.Println("║                                                              ║")
		log.Println("║  Create a .env file or export the variable before starting.  ║")
		log.Println("╚══════════════════════════════════════════════════════════════╝")
	}

	// Always register Ollama — it requires no API key (local service).
	registry.Register("ollama", provider.NewOllama())
	log.Println("ollama provider registered (default host: http://localhost:11434)")

	// Create chat engine.
	engine := chat.NewEngine(s, registry)

	// Configure output filters. Default: strip emoji from LLM responses.
	// Additional filters can be added to the chain here or via MENTAT_OUTPUT_FILTERS env var.
	outputFilters := filter.NewChain()
	if envFilters := os.Getenv("MENTAT_OUTPUT_FILTERS"); envFilters != "" {
		// Comma-separated list of filter names, e.g. "no_emoji,trim"
		names := splitFilterNames(envFilters)
		outputFilters = filter.FromNames(names)
		log.Printf("output filters from MENTAT_OUTPUT_FILTERS: %v", outputFilters.Names())
	} else {
		// Default: enable no_emoji filter
		outputFilters.Add("no_emoji", filter.NoEmoji)
		log.Printf("output filters (default): %v", outputFilters.Names())
	}
	engine.OutputFilters = outputFilters

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
	tb := toolclient.New(mcpManager, s, nil)

	// Register self-service tools as built-in (always available).
	selfToolDefs := mcp.SelfToolProviderDefinitions()
	tb.Builtins.RegisterBuiltins("self-service", selfToolDefs)
	log.Printf("registered %d self-service built-in tools", len(selfToolDefs))

	engine.ToolClient = tb

	// Set up orchestrator for task decomposition and delegation.
	orchestrator := chat.NewOrchestrator(registry, mcpManager)
	engine.Orchestrator = orchestrator
	log.Println("orchestrator initialized (decomposition + delegation enabled)")

	// Set up activity emitter to push events to Volon's GUI server.
	// Configured via VOLON_URL or VOLON_GUI_URL env var; disabled when unset.
	engine.Activity = chat.NewActivityEmitter("")

	// Clean up MCP subprocesses on shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down MCP transports...")
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

	// Start HTTP server.
	srv := server.New(s, a, *port, *dev)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// setupMCPServers configures MCP server connections.
// Uses stdio transports matching the ~/.claude.json MCP server config.
func setupMCPServers(m *mcp.Manager) {
	home, _ := os.UserHomeDir()

	// Volon — task/sprint/project management
	volonBin := home + "/Projects-apps/volon/volon"
	if _, err := os.Stat(volonBin); err == nil {
		m.AddStdioServer("volon", volonBin, []string{
			"--repo", home + "/Projects-apps/volon",
			"mcp",
		}, nil)
	} else {
		log.Printf("mcp: volon binary not found at %s, skipping", volonBin)
	}

	// Hadron — blueprint/automation engine
	hadronBin := home + "/Projects-apps/hadron/bin/hadrond"
	if _, err := os.Stat(hadronBin); err == nil {
		m.AddStdioServer("hadron", hadronBin, []string{
			"mcp",
			"-db", home + "/.hadron/state/hadron.db",
			"-logs", home + "/.hadron/logs",
			"-data", home + "/.hadron",
			"-token", "mentat-local-dev",
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

// splitFilterNames splits a comma-separated filter list into trimmed names.
func splitFilterNames(s string) []string {
	var names []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}
