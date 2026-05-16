package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/launcher"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdLaunch is the standalone agent-launch entry point (CW-20260515-0027,
// Phase 6). It starts a Nanite-managed CLI agent directly from a shared
// launch profile — no chat server, no browser dropdown, no Tether MCP in
// the loop.
//
// Usage:
//
//	nanite launch <profile-id> [flags]
//	nanite launch --catalog <dir> claude-smoke
//	nanite launch --dry-run claude-smoke           # compile + validate only
//
// The pipeline (see internal/launcher and docs/standalone-launcher.md):
// load the catalog → compile the profile through the bootprofile
// compiler → resolve deferred slots → project + validate a shared
// agentlaunch.LaunchPlan → start via Nanite's runtime (agent.Boot).
//
// When to use this vs the chat-managed launch: see
// docs/standalone-launcher.md §"When to use which".
func cmdLaunch(args []string) {
	fs := flag.NewFlagSet("launch", flag.ExitOnError)
	catalog := fs.String("catalog", "", "boot-profile catalog root (default: config boot_profile_catalog_path)")
	dbPath := fs.String("db", "./"+brand.DefaultDBName, "SQLite database path (used to persist the runtime row + plant .mcp.json)")
	dryRun := fs.Bool("dry-run", false, "compile + resolve + validate the launch plan, then exit without starting the agent")
	noWait := fs.Bool("no-wait", false, "return as soon as the agent process is started instead of blocking until it exits")
	dev := fs.Bool("dev", false, "development mode (use dev provider adapters with permissions skipped)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s launch <profile-id> [flags]\n\n", brand.BinaryName)
		fmt.Fprintln(os.Stderr, "Start a Nanite-managed agent from a shared launch profile, standalone (no chat server).")
		fmt.Fprintln(os.Stderr, "\nflags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}
	profileID := fs.Arg(0)

	// Resolve the catalog root: explicit --catalog flag wins, else the
	// agentrc config's boot_profile_catalog_path (the same field the
	// chat dropdown registry consumes).
	catalogPath := *catalog
	if catalogPath == "" {
		if cfg, _ := config.Load(); cfg != nil {
			catalogPath = cfg.ResolvedBootProfileCatalogPath()
		}
	}
	if catalogPath == "" {
		fmt.Fprintln(os.Stderr, "launch: no catalog configured — pass --catalog <dir> or set boot_profile_catalog_path in agentrc")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// --dry-run: compile + resolve + validate only. No store, no
	// adapters, no runtime — a pure compile-level check an operator (or
	// CI) can run to confirm a profile is launchable.
	if *dryRun {
		spec, plan, err := launcher.Plan(ctx, launcher.Config{
			CatalogPath: catalogPath,
			Profile:     profileID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "launch --dry-run: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("launch plan OK: profile=%s provider=%s runtime=%s workdir=%s\n",
			spec.ProfileID, plan.Provider.ID, plan.Runtime, plan.Workspace.Workdir)
		fmt.Printf("  boot prompt: %d bytes, %d slot(s), %d mcp allow-list entr(ies)\n",
			len(spec.BootPrompt), len(spec.Slots), len(plan.MCP.Allowlist))
		return
	}

	// Open the store so the runtime lifecycle row persists into the
	// agent_runtime table (introspectable / orphan-sweepable like any
	// chat-spawned session) and the planted .mcp.json names a real DB.
	s, err := store.New(context.Background(), *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch: open store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	// Reuse the exact provider-adapter construction the `serve` path
	// uses (initProviders) so a standalone launch dispatches to the same
	// claude/codex/opencode adapters with the same StreamingStdio shape.
	_, cliAdapters := initProviders(*dev)

	binPath := ""
	if exe, exeErr := os.Executable(); exeErr == nil {
		binPath = launcher.ResolveBinaryPath(exe)
	}

	res, err := launcher.Launch(ctx, launcher.Config{
		CatalogPath: catalogPath,
		Profile:     profileID,
		CLIAdapters: cliAdapters,
		BinaryPath:  binPath,
		DBPath:      s.DBPath(),
		Store:       s,
		Wait:        !*noWait,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("launched: session=%s provider=%s\n", res.SessionID, res.Provider)
	fmt.Printf("  boot dir:      %s\n", res.BootDir)
	fmt.Printf("  workspace dir: %s\n", res.WorkspaceDir)
	if *noWait {
		fmt.Println("  (--no-wait: agent process started; not blocking on exit)")
	}
}
