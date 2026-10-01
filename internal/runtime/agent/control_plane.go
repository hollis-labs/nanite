package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/go-sandbox/sandbox"
)

// control_plane.go write-protects Nanite's own state from the agents it
// launches (CW-20261001-0143, adopting CW-20260930-0237). An agent CLI runs
// as the operator's uid, so without this it could rewrite Nanite's config,
// coordination state or catalogs to grant itself authority.
//
// Every launch hands go-agent-wrapper the protected directories
// (wrapper.Config.ProtectedPaths). A native launch gets agentkit's minimal
// host-filesystem profile with those directories read-only, because
// Nanite's own SandboxProfile carries no ID. An ACP launch needs a resolved
// policy to merge them into, or the wrapper refuses it, so it gets
// acpControlPlanePolicy.
//
// The boundary is go-sandbox's: direct writes into a protected directory
// fail, from the agent and from every process it starts inside the
// sandbox. Writes the agent delegates to a same-uid process outside it
// (`systemd-run --user`, a terminal multiplexer, a host app's API) are not
// stopped, and neither is persistence planted elsewhere under $HOME that
// later runs outside the sandbox (a shell rc file, a git hook).

// ControlPlane names the directories Nanite's agents must never write.
type ControlPlane struct {
	// Dirs are Nanite's control-plane directories: config, state and data.
	// Missing entries are skipped; protection covers what exists at launch.
	Dirs []string
	// Writable are directories inside Dirs that agents must still write:
	// the main database's directory (the agent's own `nanite mcp`
	// subprocess opens it) and the worktree root agents work in.
	Writable []string
}

// protectedFor returns the real-path directories to protect for one launch.
// A Dirs entry that contains a writable root (Writable, or one of the
// launch's own: work dir, workspace, boot dir) is not protected whole: its
// child directories are, except the ones leading to that root, recursively.
// Files directly inside a split directory therefore stay writable;
// go-sandbox protects directories only, because a file's own directory
// stays writable and an atomic save would replace it.
func (c ControlPlane) protectedFor(launchWritable ...string) []string {
	var writable []string
	for _, w := range append(slices.Clone(c.Writable), launchWritable...) {
		if w = realDir(w, true); w != "" {
			writable = append(writable, w)
		}
	}
	var out []string
	for _, d := range c.Dirs {
		if d = realDir(d, false); d != "" {
			out = protectAround(out, d, writable)
		}
	}
	// Sorted, a directory precedes everything inside it, so an entry nested
	// in one already kept (two overlapping Dirs) is dropped.
	slices.Sort(out)
	var kept []string
	for _, p := range out {
		if !slices.ContainsFunc(kept, func(k string) bool { return pathWithin(p, k) }) {
			kept = append(kept, p)
		}
	}
	return kept
}

func protectAround(out []string, dir string, writable []string) []string {
	split := false
	for _, w := range writable {
		if w == dir {
			return out
		}
		split = split || pathWithin(w, dir)
	}
	if !split {
		return append(out, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		// A symlink is skipped, not followed: its target is not this
		// directory's to protect.
		if e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			out = protectAround(out, filepath.Join(dir, e.Name()), writable)
		}
	}
	return out
}

// realDir resolves path to its absolute, symlink-free form. A path that does
// not exist resolves to "", unless keepMissing, which keeps its cleaned
// absolute form (a writable root that is created later still exempts its
// would-be parents).
func realDir(path string, keepMissing bool) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	if keepMissing {
		return abs
	}
	return ""
}

func pathWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// acpControlPlanePolicy is the resolved policy an ACP launch with protected
// paths runs under; go-agent-wrapper merges the paths into it. It grants the
// whole host filesystem writable, the full network and subprocesses, which
// is what the native minimal profile gives, so the protection is its only
// effect. go-agent-wrapper has no protect-only ACP sandbox of its own until
// CW-20261001-0162.
func acpControlPlanePolicy(workdir string) (*sandbox.ResolvedAccessPolicy, error) {
	policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{
		ID:      "nanite-acp-control-plane",
		Mode:    sandbox.ConfinementRequired,
		Roots:   sandbox.Roots{Project: workdir, CWD: workdir},
		FS:      sandbox.FilesystemAccess{Write: []sandbox.PathRef{{Path: string(filepath.Separator)}}},
		Network: sandbox.NetworkAccess{Mode: sandbox.NetworkFull},
	})
	if err != nil {
		return nil, err
	}
	return &policy, nil
}
