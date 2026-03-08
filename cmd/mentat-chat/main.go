package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tiamatotel "github.com/hollis-labs/tiamat-otel"

	"github.com/hollis-labs/mentat-chat/internal/api"
	"github.com/hollis-labs/mentat-chat/internal/chat"
	"github.com/hollis-labs/mentat-chat/internal/mcp"
	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/server"
	"github.com/hollis-labs/mentat-chat/internal/store"
	"github.com/hollis-labs/mentat-chat/internal/truncate"
	"github.com/hollis-labs/mentat-chat/internal/workflow"
	"github.com/hollis-labs/tiamat-tool-broker/broker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mentat-chat <command>")
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
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8090, "HTTP listen port")
	dbPath := fs.String("db", "./mentat-chat.db", "SQLite database path")
	dev := fs.Bool("dev", false, "Development mode (skip embedded SPA)")
	workflowDir := fs.String("workflows", "./workflows", "Directory containing workflow YAML files")
	fs.Parse(args)

	// Initialise OpenTelemetry tracing (tiamat-otel).
	otelCtx := context.Background()
	otelShutdown, otelErr := tiamatotel.Init(otelCtx, tiamatotel.WithServiceName("mentat-chat"))
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

	// Set up provider registry.
	registry := provider.NewRegistry()
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		registry.Register("anthropic", provider.NewAnthropic())
		log.Println("anthropic provider registered")
	} else {
		log.Println("WARNING: ANTHROPIC_API_KEY not set — chat will not work")
		registry.Register("anthropic", provider.NewAnthropic())
	}

	if os.Getenv("OPENAI_API_KEY") != "" {
		registry.Register("openai", provider.NewOpenAI())
		log.Println("openai provider registered")
	}

	// Always register Ollama — it requires no API key (local service).
	registry.Register("ollama", provider.NewOllama())
	log.Println("ollama provider registered (default host: http://localhost:11434)")

	// Create chat engine.
	engine := chat.NewEngine(s, registry)

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

	// Initialize the tool broker with default rules before discovery.
	mcpManager.Broker = broker.NewLocalBroker(nil, broker.DefaultRules())

	// Discover tools from MCP servers (best-effort; servers may not be running).
	// Tools are automatically registered with the broker during discovery.
	if err := mcpManager.DiscoverTools(context.Background()); err != nil {
		log.Printf("WARNING: MCP tool discovery failed: %v", err)
	}

	engine.MCPManager = mcpManager

	// Set up activity emitter to push events to Volon's GUI server.
	engine.Activity = chat.NewActivityEmitter("")
	log.Println("activity emitter initialized (target: Volon GUI server)")

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

	// Create API layer.
	a := api.New(s, engine)
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
		cortexToken = "35bcccce3c726d9f269e6cf0c80a6557553a099004b19a174e2e46886fc2b979"
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
