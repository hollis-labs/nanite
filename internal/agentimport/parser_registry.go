package agentimport

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
)

// RegistryParser drives CLIAgentAdapter.Import through an AdapterRegistry —
// the seam CW-20260910-0012 re-armed. It offers the path to each registered
// adapter in priority order and takes the first non-empty result.
//
// This is the ONLY caller of the adapter registry's import direction.
// internal/agent/discovery.go has no adapter tier: registration is import,
// and import runs when an operator names a path, not when the process starts.
type RegistryParser struct {
	Registry *agent.AdapterRegistry

	// Adapter, when set, names one adapter to use instead of trying them in
	// priority order — what a `--adapter` flag resolves to. An unknown name
	// is an error, not a silent fallback to guessing.
	Adapter string
}

var _ Parser = RegistryParser{}

// Parse returns the definitions produced by the first adapter that claims
// path, or (nil, nil) when none does.
func (p RegistryParser) Parse(path string) ([]*agent.Definition, error) {
	if p.Registry == nil {
		return nil, nil
	}

	if p.Adapter != "" {
		a, ok := p.Registry.GetAdapter(p.Adapter)
		if !ok {
			return nil, fmt.Errorf("no adapter named %q (registered: %s)", p.Adapter, strings.Join(adapterNames(p.Registry), ", "))
		}
		found, err := a.Import(path)
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", a.Name(), err)
		}
		return toPointers(found), nil
	}

	_, found, err := p.Registry.ImportAll(path)
	if err != nil {
		return nil, err
	}
	return toPointers(found), nil
}

// Name identifies this parser in a ChainParser's "nothing claimed it" report.
func (p RegistryParser) Name() string {
	if p.Adapter != "" {
		return "adapter:" + p.Adapter
	}
	if p.Registry == nil {
		return "adapters (none registered)"
	}
	return "adapters (" + strings.Join(adapterNames(p.Registry), ", ") + ")"
}

func adapterNames(r *agent.AdapterRegistry) []string {
	adapters := r.Adapters()
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name())
	}
	return names
}

// toPointers converts the registry's value slice to the pointer slice the
// pipeline works in. The pipeline stamps provenance onto each definition, so
// it needs to mutate them.
func toPointers(defs []agent.Definition) []*agent.Definition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]*agent.Definition, 0, len(defs))
	for i := range defs {
		out = append(out, &defs[i])
	}
	return out
}

// ChainParser tries parsers in order and returns the first non-empty result.
//
// Order is the format-precedence rule: Nanite's own format is tried first
// because a definition authored for Nanite is the ordinary case, and a
// foreign format is the exception. Nothing here merges results — a path has
// one format, and importing two readings of it produces an agent nobody
// wrote.
type ChainParser struct {
	Parsers []Parser
}

var _ Parser = ChainParser{}

func (c ChainParser) Parse(path string) ([]*agent.Definition, error) {
	var declines []string
	for _, p := range c.Parsers {
		if p == nil {
			continue
		}
		defs, err := p.Parse(path)
		if err != nil {
			// A decline carries a reason and does not stop the chain; a
			// real failure does. See ErrNotThisFormat.
			if errors.Is(err, ErrNotThisFormat) {
				declines = append(declines, fmt.Sprintf("%s: %v", parserName(p), unwrapDecline(err)))
				continue
			}
			return nil, err
		}
		if len(defs) > 0 {
			return defs, nil
		}
		declines = append(declines, parserName(p)+": declined")
	}
	if len(declines) > 0 {
		// Every reader declined. Report why, so an operator who mistyped a
		// definition is told what is wrong with it rather than being told
		// only that nothing was found.
		return nil, fmt.Errorf("%w: %s", ErrNotThisFormat, strings.Join(declines, "; "))
	}
	return nil, nil
}

// unwrapDecline strips the ErrNotThisFormat prefix so a joined report reads
// as one list of reasons rather than repeating the sentinel per entry.
func unwrapDecline(err error) string {
	msg := err.Error()
	return strings.TrimPrefix(msg, ErrNotThisFormat.Error()+": ")
}

// Name lists what was tried, so the "no agent definitions found" message can
// tell an operator which readers declined rather than leaving them to guess
// whether the path or the format was wrong.
func (c ChainParser) Name() string {
	names := make([]string, 0, len(c.Parsers))
	for _, p := range c.Parsers {
		if p == nil {
			continue
		}
		names = append(names, parserName(p))
	}
	return strings.Join(names, ", ")
}

// namedParser is an optional refinement: a parser that can say what it is.
type namedParser interface {
	Name() string
}

func parserName(p Parser) string {
	if n, ok := p.(namedParser); ok {
		return n.Name()
	}
	return fmt.Sprintf("%T", p)
}
