// runtime_dirs.go provisions the set of ~/.nanite/ subdirectories that hold
// pure runtime state (session sandboxes, plugin cache, etc.) and drops a
// README.md in each one so the directories are distinguishable from managed
// framework assets during drift audits.
//
// Design notes:
//   - Each runtime dir has a corresponding README embedded in the framework
//     asset tree under runtime-dir-readmes/<name>.md.
//   - README creation is idempotent: if a README.md already exists (user may
//     have customized it) we leave it alone.
//   - EnsureRuntimeDirs is called from InstallHome after the main framework
//     extract so it runs on every `nanite-agent init` and `--refresh`.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/assets"
)

// runtimeDir describes a single runtime-state directory under ~/.nanite/.
type runtimeDir struct {
	// name is the directory name (relative to the nanite home).
	name string
	// readmeAsset is the path within the framework asset tree
	// (relative to the framework root) for the README source.
	readmeAsset string
}

// runtimeDirs is the canonical list of runtime-state directories that
// EnsureRuntimeDirs will create and annotate. Order is stable but not
// significant.
var runtimeDirs = []runtimeDir{
	{name: "sandboxes", readmeAsset: "runtime-dir-readmes/sandboxes.md"},
	{name: "tool-output", readmeAsset: "runtime-dir-readmes/tool-output.md"},
	{name: "plugin-cache", readmeAsset: "runtime-dir-readmes/plugin-cache.md"},
	{name: "plugin-data", readmeAsset: "runtime-dir-readmes/plugin-data.md"},
	{name: "plugin-staging", readmeAsset: "runtime-dir-readmes/plugin-staging.md"},
	{name: "plugin-catalog", readmeAsset: "runtime-dir-readmes/plugin-catalog.md"},
	{name: "drafts", readmeAsset: "runtime-dir-readmes/drafts.md"},
	{name: "playbooks", readmeAsset: "runtime-dir-readmes/playbooks.md"},
}

// RuntimeDirReport summarizes what EnsureRuntimeDirs did.
type RuntimeDirReport struct {
	DirsCreated    int // directories newly created
	DirsExisted    int // directories that were already present
	READMEsCreated int // README.md files newly written
	READMEsSkipped int // README.md files left alone (user-modified or already correct)
}

// EnsureRuntimeDirs creates each runtime-state subdirectory under naniteHome
// (if it does not already exist) and writes a README.md into it (if one is
// not already present). It is idempotent and safe to call on every install
// or refresh run.
func EnsureRuntimeDirs(naniteHome string) (*RuntimeDirReport, error) {
	report := &RuntimeDirReport{}
	for _, rd := range runtimeDirs {
		dirPath := filepath.Join(naniteHome, rd.name)

		// Create the directory if it doesn't exist.
		switch _, err := os.Stat(dirPath); {
		case err == nil:
			report.DirsExisted++
		case errors.Is(err, fs.ErrNotExist):
			if mkErr := os.MkdirAll(dirPath, 0o755); mkErr != nil {
				return nil, fmt.Errorf("mkdir %s: %w", rd.name, mkErr)
			}
			report.DirsCreated++
		default:
			return nil, fmt.Errorf("stat %s: %w", rd.name, err)
		}

		// Drop the README — skip if one is already present (user owns it).
		readmePath := filepath.Join(dirPath, "README.md")
		if _, err := os.Stat(readmePath); err == nil {
			report.READMEsSkipped++
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("stat README.md in %s: %w", rd.name, err)
		}

		content, err := assets.File(rd.readmeAsset)
		if err != nil {
			return nil, fmt.Errorf("load readme asset %s: %w", rd.readmeAsset, err)
		}
		if err := os.WriteFile(readmePath, content, 0o644); err != nil {
			return nil, fmt.Errorf("write README.md in %s: %w", rd.name, err)
		}
		report.READMEsCreated++
	}
	return report, nil
}
