package agentimport

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/agent"
)

// NativeParser reads Nanite's own agent definition format: a single markdown
// file with YAML frontmatter, exactly as internal/agent/parser.go defines it.
//
// It handles ONE file per call and deliberately does not walk a directory.
// Directory expansion is a format question — how many agents a directory
// holds, and whether a directory might itself BE one agent (a materialized
// boot directory) — so it belongs to the format adapter that understands
// the layout, not to the generic native reader. A directory handed to this
// parser reports (nil, nil), meaning "not mine," which lets a registry of
// parsers try the next one.
type NativeParser struct{}

var _ Parser = NativeParser{}

// Parse reads path as a single Nanite agent markdown file.
func (NativeParser) Parse(path string) ([]*agent.Definition, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, nil
	}
	if ext := filepath.Ext(path); ext != ".md" {
		return nil, nil
	}

	// ParseMDFile sets SourceRef to path and resolves any procedure
	// body_file references relative to the file's own directory. It is a
	// pure read: nothing is written back to the source, per the one-way
	// rule in this package's doc comment.
	def, err := agent.ParseMDFile(path)
	if err != nil {
		return nil, err
	}
	return []*agent.Definition{def}, nil
}
