package agent

import (
	"os"
	"path/filepath"

	"github.com/hollis-labs/go-sandbox/sandbox"
)

// buildSandboxProfile composes the sandbox.Profile applied to the spawned
// child. Layered on top of deps.SandboxBaseProfile so callers can inject
// portfolio-wide defaults via the composition root.
//
// Per-spawn additions:
//   - AllowLoopback=true (required for the planted .mcp.json subprocess
//     descriptor's nanite-internal RPC; benign because Net stays gated).
//   - FS write allowlist: opts.Workdir (project work), ws.Root (workspace),
//     bootDir (ephemeral boot dir).
//   - LoopbackForwardPorts left empty by default; production wiring sets it
//     when running under linux bwrap with namespace-local lo bridging.
//
// ModeBackground+WideOpen short-circuits to the zero-value Profile (no
// enforcement), preserving the legacy privileged primitive.
func buildSandboxProfile(base sandbox.Profile, opts Options, workspaceDir, bootDir string) sandbox.Profile {
	if opts.Mode == ModeBackground && opts.WideOpen {
		return sandbox.Profile{}
	}

	p := base
	p.AllowLoopback = true

	// Append paths to the write allowlist. Avoid duplicates by tracking
	// already-present entries.
	seen := make(map[string]bool, len(p.FS.Write))
	for _, w := range p.FS.Write {
		seen[w] = true
	}
	for _, path := range []string{opts.Workdir, workspaceDir, bootDir, naniteHomeDir()} {
		if path == "" || seen[path] {
			continue
		}
		p.FS.Write = append(p.FS.Write, path)
		seen[path] = true
	}

	return p
}

// naniteHomeDir resolves $HOME/.nanite for the FS allowlist. Empty when
// HOME is unset (rare; matches the lib's "empty path = skip" convention).
func naniteHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nanite")
}
