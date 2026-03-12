package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/hollis-labs/mentat-chat/internal/cli"
)

func cmdCLI(args []string) {
	fs := flag.NewFlagSet("cli", flag.ExitOnError)
	serverURL := fs.String("server", "http://127.0.0.1:8090", "mentat-chat server URL")
	agent := fs.String("agent", "mentat", "Agent slug to use")
	mode := fs.String("mode", "", "Agent mode (default: agent's default mode)")
	workspace := fs.String("workspace", "", "Workspace ID (default: first available)")
	project := fs.String("project", "", "Project ID")
	noColor := fs.Bool("no-color", false, "Disable color output")
	noUsage := fs.Bool("no-usage", false, "Hide token usage stats")
	autoStart := fs.Bool("auto-start", false, "Auto-start server if not running")
	fs.Parse(args)

	cfg := cli.Config{
		ServerURL:   *serverURL,
		Agent:       *agent,
		Mode:        *mode,
		WorkspaceID: *workspace,
		ProjectID:   *project,
		NoColor:     *noColor,
		ShowUsage:   !*noUsage,
		AutoStart:   *autoStart,
	}

	repl := cli.NewREPL(cfg)
	if err := repl.Run(context.Background()); err != nil {
		log.Fatalf("cli error: %v", err)
	}
	os.Exit(0)
}
