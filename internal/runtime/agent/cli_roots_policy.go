package agent

import (
	"log/slog"
	"os"
	"path/filepath"
)

// cli_roots_policy.go: the hard deny-list for the directories Nanite offers a
// CLI agent as writable (claude additionalDirectories and --add-dir, codex
// writable_roots, agy read_file/write_file rules).
//
// effectiveCLIWritableRoots applies it to every source: the session work
// root, the host's `directories`, `cli_writable_roots` and, behind the
// deprecated NANITE_CLI_ROOTS_FROM_DEV_TOOLS escape hatch, the dev tools list.
// Naming a denied directory in configuration does not make it writable.
//
// This bounds what Nanite grants the CLIs. It is not OS-level isolation: a
// CLI runs as the same unix user as the service and can read, and often
// write, what that user can (SECURITY.md, "CLI writable roots").

// CLIRootsFromDevToolsEnv is the deprecated escape hatch that makes CLI
// launches inherit dev_tools_allowed_paths again, as they did before
// cli_writable_roots existed. Default off; for one release only.
const CLIRootsFromDevToolsEnv = "NANITE_CLI_ROOTS_FROM_DEV_TOOLS"

// CLIRootsFromDevToolsEnabled reports whether the escape hatch is on.
func CLIRootsFromDevToolsEnabled() bool {
	return os.Getenv(CLIRootsFromDevToolsEnv) == "1"
}

// CLIRootDenyHomeDirs is the single deny-list of directories directly under
// the real home that no CLI root may equal, sit inside, or contain. Credential
// stores and the control plane of Nanite, Tether, Cerberus and the CLIs.
// Besides these, the real home itself, every parent of it and `/` are denied.
var CLIRootDenyHomeDirs = []string{
	".tether", ".cerberus", ".nanite",
	".ssh", ".gnupg", ".aws", ".kube", ".docker",
	".gemini", ".agents", ".config",
}

// CLIRootDenyReason returns why root may not be a CLI writable root, or ""
// when it may. Matching is on real paths: a symlink to a denied directory is
// denied, and so is a root that does not exist yet but would be created inside
// one. home is the real home directory; with no usable home only `/` is denied.
func CLIRootDenyReason(root, home string) string {
	if root == "" || !filepath.IsAbs(root) {
		return "not an absolute path"
	}
	cands := []string{filepath.Clean(root)}
	if r := resolveLoose(root); r != "" && r != cands[0] {
		cands = append(cands, r)
	}
	for _, c := range cands {
		if c == string(filepath.Separator) {
			return "is the filesystem root"
		}
	}
	if home == "" || !filepath.IsAbs(home) {
		return ""
	}
	homes := []string{filepath.Clean(home)}
	if h := resolveLoose(home); h != "" && h != homes[0] {
		homes = append(homes, h)
	}
	for _, c := range cands {
		for _, h := range homes {
			if pathWithin(h, c) {
				return "is, or contains, the home directory"
			}
			for _, name := range CLIRootDenyHomeDirs {
				d := filepath.Join(h, name)
				dirs := []string{d}
				if rd := resolveLoose(d); rd != "" && rd != d {
					dirs = append(dirs, rd)
				}
				for _, dd := range dirs {
					if pathWithin(c, dd) || pathWithin(dd, c) {
						return "overlaps denied directory ~/" + name
					}
				}
			}
		}
	}
	return ""
}

// filterDeniedCLIRoots drops every denied root, logging each at debug with its
// reason. Order is kept.
func filterDeniedCLIRoots(roots []string, home string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if reason := CLIRootDenyReason(r, home); reason != "" {
			slog.Debug("agent: dropping denied CLI writable root", "root", r, "reason", reason)
			continue
		}
		out = append(out, r)
	}
	return out
}
