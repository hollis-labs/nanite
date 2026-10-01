package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestResolveLayout_TightensOwnedDirsTo0700 pins go-apppaths v0.2.0+'s
// behavior as Nanite gets it (CW-20260930-0208): materializing the layout
// forces 0700 onto nanite's own directories, existing ones included, the way
// the live ~/.local/share/nanite (0775) and ~/.cache/nanite (0755) were left
// by v0.1.0.
func TestResolveLayout_TightensOwnedDirsTo0700(t *testing.T) {
	home := t.TempDir()
	for _, kv := range [][2]string{
		{"XDG_DATA_HOME", filepath.Join(home, "data")},
		{"XDG_STATE_HOME", filepath.Join(home, "state")},
		{"XDG_CACHE_HOME", filepath.Join(home, "cache")},
		{"XDG_CONFIG_HOME", filepath.Join(home, "config")},
	} {
		t.Setenv(kv[0], kv[1])
	}
	t.Setenv("NANITE_WORKSPACE", "default")
	t.Setenv("NANITE_DB_PATH", "")
	dataRoot := filepath.Join(home, "data", "nanite")
	cacheRoot := filepath.Join(home, "cache", "nanite")
	for _, dir := range []string{dataRoot, cacheRoot} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o775); err != nil { // #nosec G302 -- reproduces the loose mode v0.1.0 left on the live data root
			t.Fatal(err)
		}
	}

	layout, err := ResolveLayout()
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	for _, dir := range []string{layout.DataDir(), layout.StateDir(), layout.CacheDir(), layout.ConfigDir(), filepath.Dir(layout.MainDB())} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %#o, want 0700", dir, got)
		}
	}
}

// TestResolveLayout_TestLayoutIsOutsideRealHome fails if a layout resolved
// in this package's tests, materializing and so chmodding, lands under the
// developer's real home (CW-20260930-0208). TestMain's testhome guard is what
// keeps it out.
func TestResolveLayout_TestLayoutIsOutsideRealHome(t *testing.T) {
	layout, err := ResolveLayout()
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	testhome.AssertOutsideRealHome(t, layout.DataDir(), layout.StateDir(), layout.CacheDir(), layout.ConfigDir(), layout.MainDB())
}
