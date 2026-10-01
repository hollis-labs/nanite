package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/hollis-labs/go-apppaths/paths"
	"github.com/hollis-labs/go-sandbox/sandbox"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// agentControlPlane is the set of Nanite's own directories its agents may
// not write (CW-20261001-0143): config, state and data, and the main
// database's directory (CW-20261001-0188). The database is inside the data
// directory by default, and protected with it; a --db or NANITE_DB_PATH
// outside the layout gets its own directory protected too. Only the worktree
// root (wtBaseDir), where agents do their work, stays writable inside them.
//
// An agent's own `nanite mcp` no longer opens the database: it forwards to
// the live API (CW-20261001-0188), so nothing in an agent's tree needs the
// database's directory.
//
// Linux only for now. go-sandbox's macOS write-protect has not been run on
// a Mac (its v0.5.0 CHANGELOG), and enforcement fails closed: an
// unverified seatbelt profile would refuse every agent launch there
// (CW-20261001-0189).
//
// NANITE_SANDBOX_PROTECT=0 turns protection off (runtimeagent.ProtectEnv),
// with a warning here and in /api/health (agentProtectionWarnings). A
// backend that cannot write-protect is logged as an ERROR: every agent
// launch is then refused until it is fixed or protection is turned off.
func agentControlPlane(layout paths.Layout, dbPath, worktreeRoot string) runtimeagent.ControlPlane {
	if runtime.GOOS != "linux" {
		return runtimeagent.ControlPlane{}
	}
	if !runtimeagent.ProtectionEnabled() {
		slog.Warn("AGENT SANDBOX PROTECTION IS OFF: agents Nanite launches can write Nanite's own config, state and data",
			"env", runtimeagent.ProtectEnv+"=0")
		return runtimeagent.ControlPlane{}
	}
	if caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto); !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		slog.Error("the sandbox backend cannot write-protect paths, so every agent launch will be refused; install bubblewrap, or set "+runtimeagent.ProtectEnv+"=0 to launch agents without protection",
			"backend", caps.Backend, "goos", caps.GOOS)
	}
	dirs := []string{layout.ConfigDir(), layout.StateDir(), layout.DataDir()}
	// Always offered, not only when it lies outside the layout: a database
	// inside the data directory is nested in it and dropped as redundant, and
	// one that is not (a --db override) is protected on its own.
	if dbDir := filepath.Dir(dbPath); dbPath != "" && dbDir != "." {
		dirs = append(dirs, dbDir)
	}
	return runtimeagent.ControlPlane{
		Dirs:     safeControlPlaneDirs(dirs),
		Writable: []string{worktreeRoot},
	}
}

// agentProtectionWarnings is what /api/health reports about agent
// protection: a warning while an operator has turned it off.
func agentProtectionWarnings() []string {
	if runtime.GOOS == "linux" && !runtimeagent.ProtectionEnabled() {
		return []string{"agent sandbox protection is off (" + runtimeagent.ProtectEnv + "=0): agents can write Nanite's config, state and data"}
	}
	return nil
}

// safeControlPlaneDirs drops a directory that is / or contains the home
// directory: an XDG override pointing a Nanite directory at one of those
// would make everything an agent works in read-only.
func safeControlPlaneDirs(dirs []string) []string {
	home, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	var out []string
	for _, d := range dirs {
		clean := filepath.Clean(d)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			clean = resolved
		}
		if clean == string(filepath.Separator) || (home != "" && containsPath(clean, home)) {
			slog.Warn("not write-protecting a Nanite directory that is / or contains the home directory", "dir", d)
			continue
		}
		out = append(out, d)
	}
	return out
}

// containsPath reports whether dir is path or one of its ancestors.
func containsPath(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
