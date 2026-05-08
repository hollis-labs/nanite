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
//	└── logs/       — session.log (PTY transcript), stderr.<runID>.log
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
