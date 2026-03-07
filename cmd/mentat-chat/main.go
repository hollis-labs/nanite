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

	// Create chat engine.
	engine := chat.NewEngine(s, registry)

	// Create API layer.
	a := api.New(s, engine)

	// Start HTTP server.
	srv := server.New(s, a, *port, *dev)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
