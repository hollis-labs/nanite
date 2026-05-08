package agent

import "errors"

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
	Root    string
	LogPath string
	LogDir  string
}

// workspaceCreate materializes the workspace root. Phase 3 fills in
// the directory creation, permissions, and orphan-detection wiring.
func workspaceCreate(root, sessionID string, opts Options) (*Workspace, error) {
	_ = root
	_ = sessionID
	_ = opts
	return nil, errors.New("agent.workspaceCreate: not yet implemented (skeleton — phase 2)")
}
