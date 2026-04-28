package toolclient

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ToolPreferenceSkill is a single operator-authored "when intent X, prefer
// tools [A, B]" rule. Surfaced to the broker as a higher-weight ranking
// signal than keyword match (see ranking.go).
//
// Authoring format: see internal/toolclient/testdata/skills/example.tools.preferences.md
// for the canonical layout. Files are Markdown with a YAML frontmatter:
//
//	---
//	pattern: "search code|investigate|find function"
//	prefer: [dev_glob, dev_grep, dev_read]
//	weight: 7
//	rationale: "Codebase exploration starts with the dev_* triad."
//	---
//	# Free-form prose follows. The body is ignored by the broker but useful
//	# for humans browsing the directory.
//
// Fields:
//   - Pattern: a substring (lower-cased compare) OR a regex (when prefixed
//     with `re:`). Empty pattern matches every intent.
//   - Prefer: ordered list of tool names to bias toward. Matching is by
//     exact tool name on the agent-facing surface (post ADR-002 — no
//     `mcp__` prefixes).
//   - Weight: ranking weight added per matching tool. Default 5. Skills
//     outweigh memory recall (which adds 3) and keyword match (which adds
//     1-2 via SelectByIntent). See ranking.go for the precedence rationale.
//   - Rationale: free-form; surfaced in slog so an operator can tell which
//     skill influenced a selection.
//   - Source: the file path (set by LoadSkillsFromDir) for diagnostics.
type ToolPreferenceSkill struct {
	Pattern   string   `yaml:"pattern"`
	Prefer    []string `yaml:"prefer"`
	Weight    int      `yaml:"weight"`
	Rationale string   `yaml:"rationale"`
	Source    string   `yaml:"-"`

	// compiledRegex is non-nil when Pattern is `re:<expr>`. Compiled once
	// at load time so MatchingSkills doesn't pay per-call regex compilation.
	compiledRegex *regexp.Regexp
}

// DefaultSkillWeight is the rank contribution per matching tool when a skill
// does not specify Weight. Tuned to outweigh memory (3) and keyword (1-2)
// without dominating completely — three keyword matches still tie one skill
// hit on Tier 1, which protects against an over-eager skill drowning out
// genuine relevance.
const DefaultSkillWeight = 5

// MatchingSkills returns the subset of skills whose Pattern matches the
// given intent. Matching is case-insensitive substring by default; patterns
// prefixed with "re:" are evaluated as Go regexp.
//
// An empty intent returns no matches — operator skills should not fire on
// the wildcard fallback path. (Wildcard intent → minimal safe set, which is
// the broker's existing behaviour.)
func MatchingSkills(skills []ToolPreferenceSkill, intent string) []ToolPreferenceSkill {
	if intent == "" || intent == "*" {
		return nil
	}
	loweredIntent := strings.ToLower(intent)

	var matches []ToolPreferenceSkill
	for _, sk := range skills {
		if skillMatches(sk, loweredIntent) {
			matches = append(matches, sk)
		}
	}
	return matches
}

func skillMatches(sk ToolPreferenceSkill, loweredIntent string) bool {
	if sk.Pattern == "" {
		return true
	}
	if sk.compiledRegex != nil {
		return sk.compiledRegex.MatchString(loweredIntent)
	}
	return strings.Contains(loweredIntent, strings.ToLower(sk.Pattern))
}

// LoadSkillsFromDir scans `dir` (and its first level of subdirectories) for
// `*.tools.preferences.md` files and returns the parsed skills. Missing
// directory is not an error — it returns nil with no error so the broker
// can be opt-in (default behaviour is unchanged when the dir is absent).
//
// Malformed files emit a slog warning and are skipped — a typo in one file
// must not deny tool selection wholesale.
func LoadSkillsFromDir(dir string) ([]ToolPreferenceSkill, error) {
	if dir == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		slog.Debug("toolclient: skills dir absent, skipping", "dir", dir)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat skills dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills path %q is not a directory", dir)
	}

	var out []ToolPreferenceSkill
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("toolclient: skills walk error", "path", path, "err", err)
			return nil // continue
		}
		if d.IsDir() {
			// Limit depth: only the dir itself + first-level children.
			rel, _ := filepath.Rel(dir, path)
			if rel == "." {
				return nil
			}
			if strings.Contains(rel, string(filepath.Separator)) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".tools.preferences.md") {
			return nil
		}
		sk, err := parseSkillFile(path)
		if err != nil {
			slog.Warn("toolclient: failed to parse skill file", "path", path, "err", err)
			return nil
		}
		out = append(out, sk)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk skills dir: %w", walkErr)
	}
	slog.Info("toolclient: loaded operator tool-preference skills", "dir", dir, "count", len(out))
	return out, nil
}

// parseSkillFile reads one `*.tools.preferences.md` file and returns its
// parsed ToolPreferenceSkill. Frontmatter must be a YAML block at the top of
// the file delimited by `---` lines; the prose body after the closing `---`
// is ignored.
func parseSkillFile(path string) (ToolPreferenceSkill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ToolPreferenceSkill{}, fmt.Errorf("read: %w", err)
	}

	raw := string(data)
	if !strings.HasPrefix(raw, "---") {
		return ToolPreferenceSkill{}, fmt.Errorf("missing YAML frontmatter")
	}
	rest := strings.TrimPrefix(raw, "---")
	// Find the closing `---` on its own line (possibly with surrounding whitespace).
	end := -1
	lines := strings.Split(rest, "\n")
	idx := 0
	// Track byte index as we walk lines.
	for i, line := range lines {
		idx += len(line) + 1 // +1 for the newline removed by Split
		if i == 0 {
			// First line is the leftover after the opening "---" (typically empty).
			continue
		}
		if strings.TrimSpace(line) == "---" {
			end = idx
			break
		}
	}
	if end < 0 {
		return ToolPreferenceSkill{}, fmt.Errorf("missing closing --- frontmatter delimiter")
	}

	// frontmatter spans from after the opening "---" to the line before the closing "---".
	fmStart := 0
	// Skip the first newline after opening "---".
	if len(rest) > 0 && rest[0] == '\n' {
		fmStart = 1
	}
	frontmatter := rest[fmStart : end-len("---\n")]
	// Strip the trailing closing "---\n" if our slicing was off by 1 (trailing line without newline).
	frontmatter = strings.TrimSuffix(frontmatter, "---")

	var sk ToolPreferenceSkill
	if err := yaml.Unmarshal([]byte(frontmatter), &sk); err != nil {
		return ToolPreferenceSkill{}, fmt.Errorf("yaml: %w", err)
	}
	if sk.Weight <= 0 {
		sk.Weight = DefaultSkillWeight
	}
	if strings.HasPrefix(sk.Pattern, "re:") {
		expr := strings.TrimPrefix(sk.Pattern, "re:")
		re, err := regexp.Compile("(?i)" + expr)
		if err != nil {
			return ToolPreferenceSkill{}, fmt.Errorf("compile pattern regex: %w", err)
		}
		sk.compiledRegex = re
	}
	sk.Source = path
	return sk, nil
}

// DefaultSkillsDir is the conventional location of operator-authored
// tool-preference skills. Resolved relative to $HOME at runtime.
//
// We co-locate with the existing `~/.nanite/skills/` directory (per
// .nanite/config.yaml conventions) but use a distinct file extension
// (`.tools.preferences.md`) so general session-level skills (e.g.
// `playbook.md`, `go-build.md`) and broker tool-preference skills don't
// collide in the same flat directory.
const DefaultSkillsDir = ".nanite/skills"

// DefaultSkillsPath returns the absolute path to the conventional skills
// directory for the current user. Returns "" when $HOME is unresolvable —
// the loader treats "" as "feature off" so missing $HOME never crashes.
func DefaultSkillsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, DefaultSkillsDir)
}
