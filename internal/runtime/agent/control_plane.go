package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
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
// acpControlPlanePolicy. Native codex is the exception: it runs under its
// own sandbox, whose writable_roots are narrowed around these directories
// (codexSandboxesItself, rootsOutsideProtected).
//
// The boundary is go-sandbox's: direct writes into a protected directory
// fail, from the agent and from every process it starts inside the
// sandbox. Writes the agent delegates to a same-uid process outside it
// (`systemd-run --user`, a terminal multiplexer, a host app's API) are not
// stopped, and neither is persistence planted elsewhere under $HOME that
// later runs outside the sandbox (a shell rc file, a git hook).

// ProtectEnv is the operator kill switch, the same shape as Torque's
// TORQUE_SANDBOX_PROTECT and Tether's TETHER_SANDBOX_PROTECT. Protection is
// on by default; "0", "false", "off" or "no" turns it off, so an operator
// can launch agents on a host whose sandbox backend misbehaves without
// rolling Nanite back. cmd/nanite warns loudly at startup and /api/health
// reports a warning while it is off.
const ProtectEnv = "NANITE_SANDBOX_PROTECT"

// ProtectionEnabled reports whether ProtectEnv leaves protection on.
func ProtectionEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ProtectEnv))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// ControlPlane names the directories Nanite's agents must never write.
type ControlPlane struct {
	// Dirs are Nanite's control-plane directories: config, state and data.
	// Missing entries are skipped; protection covers what exists at launch.
	Dirs []string
	// Writable are directories inside Dirs that agents must still write:
	// the worktree root agents work in. The main database's directory is not
	// one: no agent process opens the database any more (CW-20261001-0188).
	Writable []string
}

// protectedFor returns the real-path directories to protect for one launch.
// launchOwned are the launch's own roots, which Nanite chose (work dir,
// workspace, boot dir, ~/.nanite). Never pass a root a user or a config file
// offered, such as a path grant or dev_tools_allowed_paths: an exempt root
// is not protected, so one that names the control plane would un-protect it
// (rootsOutsideProtected drops those roots instead).
//
// A Dirs entry that contains a writable root (Writable, or one of the
// launch's own) is not protected whole: its
// child directories are, except the ones leading to that root, recursively.
// Files directly inside a split directory therefore stay writable;
// go-sandbox protects directories only, because a file's own directory
// stays writable and an atomic save would replace it.
func (c ControlPlane) protectedFor(launchOwned ...string) []string {
	var writable []string
	for _, w := range append(slices.Clone(c.Writable), launchOwned...) {
		if w = realDir(w, true); w != "" {
			writable = append(writable, w)
		}
	}
	var out []string
	for _, d := range c.Dirs {
		if d = realDir(d, false); d != "" {
			out = splitAround(out, d, writable)
		}
	}
	return outermost(out)
}

// rootsOutsideProtected keeps a launch's writable roots (the work root,
// dev_tools_allowed_paths, the session's path grants) from offering the
// control plane to the agent (CW-20261001-0143). A path grant is whatever a
// user's message names, and its parent directory too, so a message that
// mentions <state>/coordination/x grants <state>/coordination itself. Each
// root is judged by its real path, resolved through its nearest existing
// ancestor when it does not exist yet:
//   - equal to or inside a protected directory: dropped;
//   - containing one: replaced by its other child directories, recursively,
//     when split (Codex, whose own sandbox enforces its roots), else kept
//     whole (Claude's additionalDirectories, which its bwrap protection
//     already backs);
//   - anything else: kept as given, in order.
//
// protected must not have been computed with these roots as exemptions, or a
// root would un-protect itself. With nothing protected the roots are
// returned unchanged. When split, files directly in a narrowed root are no
// longer writable to the agent.
func rootsOutsideProtected(roots, protected []string, split bool) []string {
	if len(protected) == 0 {
		return roots
	}
	var out []string
	for _, r := range roots {
		resolved := resolveLoose(r)
		if resolved == "" {
			continue
		}
		switch {
		case slices.ContainsFunc(protected, func(p string) bool { return pathWithin(resolved, p) }):
			// equal to or inside a protected directory
		case split && slices.ContainsFunc(protected, func(p string) bool { return pathWithin(p, resolved) }):
			out = append(out, splitAround(nil, resolved, protected)...)
		default:
			out = append(out, r)
		}
	}
	return slices.Compact(out)
}

// resolveLoose is the real path of path, with symlinks resolved through its
// nearest existing ancestor: a root that does not exist yet is judged where
// it would be created. "" when path cannot be made absolute.
func resolveLoose(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	rest := ""
	for cur := abs; ; {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// splitAround appends dir to out unless an avoid path is dir itself (then
// nothing) or lies inside it (then dir's child directories, recursively,
// each judged the same way).
func splitAround(out []string, dir string, avoid []string) []string {
	split := false
	for _, a := range avoid {
		if a == dir {
			return out
		}
		split = split || pathWithin(a, dir)
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
		// directory's to judge.
		if e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			out = splitAround(out, filepath.Join(dir, e.Name()), avoid)
		}
	}
	return out
}

// outermost sorts dirs and drops duplicates and any entry nested inside
// another. Sorted, a directory precedes everything inside it.
func outermost(dirs []string) []string {
	slices.Sort(dirs)
	var kept []string
	for _, p := range dirs {
		if !slices.ContainsFunc(kept, func(k string) bool { return pathWithin(p, k) }) {
			kept = append(kept, p)
		}
	}
	return kept
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
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
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

// codexSandboxesItself reports whether a launch is native codex under its
// own sandbox, which confines its commands' writes to its cwd, $TMPDIR,
// /tmp and its writable_roots, narrowed around the control plane
// (composeBootdirParams). Nanite does not wrap such a launch: codex's
// sandbox is a bwrap of its own, and inside Nanite's it cannot get the
// capabilities it needs (Ubuntu's AppArmor unpriv_bwrap profile ends in
// `audit deny capability`), so every command it runs would fail. A codex
// with danger-full-access, or over ACP, keeps Nanite's sandbox.
func codexSandboxesItself(sel RuntimeSelection) bool {
	return sel.Runtime == runtimes.Codex && !sel.ACP() &&
		(codexSandboxMode == "workspace-write" || codexSandboxMode == "read-only")
}
