package main

import (
	"path/filepath"
	"runtime"

	"github.com/hollis-labs/go-apppaths/paths"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// agentControlPlane is the set of Nanite's own directories its agents may
// not write (CW-20261001-0143): config, state and data. Two directories
// inside them stay writable:
//   - the main database's directory. Each agent runs its own `nanite mcp
//     --db <main.db>` subprocess inside its sandbox, and that opens the
//     database read-write. Until it stops doing so (CW-20261001-0188), an
//     agent can still write main.db directly;
//   - the worktree root (wtBaseDir), where agents do their work.
//
// Linux only for now. go-sandbox's macOS write-protect has not been run on
// a Mac (its v0.5.0 CHANGELOG), and enforcement fails closed: an
// unverified seatbelt profile would refuse every agent launch there
// (CW-20261001-0189).
func agentControlPlane(layout paths.Layout, dbPath, worktreeRoot string) runtimeagent.ControlPlane {
	if runtime.GOOS != "linux" {
		return runtimeagent.ControlPlane{}
	}
	return runtimeagent.ControlPlane{
		Dirs:     []string{layout.ConfigDir(), layout.StateDir(), layout.DataDir()},
		Writable: []string{filepath.Dir(dbPath), worktreeRoot},
	}
}
