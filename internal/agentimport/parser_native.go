package agentimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
)

// NativeOriginSystem is recorded as origin_system for a definition authored
// in Nanite's own format.
const NativeOriginSystem = "nanite"

// NativeParser reads Nanite's own agent definition format: a single markdown
// file with YAML frontmatter, as internal/agent/parser.go defines it.
//
// # What makes a file "Nanite's format"
//
// An explicit `slug:` in the frontmatter, and nothing less. That is a real
// discriminator rather than a formality: internal/agent.ParseMDFile falls
// back to the filename when frontmatter omits a slug, which would let this
// parser cheerfully claim a Claude subagent file (`name:` and no `slug:`) and
// import it under Nanite's reading of a foreign format — carrying across a
// `model:` alias and choking on a comma-separated `tools:` string. Requiring
// the declaration keeps first-in-the-chain from meaning first-to-guess.
//
// A file it declines comes back as ErrNotThisFormat carrying the underlying
// reason, so an operator who mistyped a Nanite definition is told what is
// wrong with it rather than being told nothing recognized the path.
//
// # One file, never a directory
//
// Directory expansion is a format question — how many agents a directory
// holds, and whether a directory might itself BE one agent (a materialized
// boot directory) — so it belongs to the format adapter that understands the
// layout. A directory handed here is declined.
type NativeParser struct{}

var _ Parser = NativeParser{}

// Name identifies this parser in a ChainParser's report.
func (NativeParser) Name() string { return "nanite (frontmatter with an explicit slug)" }

// Parse reads path as a single Nanite agent markdown file.
func (NativeParser) Parse(path string) ([]*agent.Definition, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s is a directory", ErrNotThisFormat, path)
	}
	if ext := filepath.Ext(path); !strings.EqualFold(ext, ".md") {
		return nil, fmt.Errorf("%w: %s is not a .md file", ErrNotThisFormat, path)
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is operator-named; import is an explicitly invoked operation
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// ParseMD, unlike ParseMDFile, has no filename fallback for the slug —
	// so a successful call here IS the format check described above.
	if _, parseErr := agent.ParseMD(data); parseErr != nil {
		// Joined rather than %w-wrapped on one of them: the decline
		// sentinel is what a ChainParser matches on, and the parse error is
		// what an operator needs to read. Both matter.
		return nil, fmt.Errorf("%w: %w", ErrNotThisFormat, parseErr)
	}

	// ParseMDFile sets SourceRef to path and resolves any procedure
	// body_file references relative to the file's own directory. It is a
	// pure read: nothing is written back to the source, per the one-way
	// rule in this package's doc comment.
	def, err := agent.ParseMDFile(path)
	if err != nil {
		return nil, err
	}
	// Source names the ecosystem this came FROM. write() records it as
	// origin_system and stamps SourceProvenance into the stored row.
	def.Source = NativeOriginSystem
	return []*agent.Definition{def}, nil
}

// ErrNotThisFormat marks a parser's decline. A parser returning it is saying
// "this path is not mine," not "this path is broken" — a ChainParser moves on
// to the next parser and only surfaces the accumulated reasons if nothing
// claims the path at all.
//
// It exists so declining can carry an explanation. A bare (nil, nil) decline
// is still valid and means "not mine, no comment."
var ErrNotThisFormat = errors.New("not this format")
