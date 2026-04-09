package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/store"
)

var baseDirName = "." + brand.ID + "/sandboxes"
const sandboxSubDir = ".sandbox"

// Dir returns the sandbox directory path for a session, creating it and
// the .sandbox/ subdirectory if needed.
func Dir(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve home dir: %w", err)
	}
	dir := filepath.Join(home, baseDirName, sessionID)
	subDir := filepath.Join(dir, sandboxSubDir)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return "", fmt.Errorf("sandbox: create dir: %w", err)
	}
	return dir, nil
}

// PopulateOpts contains optional parameters for sandbox population.
type PopulateOpts struct {
	SessionID string                  // Nanite session ID (for MCP server args)
	DBPath    string                  // Absolute path to Nanite's SQLite database
	Adapters  *agentpkg.AdapterRegistry // Adapter registry for delegated sandbox writing
}

// Populate creates the sandbox subdirectory and delegates content generation
// to registered adapters. If no adapter registry is provided, it is a no-op
// beyond directory creation.
func Populate(dir string, agent *store.AgentProfile, mode *store.AgentMode, opts PopulateOpts) error {
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

// writeFile is a convenience helper for writing a single file into a directory.
func writeFile(dir, name, content string) error {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("sandbox: write %s: %w", name, err)
	}
	return nil
}
