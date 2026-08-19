package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/store"
)

var baseDirName = "." + brand.ID + "/sandboxes"

const sandboxSubDir = ".sandbox"

// sessionIDRe is the character class allowed for session IDs when they are
// used as filesystem path components under the sandbox base dir. Matches
// UUIDs, slugs, and short test names; excludes every byte that has meaning
// to path resolution (`/`, `.`) or sandbox-exec profile tokenizing
// (quotes, parens, semicolons, whitespace, control chars).
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Dir returns the sandbox directory path for a session, creating it and
// the .sandbox/ subdirectory if needed. The session ID is validated against
// a strict character class and the resolved path is verified to stay under
// the sandbox base dir via pathsafe.ResolveUnder. An attempt to escape the
// base dir returns a *pathsafe.EscapeError.
func Dir(sessionID string) (string, error) {
	if !sessionIDRe.MatchString(sessionID) {
		return "", fmt.Errorf("sandbox: invalid session id (want %s)", sessionIDRe.String())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve home dir: %w", err)
	}
	base := filepath.Join(home, baseDirName)
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", fmt.Errorf("sandbox: create base dir: %w", err)
	}
	// Defense-in-depth: re-validate through pathsafe so a symlinked base dir
	// or a future caller passing a novel session id shape still cannot
	// escape the sandbox root.
	dir, err := pathsafe.ResolveUnder(base, sessionID)
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve session dir: %w", err)
	}
	subDir := filepath.Join(dir, sandboxSubDir)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return "", fmt.Errorf("sandbox: create dir: %w", err)
	}
	return dir, nil
}

// PopulateOpts contains optional parameters for sandbox population.
type PopulateOpts struct {
	SessionID string                    // Nanite session ID (for MCP server args)
	DBPath    string                    // Absolute path to Nanite's SQLite database
	Adapters  *agentpkg.AdapterRegistry // Adapter registry for delegated sandbox writing
}

// Populate creates the sandbox subdirectory and delegates content generation
// to registered adapters. If no adapter registry is provided, it is a no-op
// beyond directory creation.
//
// Phase 0 item 21 ("Cut Modes, in full") removed the mode *store.AgentMode
// parameter this used to take — it was already unused in the function body
// (Legacy Agent Mode never fed sandbox population) and store.AgentMode no
// longer exists.
func Populate(dir string, agent *store.AgentProfile, opts PopulateOpts) error {
	subDir := filepath.Join(dir, sandboxSubDir)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return fmt.Errorf("sandbox: create subdir: %w", err)
	}
	if opts.Adapters != nil {
		ctx := agentpkg.SandboxContext{
			SessionID:  opts.SessionID,
			WorkingDir: dir,
			DBPath:     opts.DBPath,
		}
		return opts.Adapters.PopulateAllSandboxes(dir, *agent, ctx)
	}
	return nil
}
