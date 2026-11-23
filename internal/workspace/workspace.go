// Package workspace is how Nanite finds its configuration when it is run in a directory that is
// not an agent directory, the way claude and codex do: a global home (NANITE_HOME, default
// ~/.nanite) that is itself the agent directory, per-session state under it, instructions from
// AGENTS.md files (CLAUDE.md where a directory has none) walking up from the workspace, and
// project settings (.nanite/settings.json, .mcp.json, .nanite/skills) that override the home's,
// the closest directory winning.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxFile bounds an instructions or settings file, like the authored files (1 MiB).
const maxFile = 1 << 20

// Home is the global Nanite directory: $NANITE_HOME, else ~/.nanite.
func Home() (string, error) {
	if h := strings.TrimSpace(os.Getenv("NANITE_HOME")); h != "" {
		return filepath.Abs(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nanite"), nil
}

// IsAgentDir reports whether dir holds an agent directory's two required files.
func IsAgentDir(dir string) bool {
	return regular(filepath.Join(dir, "agent.md")) && regular(filepath.Join(dir, "host.yaml"))
}

func regular(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// EnsureHome makes home a usable agent directory, writing the files it lacks: host.yaml, and
// agent.md (Nanite's built-in base; the person's own instructions go in AGENTS.md). It returns
// the files it wrote.
func EnsureHome(home, agentMD, hostYAML string) ([]string, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	var wrote []string
	for _, f := range []struct{ name, body string }{{"agent.md", agentMD}, {"host.yaml", hostYAML}} {
		path := filepath.Join(home, f.name)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return wrote, err
		}
		if err := os.WriteFile(path, []byte(f.body), 0o600); err != nil {
			return wrote, err
		}
		wrote = append(wrote, path)
	}
	return wrote, nil
}

// projectKey names a workspace's directory under home/projects: its absolute path with every
// character other than a letter, digit, dot or underscore turned into a dash, as claude does.
func projectKey(workspace string) string {
	var b strings.Builder
	for _, r := range filepath.Clean(workspace) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ProjectDir is where a workspace's sessions are kept.
func ProjectDir(home, workspace string) string {
	return filepath.Join(home, "projects", projectKey(workspace))
}

// SessionDir is one session's state directory (its store, traces and lock).
func SessionDir(home, workspace, session string) (string, error) {
	if session == "" || strings.ContainsAny(session, `/\`) || session == "." || session == ".." {
		return "", fmt.Errorf("session id %q cannot name a directory", session)
	}
	return filepath.Join(ProjectDir(home, workspace), session), nil
}

// LatestSession is the workspace's most recently used session ("" when there is none): the
// session directory whose store changed last.
func LatestSession(home, workspace string) (string, error) {
	entries, err := os.ReadDir(ProjectDir(home, workspace))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	best, bestTime := "", int64(0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(ProjectDir(home, workspace), e.Name())
		t := int64(0)
		for _, name := range []string{"nanite.db-wal", "nanite.db"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.ModTime().UnixNano() > t {
				t = info.ModTime().UnixNano()
			}
		}
		if t > bestTime || (t == bestTime && e.Name() > best) {
			best, bestTime = e.Name(), t
		}
	}
	return best, nil
}

// Ancestors are the directories from the outermost one the walk looks at down to workspace: up
// to but not including $HOME when workspace is under it, else up to the filesystem root.
func Ancestors(workspace, userHome string) []string {
	workspace = filepath.Clean(workspace)
	userHome = filepath.Clean(userHome)
	var dirs []string
	for dir := workspace; ; dir = filepath.Dir(dir) {
		atHome := userHome != "" && dir == userHome
		if atHome && dir != workspace {
			break
		}
		dirs = append(dirs, dir)
		if parent := filepath.Dir(dir); parent == dir || atHome {
			break
		}
	}
	slices.Reverse(dirs)
	return dirs
}

// Layer is one instructions file.
type Layer struct {
	Path string
	Text string
}

// Instructions are the layers after the agent's own base, in order: home/AGENTS.md, then for
// each ancestor of the workspace, outermost first, its AGENTS.md, else its CLAUDE.md.
func Instructions(home, workspace, userHome string) ([]Layer, error) {
	var out []Layer
	add := func(path string) (bool, error) {
		text, ok, err := readSmall(path)
		if ok && strings.TrimSpace(text) != "" {
			out = append(out, Layer{Path: path, Text: text})
		}
		return ok, err
	}
	if _, err := add(filepath.Join(home, "AGENTS.md")); err != nil {
		return nil, err
	}
	for _, dir := range Ancestors(workspace, userHome) {
		ok, err := add(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			return nil, err
		}
		if !ok {
			if _, err := add(filepath.Join(dir, "CLAUDE.md")); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Project are the configuration files that apply to a workspace, least specific first, so a
// caller merging them in order lets the closest win: the home's settings.json, then each
// ancestor's .nanite/settings.json; each ancestor's .mcp.json (the home's own .mcp.json is the
// agent directory's and already loaded); each ancestor's .nanite/skills.
type Project struct {
	Settings  []string
	MCP       []string
	SkillDirs []string
}

// FindProject lists the configuration files present for workspace.
func FindProject(home, workspace, userHome string) Project {
	var p Project
	if regular(filepath.Join(home, "settings.json")) {
		p.Settings = append(p.Settings, filepath.Join(home, "settings.json"))
	}
	for _, dir := range Ancestors(workspace, userHome) {
		if filepath.Clean(dir) == filepath.Clean(home) {
			continue
		}
		if f := filepath.Join(dir, ".nanite", "settings.json"); regular(f) {
			p.Settings = append(p.Settings, f)
		}
		if f := filepath.Join(dir, ".mcp.json"); regular(f) {
			p.MCP = append(p.MCP, f)
		}
		if d := filepath.Join(dir, ".nanite", "skills"); isDir(d) {
			p.SkillDirs = append(p.SkillDirs, d)
		}
	}
	return p
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// readSmall reads a regular file of at most maxFile bytes; a missing file is not an error.
func readSmall(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, nil // a link or a directory is not read
	}
	if info.Size() > maxFile {
		return "", false, fmt.Errorf("%s is larger than 1 MiB", path)
	}
	b, err := os.ReadFile(path) // #nosec G304 -- an instructions file found by the documented walk
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}
