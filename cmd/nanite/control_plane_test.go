package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	paths "github.com/hollis-labs/libs/util/apppaths"
	"github.com/hollis-labs/nanite/internal/config"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// On Linux the agent control plane is Nanite's config, state and data dirs
// and the main database's directory, with only the worktree root writable
// inside them; the kill switch empties it and raises the health warning
// (CW-20261001-0143, CW-20261001-0188).
func TestAgentControlPlane(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("agent control-plane protection is Linux only (CW-20261001-0189)")
	}
	layout, err := config.ResolveLayout(paths.WithoutMaterialize())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(runtimeagent.ProtectEnv, "")
	base := []string{layout.ConfigDir(), layout.StateDir(), layout.DataDir()}

	t.Run("the layout's own database is protected with the data dir", func(t *testing.T) {
		cp := agentControlPlane(layout, layout.MainDB(), "/x/worktrees")
		want := append(slices.Clone(base), filepath.Dir(layout.MainDB()))
		if !slices.Equal(cp.Dirs, want) {
			t.Errorf("Dirs = %q, want %q", cp.Dirs, want)
		}
		if want := []string{"/x/worktrees"}; !slices.Equal(cp.Writable, want) {
			t.Errorf("Writable = %q, want only the worktree root %q", cp.Writable, want)
		}
		if slices.Contains(cp.Writable, filepath.Dir(layout.MainDB())) {
			t.Errorf("the database's directory %s is writable to agents", filepath.Dir(layout.MainDB()))
		}
	})

	t.Run("a database outside the layout is protected on its own", func(t *testing.T) {
		cp := agentControlPlane(layout, "/x/db/main.db", "/x/worktrees")
		if want := append(slices.Clone(base), "/x/db"); !slices.Equal(cp.Dirs, want) {
			t.Errorf("Dirs = %q, want %q", cp.Dirs, want)
		}
		if slices.Contains(cp.Writable, "/x/db") {
			t.Errorf("Writable = %q holds the database's directory", cp.Writable)
		}
	})

	t.Run("no database path adds nothing, and a root database dir is dropped", func(t *testing.T) {
		for _, db := range []string{"", "main.db", "/main.db"} {
			if cp := agentControlPlane(layout, db, "/x/worktrees"); !slices.Equal(cp.Dirs, base) {
				t.Errorf("db %q: Dirs = %q, want %q", db, cp.Dirs, base)
			}
		}
	})

	if w := agentProtectionWarnings(); w != nil {
		t.Errorf("health warns %q with protection on", w)
	}

	t.Setenv(runtimeagent.ProtectEnv, "0")
	if cp := agentControlPlane(layout, layout.MainDB(), "/x/worktrees"); len(cp.Dirs) != 0 || len(cp.Writable) != 0 {
		t.Errorf("%s=0 still protects %+v", runtimeagent.ProtectEnv, cp)
	}
	if w := agentProtectionWarnings(); len(w) != 1 {
		t.Errorf("health warnings with protection off = %q, want one", w)
	}
}

// A Nanite directory overridden to / or to an ancestor of the home
// directory is not protected: it would make everything read-only.
func TestSafeControlPlaneDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, ".config", "nanite")
	if err := os.MkdirAll(keep, 0o750); err != nil {
		t.Fatal(err)
	}
	got := safeControlPlaneDirs([]string{"/", home, filepath.Dir(resolved), keep})
	if want := []string{keep}; !slices.Equal(got, want) {
		t.Errorf("safeControlPlaneDirs = %q, want %q", got, want)
	}
}
