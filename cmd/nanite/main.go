// Command nanite runs a single authored agent from its directory.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hollis-labs/libs/agent/agentdir"
	libpolicy "github.com/hollis-labs/libs/security/policy"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/policy"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "nanite:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	return runArgs(ctx, os.Args[1:])
}

func runArgs(ctx context.Context, argv []string) error {
	dir, args, err := extractDir(argv)
	if err != nil {
		return err
	}
	restore, err := useConfigFile(configIndependent(args))
	if err != nil {
		return err
	}
	defer restore()
	// Init and the Substrate commands do not require an agent database.
	dataDir := ""
	if requiresAgent(args) {
		if _, err := agentdir.Load(dir); err != nil {
			return fmt.Errorf("load agent directory: %w", err)
		}
		dataDir = filepath.Join(dir, ".nanite")
	}
	started, err := start(ctx, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()
	root := newRoot(started.App, dir)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

func configIndependent(args []string) bool {
	if hasHelpFlag(args) {
		return true
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version", "init", "validate", "help", "completion":
		return true
	default:
		return false
	}
}

func hasHelpFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "-p", "--prompt", "--output", "--resume":
			i++
		case "-h", "--help":
			return true
		}
	}
	return false
}

func requiresAgent(args []string) bool {
	return !configIndependent(args) && (len(args) == 0 || (args[0] != "config" && args[0] != "health"))
}

type localBoundary struct{}

func (localBoundary) Evaluate(context.Context, libpolicy.Request) (libpolicy.Decision, error) {
	return libpolicy.Decision{Effect: libpolicy.Allow, Reason: "delegate to authored agent tool rules"}, nil
}

func start(ctx context.Context, dataDir string) (*boot.Running, error) {
	policy.UsePDP(localBoundary{})
	return boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			path := ":memory:"
			if dataDir != "" {
				if err := nanitestore.PreparePrivateDB(dataDir); err != nil {
					return err
				}
				path = filepath.Join(dataDir, "nanite.db")
			}
			db, _ := app.Config["database"].(map[string]any)
			if db == nil {
				db = make(map[string]any)
				app.Config["database"] = db
			}
			sqlite, _ := db["sqlite"].(map[string]any)
			if sqlite == nil {
				sqlite = make(map[string]any)
				db["sqlite"] = sqlite
			}
			db["driver"] = "sqlite"
			sqlite["path"] = path
			return nil
		},
	})
}
