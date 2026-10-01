package main

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestResolveDBPath_IsOutsideRealHome covers the path that chmodded the real
// ~/.local/share/nanite and ~/.cache/nanite during CW-20260930-0208's gate:
// TestMCPExportCLIKeepsEnvValues and TestMCPImportCLIDropsPlaceholders set
// NANITE_DB_PATH but not HOME/XDG, and the mcp CLI's resolveDBPath
// materializes the layout. Under TestMain's testhome guard both the database
// path and the materialized layout stay out of the real home.
func TestResolveDBPath_IsOutsideRealHome(t *testing.T) {
	// resolveDBPath has no silent default; the workspace name is the input
	// the live service gives it (NANITE_WORKSPACE=default in its unit).
	t.Setenv("NANITE_WORKSPACE", "default")
	db := resolveDBPath()
	layout, err := config.ResolveLayout()
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	testhome.AssertOutsideRealHome(t, db, layout.DataDir(), layout.StateDir(), layout.CacheDir(), layout.ConfigDir())
}
