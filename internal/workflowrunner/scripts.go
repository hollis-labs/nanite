package workflowrunner

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// embeddedScripts bakes the production external-engine runner scripts
// (langgraph_runner.py, crewai_runner.py) plus their requirements.txt
// straight into the nanite binary. A Cerberus-deployed binary ships alone
// — there is no guarantee a repo checkout sits next to it — so ScriptPath
// can't point at a source-tree-relative path in production; embedding plus
// MaterializeScripts below is what makes these scripts locatable no matter
// how the binary got onto the host (CW-20260814-0003).
//
//go:embed scripts/langgraph_runner.py scripts/crewai_runner.py scripts/requirements.txt
var embeddedScripts embed.FS

// MaterializeScripts writes every embedded script under scripts/ to dir
// (created if missing), overwriting any copy left by a prior binary
// version — the embedded bytes are always this binary's source of truth,
// never merged with or preserved against whatever's already on disk.
// Returns each script's on-disk path keyed by base filename (e.g.
// "langgraph_runner.py"), for a caller to plant as an
// ExternalWorkflowEngineConfig.ScriptPath.
func MaterializeScripts(dir string) (map[string]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("workflowrunner: MaterializeScripts: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("workflowrunner: create scripts dir %s: %w", dir, err)
	}

	entries, err := embeddedScripts.ReadDir("scripts")
	if err != nil {
		return nil, fmt.Errorf("workflowrunner: read embedded scripts: %w", err)
	}

	paths := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := embeddedScripts.ReadFile(filepath.Join("scripts", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("workflowrunner: read embedded script %s: %w", entry.Name(), err)
		}
		dest := filepath.Join(dir, entry.Name())
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return nil, fmt.Errorf("workflowrunner: write %s: %w", dest, err)
		}
		paths[entry.Name()] = dest
	}
	return paths, nil
}
