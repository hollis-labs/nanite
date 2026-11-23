package main

import (
	"maps"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/headless"
	"github.com/spf13/cobra"
)

// runHeadless runs a conversation as claude -p does for a program driving it (internal/headless):
// SIGINT interrupts the running turn.
func runHeadless(cmd *cobra.Command, a *agent.Agent, c *agent.Conversation, o headless.Options) error {
	if info, err := a.Info(cmd.Context()); err == nil && o.Model == "" {
		o.Model = info.Definition.Profile.Runtime.Model
	}
	if o.Model == "" {
		o.Model = firstNonEmptyEnv("NANITE_DEFAULT_MODEL", "claude-sonnet-5-5")
	}
	o.Cwd, _ = os.Getwd()
	o.Version = naniteVersion()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	ints := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-sig:
				select {
				case ints <- struct{}{}:
				case <-stop:
					return
				}
			}
		}
	}()
	o.Interrupts = ints
	return headless.Run(cmd.Context(), c, o, cmd.InOrStdin(), cmd.OutOrStdout())
}

// naniteVersion is the module version the binary was built from ("dev" for a local build).
func naniteVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func firstNonEmptyEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
