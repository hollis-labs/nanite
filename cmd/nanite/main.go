// Command nanite runs a single authored agent from its directory.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"

	"github.com/hollis-labs/libs/agent/agentdir"
	libpolicy "github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/control"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workspace"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/policy"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if code := guardedMain(ctx, run, os.Stderr); code != 0 {
		stop()
		os.Exit(code)
	}
}

// guardedMain runs the CLI under the outermost panic guard. A panic that
// escapes every inner boundary prints a ref and exits with
// panics.ExitSoftware instead of a stack dump; the redacted record goes to
// standard error.
func guardedMain(ctx context.Context, run func(context.Context) error, stderr io.Writer) int {
	guard := panics.New(panics.Config{Reporter: panics.StderrReporter(stderr), Redact: mainRedactor()})
	err := guard.Call("nanite main", func() error { return run(ctx) })
	if err == nil {
		return 0
	}
	fmt.Fprintln(stderr, "nanite:", err)
	printIntegrity(stderr, err)
	if _, ok := panics.Ref(err); ok {
		return panics.ExitSoftware
	}
	return 1
}

// mainRedactor scrubs known secret shapes from a panic record. Without one
// the guard withholds the value entirely.
func mainRedactor() func(string) string {
	r, err := redact.New()
	if err != nil {
		return nil
	}
	return r.Text
}

func run(ctx context.Context) error {
	return runArgs(ctx, os.Args[1:])
}

func runArgs(ctx context.Context, argv []string) error {
	dir, dirGiven, args, err := extractDirGiven(argv)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "stop" && !hasHelpFlag(args) {
		if len(args) != 2 || args[1] != "--all" {
			return fmt.Errorf("usage: nanite stop --all --dir <agent-directory>")
		}
		return control.Stop(ctx, filepath.Join(dir, ".nanite"))
	}
	restore, err := useConfigFile(configIndependent(args))
	if err != nil {
		return err
	}
	defer restore()
	// Init and the Substrate commands do not require an agent database.
	// auth status reads the agent's provider: in a plain directory that is the home's.
	if len(args) > 0 && args[0] == "auth" && !dirGiven && !workspace.IsAgentDir(dir) {
		if home, err := workspace.Home(); err == nil && workspace.IsAgentDir(home) {
			dir = home
		}
	}
	dataDir := ""
	var ws *wsMode
	if requiresAgent(args) {
		// Not an agent directory and no -C: workspace mode, with the home as the agent.
		if ws, err = detectWorkspace(dirGiven, dir, args, os.Stderr); err != nil {
			return err
		}
		agentDir := dir
		if ws != nil {
			agentDir = ws.home
		}
		if _, err := agentdir.Load(agentDir); err != nil {
			return fmt.Errorf("load agent directory: %w", err)
		}
		dataDir = filepath.Join(agentDir, ".nanite")
		if ws != nil {
			dataDir = ws.dataDir
		}
	}
	started, err := start(ctx, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()
	root := newRootFor(started.App, dir, ws)
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
	case "version", "--version", "-v", "init", "validate", "help", "completion", "stop", "tools", "agents":
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
		case "-p", "--prompt":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case "--output", "--resume", "--events", "--session-id", "--input-format", "--output-format":
			i++
		case "-h", "--help":
			return true
		}
	}
	return false
}

func requiresAgent(args []string) bool {
	return !configIndependent(args) && (len(args) == 0 || (args[0] != "config" && args[0] != "health" && args[0] != "auth"))
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
