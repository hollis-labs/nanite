package adapterclaude

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/agent"
)

// AdapterName is the ecosystem this adapter imports from. It is written into
// agent_profiles.origin_system for every row imported through it.
const AdapterName = "claude"

// SubagentsDir is where Claude Code keeps subagent definitions inside a
// project or a materialized boot directory.
const SubagentsDir = ".claude/agents"

// subagentFrontmatter is the Claude Code subagent format, which is NOT
// Nanite's own. The differences are the whole reason this adapter exists:
//
//   - `name` is the identifier. There is no `slug` field at all, so Nanite's
//     slug is derived here rather than read.
//   - `tools` is a comma-separated string of Claude tool names, or a list.
//     Either way the names belong to Claude's catalog, not Nanite's.
//   - `model` is a Claude alias ("sonnet", "opus"), not a Nanite model ID.
//
// Everything below the frontmatter is the system prompt. A charter/lens
// composition has already collapsed into that body by the time it reaches
// this format — Cairn performs the collapse when it renders. Nanite stores
// materialized instances, not recipes, so this adapter flattens to
// Definition.SystemPrompt and does not reconstruct the two axes.
type subagentFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Tools       any    `yaml:"tools"`
	Model       string `yaml:"model"`
}

// Import reads Claude Code subagent definitions from path.
//
// path may be:
//
//   - a single .md file — one definition;
//   - a directory containing .claude/agents/*.md — every subagent in it.
//     This is what a project root looks like, and also what a Cairn-planted
//     boot directory looks like, so "import whatever was just planted" needs
//     no separate code path;
//   - a directory that IS a .claude/agents (or any directory of subagent
//     files) — every .md in it.
//
// Anything else answers (nil, nil): not this adapter's format. Nothing here
// writes to path.
func (a *Adapter) Import(path string) ([]agent.Definition, error) {
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	if !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil, nil
		}
		def, err := parseSubagentFile(path)
		if err != nil {
			return nil, err
		}
		if def == nil {
			return nil, nil
		}
		return []agent.Definition{*def}, nil
	}

	// A project root or planted boot directory: the subagents live one level
	// down at .claude/agents. Prefer that over the directory's own *.md so a
	// boot directory's CLAUDE.md and prompt files are never mistaken for
	// agent definitions.
	nested := filepath.Join(path, SubagentsDir)
	if nestedInfo, err := os.Stat(nested); err == nil && nestedInfo.IsDir() {
		return importDir(nested)
	}

	return importDir(path)
}

// importDir reads every *.md directly inside dir, skipping files that are not
// this format rather than failing the whole directory on one stray file. A
// malformed file that IS this format is still an error: silently importing
// most of a directory is how an operator ends up with a roster nobody
// authored.
func importDir(dir string) ([]agent.Definition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var defs []agent.Definition
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		def, err := parseSubagentFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if def == nil {
			continue
		}
		defs = append(defs, *def)
	}
	return defs, nil
}

// parseSubagentFile reads one file. It returns (nil, nil) when the file is
// readable markdown that is simply not a Claude subagent — no frontmatter, or
// frontmatter with no `name`. It returns an error only when the file IS this
// format and cannot be used.
func parseSubagentFile(path string) (*agent.Definition, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-named; import is an explicitly invoked operation
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	fmBytes, body, ok := splitFrontmatter(data)
	if !ok {
		return nil, nil
	}

	var fm subagentFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, fmt.Errorf("%s: invalid YAML frontmatter: %w", path, err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		// No `name` means this is not a subagent — a CLAUDE.md or a stray
		// note living beside the real ones.
		return nil, nil
	}

	slug := slugifyName(fm.Name)
	if err := agent.ValidateSlug(slug); err != nil {
		return nil, fmt.Errorf("%s: cannot derive a usable slug from name %q: %w", path, fm.Name, err)
	}

	// Claude tool names ("Read", "Bash", "Grep") are not Nanite tool names
	// ("dev_bash"). Carrying them across would write grants that can never
	// resolve — precisely the silent failure CW-20260815-0013 added the
	// unknown-tool check to catch, except pre-seeded by an import rather than
	// typed by hand. Nanite does not own a mapping between the two catalogs
	// and guessing one would be worse than leaving the field empty, so the
	// list is dropped and named in the log rather than dropped quietly.
	if tools := claudeToolNames(fm.Tools); len(tools) > 0 {
		slog.Warn("adapter-claude: dropping Claude tool names on import — they do not exist in Nanite's tool catalog and would produce grants that never resolve; grant Nanite tools to the imported agent instead",
			"path", path, "slug", slug, "dropped_tools", tools)
	}

	// `model` is left blank, deliberately and always. A Claude alias
	// ("sonnet") is not a Nanite model ID, and even a real ID belongs blank
	// so store.ResolveProviderAndModel re-resolves per request — see
	// internal/agent/parser.go's Model doc comment (CW-20260815-0021: ten
	// profiles independently pinned a model and each one is a future 404).
	if strings.TrimSpace(fm.Model) != "" {
		slog.Warn("adapter-claude: dropping the source's model on import — Nanite resolves provider and model per request (ResolveProviderAndModel); a pinned alias would silently break when it is retired",
			"path", path, "slug", slug, "dropped_model", fm.Model)
	}

	return &agent.Definition{
		Name:        fm.Name,
		Slug:        slug,
		Description: strings.TrimSpace(fm.Description),
		// The body IS the agent. Charter and lens have already collapsed
		// into it upstream; see subagentFrontmatter's doc comment.
		SystemPrompt: strings.TrimSpace(string(body)),
		// Source names the ecyosystem this came FROM. internal/agentimport
		// records it as origin_system and stamps its own provenance marker
		// into the stored row's source column — an adapter never declares an
		// imported agent operator-owned.
		Source:    AdapterName,
		SourceRef: path,
	}, nil
}

// claudeToolNames normalizes the two shapes Claude's `tools` field takes: a
// comma-separated string, or a YAML list.
func claudeToolNames(raw any) []string {
	var out []string
	switch v := raw.(type) {
	case string:
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if p := strings.TrimSpace(s); p != "" {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugifyName derives a Nanite slug from a Claude subagent's `name`. Claude
// names are lowercase-hyphenated by convention but not by rule, so this
// normalizes rather than assuming. agent.ValidateSlug is the gate on the
// result — this function does not get to decide what is acceptable.
func slugifyName(name string) string {
	s := nonSlugChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	return strings.Trim(s, "-")
}

var frontmatterDelim = []byte("---")

// splitFrontmatter splits YAML frontmatter from the markdown body. It reports
// ok=false for content that does not open with a `---` line, which is this
// adapter's "not my format" signal rather than an error.
//
// Deliberately a local copy of internal/agent/parser.go's unexported
// splitFrontmatter rather than an export of it: that function is the Nanite
// format's reader, and an adapter that shares it would drift into sharing the
// format. Ten lines of duplication is the cheaper side of that trade.
func splitFrontmatter(data []byte) (frontmatter, body []byte, ok bool) {
	trimmed := strings.TrimLeft(string(data), "\n\r")
	if !strings.HasPrefix(trimmed, string(frontmatterDelim)) {
		return nil, nil, false
	}

	rest := trimmed[len(frontmatterDelim):]
	idx := strings.Index(rest, "\n")
	if idx < 0 {
		return nil, nil, false
	}
	rest = rest[idx+1:]

	closeIdx := strings.Index(rest, "\n"+string(frontmatterDelim))
	if closeIdx < 0 {
		return nil, nil, false
	}
	frontmatter = []byte(rest[:closeIdx])

	after := rest[closeIdx+1+len(frontmatterDelim):]
	if nl := strings.Index(after, "\n"); nl >= 0 {
		body = []byte(after[nl+1:])
	}
	return frontmatter, body, true
}
