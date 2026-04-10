package install

import (
	"os"
	"path/filepath"
)

// adapterEvidence maps adapter slugs to the project-root paths whose
// presence indicates that adapter's CLI tool is in use. Both a markdown
// file and a dot-directory are checked; either one triggers detection.
//
// nanite-native is intentionally absent — it's not a CLI integration.
// It manages .nanite/ and NANITE.md, which are always Nanite's own.
var adapterEvidence = map[string]struct {
	rootFile string
	dotDir   string
}{
	"claude":   {"CLAUDE.md", ".claude"},
	"codex":    {"AGENTS.md", ".codex"},
	"gemini":   {"GEMINI.md", ".gemini"},
	"opencode": {"OPENCODE.md", ".opencode"},
}

// DetectAdapters scans projectDir for filesystem evidence of CLI tools
// (project-root markdown files or .cli/ subdirectories) and returns the
// slugs of adapters that should be considered "in use" for this project.
//
// Each adapter is checked once. The order of returned slugs is not
// guaranteed — callers should sort if they need determinism.
func DetectAdapters(projectDir string) []string {
	var found []string
	for slug, evidence := range adapterEvidence {
		if hasFile(filepath.Join(projectDir, evidence.rootFile)) ||
			hasDir(filepath.Join(projectDir, evidence.dotDir)) {
			found = append(found, slug)
		}
	}
	return found
}

func hasFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func hasDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
