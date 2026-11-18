// Command nanite is a class 2 tool built on Substrate. The shell (config, logging, health, CLI skeleton)
// comes from the manifest; this package and internal/ are the tool.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	libpolicy "github.com/hollis-labs/libs/security/policy"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/policy"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	started, err := start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()
	return newRoot(ctx, started.App).ExecuteContext(ctx)
}

// newRoot is the command tree over the started app: Substrate supplies version, config and health.
func newRoot(ctx context.Context, app *module.App) *cobra.Command {
	root := cli.Root(app)
	root.AddCommand(&cobra.Command{
		Use:   "hello",
		Short: "Say hello (replace with the tool's commands)",
		Args:  cobra.NoArgs,
		Run:   func(c *cobra.Command, _ []string) { _, _ = fmt.Fprintln(c.OutOrStdout(), "hello") },
	})
	return root
}

// scaffoldPDP denies authorization while the CLI is only a command scaffold.
type scaffoldPDP struct{}

func (scaffoldPDP) Evaluate(context.Context, libpolicy.Request) (libpolicy.Decision, error) {
	return libpolicy.Decision{Effect: libpolicy.Deny, Reason: "Nanite tool policy is not configured"}, nil
}

func start(ctx context.Context) (*boot.Running, error) {
	policy.UsePDP(scaffoldPDP{})
	return boot.Start(ctx, substrateApp, substrateModules, substrateConfigJSON)
}
