package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace describes the durable session root planted at
// <WorkspacesRoot>/<sessionID>/ alongside the ephemeral boot dir.
//
// Layout:
//
//	<Root>/
//	├── prompts/    — boot.md and any planted kickoff payloads
//	├── state/      — plan.json, checkpoints/
//	└── logs/       — session.log (runtime transcript), stderr.<runID>.log
//
// The boot dir lives in $TMPDIR and is cleaned on session done; the
// workspace dir survives for forensic value and ModeResume.
type Workspace struct {
	Root       string
	PromptsDir string
	StateDir   string
	LogDir     string
	// LogPath is the convenience pointer at <LogDir>/session.log, the
	// destination passed into agentsessions.StartOptions.LogPath when the
	// caller has no other log destination.
	LogPath string
}

// workspaceCreate materializes the workspace root and its three
// subdirectories (prompts/, state/, logs/). Idempotent: re-creating an
// existing workspace returns the existing paths without error so resume
// flows can be reentrant.
func workspaceCreate(root, sessionID string, opts Options) (*Workspace, error) {
	if root == "" {
		return nil, errors.New("agent.workspaceCreate: WorkspacesRoot is required")
	}
	if sessionID == "" {
		return nil, errors.New("agent.workspaceCreate: sessionID is required")
	}
	_ = opts // reserved for future per-Mode workspace adjustments

	wsRoot := filepath.Join(root, sessionID)
	prompts := filepath.Join(wsRoot, "prompts")
	state := filepath.Join(wsRoot, "state")
	logs := filepath.Join(wsRoot, "logs")

	for _, dir := range []string{wsRoot, prompts, state, logs} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("agent.workspaceCreate: mkdir %s: %w", dir, err)
		}
	}

	return &Workspace{
		Root:       wsRoot,
		PromptsDir: prompts,
		StateDir:   state,
		LogDir:     logs,
		LogPath:    filepath.Join(logs, "session.log"),
	}, nil
}

// EnsureSessionWorkspace creates (or finds) the durable session workspace
// <root>/<sessionID>/{prompts,state,logs} for a session that is not booted
// through Boot, so a native-loop (API) session has the same workspace a CLI
// session gets. Idempotent.
func EnsureSessionWorkspace(root, sessionID string) (*Workspace, error) {
	return workspaceCreate(root, sessionID, Options{})
}

// SessionToolRoots returns the directories an API-backed session's file and
// shell tools may use, work root first: the host's directories in order, else
// the session workspace (a CLI session's work root defaults to its workspace
// the same way), then the workspace itself. A root equal to or inside a
// directory Nanite protects is dropped. A root that contains one is kept
// whole, as a CLI launch's is, so the dev tools must refuse the protected
// directory themselves: see SessionToolProtection. hostDirs may contain "~".
func SessionToolRoots(deps *Dependencies, ws *Workspace, hostDirs []string) []string {
	var roots []string
	for _, dir := range hostDirs {
		expanded, err := ExpandUserHome(dir)
		if err != nil || !filepath.IsAbs(expanded) {
			continue
		}
		roots = append(roots, filepath.Clean(expanded))
	}
	if ws != nil {
		roots = append(roots, ws.Root)
	}
	if len(roots) == 0 {
		return nil
	}
	if deps != nil {
		// The session workspace is launch-owned, as a CLI launch's work root is:
		// it lives under the control plane's workspaces directory by design.
		owned := []string{naniteHomeDir()}
		if ws != nil {
			owned = append(owned, ws.Root)
		}
		roots = rootsOutsideProtected(roots, deps.ControlPlane.protectedFor(owned...), false)
	}
	return roots
}

// SessionToolProtection returns the directories the dev tools must refuse for
// an API-backed session whatever grant reaches them (the control plane,
// resolved), and, when a root the host chose contains or is one of them, why
// dev_bash is withheld: a command cannot be path-filtered, so such a session
// gets no shell. A root wholly outside every protected directory leaves
// dev_bash on.
func SessionToolProtection(deps *Dependencies, ws *Workspace, hostDirs []string) (protected []string, shellRefusal string) {
	if deps == nil {
		return nil, ""
	}
	owned := []string{naniteHomeDir()}
	if ws != nil {
		owned = append(owned, ws.Root)
	}
	protected = deps.ControlPlane.protectedFor(owned...)
	for _, dir := range hostDirs {
		expanded, err := ExpandUserHome(dir)
		if err != nil || !filepath.IsAbs(expanded) {
			continue
		}
		resolved := resolveLoose(expanded)
		for _, p := range protected {
			if resolved != "" && pathWithin(p, resolved) {
				return protected, "not available: the host directory " + expanded +
					" contains the protected directory " + p + ", and a shell command cannot be limited to the rest"
			}
		}
	}
	return protected, ""
}
