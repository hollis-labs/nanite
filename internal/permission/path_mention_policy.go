package permission

// path_mention_policy.go bounds which free-text path mentions may become
// session path grants (CW-20261001-0232).
//
// A mention is any "~/", "/" or "./" token in a turn's text. The text can come
// from a program as easily as from a person: the harness and message APIs sit
// on an unauthenticated loopback, so any local client can supply a "chat"
// turn. A grant is therefore refused when it would cover somewhere a mention
// must never reach, whoever typed it:
//
//   - a sensitive path: credentials, shell startup files, the systemd user
//     manager, the CLI agents' own state, ~/.local/bin, and the state of
//     Nanite and its sibling apps;
//   - an ancestor of one. The grant rule (Q2 of CW-20260430-0009) also covers
//     the mentioned path's parent directory, so a mention of ~/x.txt would
//     otherwise grant $HOME, and a mention of /tmp would grant "/";
//   - when the policy confines (production does), anything outside $HOME and
//     the configured allowed bases.
//
// Paths are compared by real path, with symlinks resolved, and by their
// literal spelling, so neither a symlink into a sensitive directory nor a
// symlinked sensitive directory gets past it.

import (
	"os"
	"path/filepath"
	"strings"
)

// MentionPolicy configures which mentioned paths may be granted. The zero
// value applies only the built-in sensitive-path denylist.
type MentionPolicy struct {
	// Confine refuses any mention whose real path is outside Home and every
	// entry of Bases.
	Confine bool
	// Home is the user's home directory. Empty means HomeDir().
	Home string
	// Bases are the configured allowed directories beyond Home
	// (dev_tools_allowed_paths).
	Bases []string
	// Denied are further directories never granted: the running Nanite's own
	// state, beyond the built-in app directories.
	Denied []string
}

// SetMentionPolicy installs the policy RegisterFromUserMessage applies.
// Nil-safe. The built-in denylist applies whether or not a policy is set.
func (g *PathGrants) SetMentionPolicy(p MentionPolicy) {
	if g == nil {
		return
	}
	cp := MentionPolicy{
		Confine: p.Confine,
		Home:    p.Home,
		Bases:   append([]string(nil), p.Bases...),
		Denied:  append([]string(nil), p.Denied...),
	}
	g.mu.Lock()
	g.mention = &cp
	g.mu.Unlock()
}

// Why a mentioned path was refused.
const (
	// RefusedSensitive: the path is, or is inside, a sensitive path.
	RefusedSensitive = "sensitive"
	// RefusedCoversSensitive: the path is an ancestor of a sensitive path, so
	// granting it would grant the sensitive path too.
	RefusedCoversSensitive = "covers_sensitive"
	// RefusedOutsideAllowed: the policy confines, and the path is outside
	// $HOME and the allowed bases.
	RefusedOutsideAllowed = "outside_allowed"
)

// sensitiveHomeDirs are the home-relative paths a mention may never grant,
// or grant an ancestor of.
var sensitiveHomeDirs = []string{
	".ssh", ".gnupg", ".codex", ".aws", ".kube", ".docker", ".netrc",
	".bashrc", ".bash_profile", ".bash_login", ".profile",
	".zshrc", ".zprofile", ".zshenv",
	filepath.Join(".local", "bin"),
}

// sensitiveHomePrefixes are first path segments under home, matched by
// prefix: ~/.claude, ~/.claude.json, ~/.claude-work, and so on.
var sensitiveHomePrefixes = []string{".claude"}

// sensitiveApps are the applications whose state directories a mention may
// not grant, under the config, data and state roots: Nanite and the sibling
// apps whose databases and coordination stores it must not be steered into
// writing. This only limits text-derived grants. A directory an operator
// lists in dev_tools_allowed_paths is unaffected.
var sensitiveApps = []string{"nanite", "torque", "tether", "tesseract", "hadron", "tangent", "fragments-engine"}

// xdgRoots returns the default root under home and, when set to an absolute
// path, the one the environment relocates it to.
func xdgRoots(home, env string, fallback ...string) []string {
	roots := []string{filepath.Join(append([]string{home}, fallback...)...)}
	if v := os.Getenv(env); filepath.IsAbs(v) && filepath.Clean(v) != roots[0] {
		roots = append(roots, filepath.Clean(v))
	}
	return roots
}

// sensitiveDirs lists the directories a mention may never grant, for home
// (empty: none) plus extra.
func sensitiveDirs(home string, extra []string) []string {
	var dirs []string
	if home != "" {
		for _, rel := range sensitiveHomeDirs {
			dirs = append(dirs, filepath.Join(home, rel))
		}
		configRoots := xdgRoots(home, "XDG_CONFIG_HOME", ".config")
		dataRoots := xdgRoots(home, "XDG_DATA_HOME", ".local", "share")
		stateRoots := xdgRoots(home, "XDG_STATE_HOME", ".local", "state")
		for _, root := range configRoots {
			// The systemd user manager runs a unit for the user, outside any sandbox.
			dirs = append(dirs, filepath.Join(root, "systemd"), filepath.Join(root, "autostart"))
		}
		for _, root := range append(append(append([]string{}, configRoots...), dataRoots...), stateRoots...) {
			for _, app := range sensitiveApps {
				dirs = append(dirs, filepath.Join(root, app))
			}
		}
	}
	for _, d := range extra {
		if d != "" {
			dirs = append(dirs, filepath.Clean(d))
		}
	}
	return dirs
}

// realPath resolves symlinks in the longest existing prefix of p and appends
// the rest, so a path that does not exist yet is still compared by where it
// would land.
func realPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, ensureTrailingSep(dir))
}

// forms returns p as spelled and, when different, by real path.
func forms(p string) []string {
	clean := filepath.Clean(p)
	resolved := realPath(clean)
	if resolved == clean {
		return []string{clean}
	}
	return []string{clean, resolved}
}

// mentionRefusal says why abs, an absolute cleaned path, may not become a
// grant: one of the Refused* reasons, or "" when it may.
func (g *PathGrants) mentionRefusal(abs string) string {
	g.mu.RLock()
	pol := g.mention
	g.mu.RUnlock()

	home := ""
	if pol != nil {
		home = pol.Home
	}
	if home == "" {
		if h, err := HomeDir(); err == nil {
			home = h
		}
	}
	var extra []string
	if pol != nil {
		extra = pol.Denied
	}

	cands := forms(abs)
	denied := sensitiveDirs(home, extra)
	for _, d := range denied {
		for _, dform := range forms(d) {
			for _, c := range cands {
				if within(c, dform) {
					return RefusedSensitive
				}
				if within(dform, c) {
					return RefusedCoversSensitive
				}
			}
		}
	}
	if home != "" {
		for _, hform := range forms(home) {
			for _, c := range cands {
				if !within(c, hform) {
					continue
				}
				rel := strings.TrimPrefix(c, ensureTrailingSep(hform))
				first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
				for _, prefix := range sensitiveHomePrefixes {
					if strings.HasPrefix(first, prefix) {
						return RefusedSensitive
					}
				}
			}
		}
	}

	if pol != nil && pol.Confine {
		bases := append([]string(nil), pol.Bases...)
		if home != "" {
			bases = append(bases, home)
		}
		inside := false
		resolved := cands[len(cands)-1] // the real path when it differs, else the spelling
		for _, base := range bases {
			if base == "" {
				continue
			}
			for _, bform := range forms(base) {
				if within(resolved, bform) {
					inside = true
				}
			}
		}
		if !inside {
			return RefusedOutsideAllowed
		}
	}
	return ""
}
