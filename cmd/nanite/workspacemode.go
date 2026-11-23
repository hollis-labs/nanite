package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workspace"
)

// Workspace mode: `nanite` run in a directory that is not an agent directory, without -C, works
// the way claude and codex do (internal/workspace, docs/workspace.md). The global home
// (NANITE_HOME, default ~/.nanite) is the agent directory; the current directory is the
// workspace; each session keeps its state under home/projects/<workspace>/<session id>/.

// homeAgent is the home's agent.md when it has none: Nanite's built-in base. The person's own
// standing instructions go in home/AGENTS.md, layered after it.
const homeAgent = `---
schema_version: "2"
definition_id: def:nanite
revision: "1"
name: nanite
description: Nanite's default agent, for work in any directory.
behavior:
  purpose: Help the person with the work in the current directory, from questions and writing to changes to its files.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: durable
---
You are Nanite, a coding and general-purpose agent running in the person's current directory
(the workspace).

Help with whatever the person asks: answering questions, explaining, writing, planning, and
working on the files and code in the workspace. Read a file before describing it and run a
command before reporting what it does. Follow the instructions in the AGENTS.md (or CLAUDE.md)
files you are given; the more specific directory's instructions win.

Be specific: name the file, the line, the command. Prefer small changes the person can review
over sweeping ones.
`

// homeHost is the home's host.yaml when it has none.
const homeHost = `kind: stable
lifetime: on-demand
seat_id: nanite
runtime:
  provider: anthropic
permissions:
  mode: default
`

// wsMode is a workspace-mode run.
type wsMode struct {
	home, workspace, userHome string
	session                   string // the conversation id; also the state directory's name
	dataDir                   string
	project                   workspace.Project
	instructions              []workspace.Layer
}

// detectWorkspace decides the mode before the app starts (it needs the state directory). It
// returns nil for an agent-directory run: -C given, or the directory is one.
func detectWorkspace(dirGiven bool, dir string, args []string, stderr io.Writer) (*wsMode, error) {
	if dirGiven || workspace.IsAgentDir(dir) {
		return nil, nil
	}
	home, err := workspace.Home()
	if err != nil {
		return nil, fmt.Errorf("nanite home: %w", err)
	}
	wrote, err := workspace.EnsureHome(home, homeAgent, homeHost)
	if err != nil {
		return nil, fmt.Errorf("nanite home %s: %w", home, err)
	}
	if len(wrote) > 0 {
		_, _ = fmt.Fprintf(stderr, "nanite: set up %s (%s); put your own instructions in %s\n", home,
			strings.Join(baseNames(wrote), ", "), filepath.Join(home, "AGENTS.md"))
	}
	userHome, _ := os.UserHomeDir()
	w := &wsMode{home: home, workspace: dir, userHome: userHome}
	switch {
	case flagValue(args, "--session-id") != "":
		w.session = flagValue(args, "--session-id")
	case flagValue(args, "--resume") != "" || flagValue(args, "--discard") != "":
		w.session = firstNonEmpty(flagValue(args, "--resume"), flagValue(args, "--discard"))
		d, err := workspace.SessionDir(home, dir, w.session)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(d); err != nil { // #nosec G703 -- SessionDir refuses ids with separators or dot names
			return nil, fmt.Errorf("no session %s in this workspace (%s)", w.session, dir)
		}
	case hasFlag(args, "--continue"):
		if w.session, err = workspace.LatestSession(home, dir); err != nil {
			return nil, err
		}
	}
	if w.session == "" {
		w.session = store.NewID(time.Now().UTC())
	}
	if w.dataDir, err = workspace.SessionDir(home, dir, w.session); err != nil {
		return nil, err
	}
	if w.instructions, err = workspace.Instructions(home, dir, userHome); err != nil {
		return nil, err
	}
	w.project = workspace.FindProject(home, dir, userHome)
	return w, nil
}

// options sets a workspace run's agent options, before the claude flags' (which come last).
func (w *wsMode) options(o *agent.Options) {
	o.DataDir, o.WorkDir, o.HideDir = w.dataDir, w.workspace, true
	var layered []string
	for _, l := range w.instructions {
		layered = append(layered, l.Text)
	}
	o.Instructions = append(layered, o.Instructions...)
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// flagValue is the value of --name in args ("--name v" or "--name=v"), "" when absent.
func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == "--" {
			return ""
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == name || a == name+"=true" {
			return true
		}
	}
	return false
}
