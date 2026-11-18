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
	restore, err := useConfigFile()
	if err != nil {
		return err
	}
	defer restore()
	dir, args, err := extractDir(os.Args[1:])
	if err != nil {
		return err
	}
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

func requiresAgent(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "init", "validate", "version", "config", "health", "help", "completion", "-h", "--help":
			return false
		default:
			return true
		}
	}
	return true
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
				if err := os.MkdirAll(dataDir, 0700); err != nil {
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
