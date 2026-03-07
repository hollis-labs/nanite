package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/hollis-labs/mentat-chat/internal/api"
	"github.com/hollis-labs/mentat-chat/internal/chat"
	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/server"
	"github.com/hollis-labs/mentat-chat/internal/store"
	"github.com/hollis-labs/mentat-chat/internal/workflow"
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

	// Set up provider registry.
	registry := provider.NewRegistry()
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		registry.Register("anthropic", provider.NewAnthropic())
		log.Println("anthropic provider registered")
	} else {
		log.Println("WARNING: ANTHROPIC_API_KEY not set — chat will not work")
		// Still register the provider so routes exist; it will return errors on use.
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

	// Load workflow definitions.
	wfLoader := workflow.NewLoader(*workflowDir)
	if err := wfLoader.LoadAll(); err != nil {
		log.Printf("WARNING: failed to load workflows from %s: %v", *workflowDir, err)
	} else {
		log.Printf("loaded %d workflow(s) from %s", len(wfLoader.List()), *workflowDir)
	}
	wfEngine := workflow.NewEngine(registry, s)

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
