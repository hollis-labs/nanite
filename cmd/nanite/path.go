package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/hollis-labs/go-apppaths/paths"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
)

// cmdPath implements the `nanite path` subcommand. It prints nanite's resolved
// on-disk layout — the go-apppaths roots, the active workspace, and the main
// database — plus the nanite-derived extras (config.yaml, the coordination/
// and worktrees/ state dirs, the plugin-data / plugin-cache roots, and the
// plugins/ discovery dir). It is the introspection surface the go-apppaths
// cutover uses to confirm where the daemon reads and writes before a deploy.
//
// Resolution honors every override the running daemon would see — the
// NANITE_DB_PATH / NANITE_WORKSPACE env vars (native to go-apppaths), the
// legacy NANITE_DB compat alias, and the $XDG_*_HOME vars. It uses
// paths.WithoutMaterialize() so introspection never creates directories.
//
// CW-20260517-0061. Mirrors torque/cmd/torque/path.go and
// tesseract/cmd/tesseract/path.go.
func cmdPath(args []string) {
	opts := []paths.Option{paths.WithoutMaterialize()}
	// Reflect the legacy NANITE_DB compat alias so `nanite path` prints the
	// same main-db the daemon's resolveDBPathWith would resolve. NANITE_DB_PATH
	// is read natively inside paths.Resolve and needs no shim here.
	if legacy := os.Getenv(brand.Env("DB")); legacy != "" {
		opts = append(opts, paths.WithDBOverride(legacy))
	}
	layout, err := config.ResolveLayout(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s path: resolve layout: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, e := range layout.Describe() {
		fmt.Fprintf(w, "%s\t%s\n", e.Label, e.Value)
	}
	fmt.Fprintf(w, "config-file\t%s\n", filepath.Join(layout.ConfigDir(), "config.yaml"))
	fmt.Fprintf(w, "coordination\t%s\n", filepath.Join(layout.StateDir(), "coordination"))
	fmt.Fprintf(w, "worktrees\t%s\n", filepath.Join(layout.StateDir(), "worktrees"))
	fmt.Fprintf(w, "plugin-data\t%s\n", filepath.Join(layout.DataDir(), "plugin-data"))
	fmt.Fprintf(w, "plugin-cache\t%s\n", filepath.Join(layout.CacheDir(), "plugin-cache"))
	fmt.Fprintf(w, "plugins\t%s\n", resolvePluginsDir())
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "%s path: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
}
