package main

import (
	"fmt"
	"os"
)

// cmdInstall is a deprecation shim. The framework-injection flow has moved to
// the `nanite-agent` binary (subcommand `init`) to disambiguate it from the
// future Nanite desktop installer. This shim prints a migration hint and
// exits non-zero so pipelines and humans see the rename immediately.
func cmdInstall(args []string) {
	_ = args
	fmt.Fprintln(os.Stderr, "nanite install is deprecated; use: nanite-agent init")
	os.Exit(1)
}
