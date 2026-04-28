package reflex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/classify"
)

// ReflexYAML is the YAML-loadable form of a Reflex. Field names mirror the
// schema documented in docs/agent-reflex-catalog.md so authors can write
// overrides in the same shape as the canonical documentation.
//
// User override files live at ~/.nanite/reflexes/*.yaml.
// Set priority >= 50 to reliably beat all built-in reflexes (max builtin: 25).
type ReflexYAML struct {
	ID       string       `yaml:"id"`
	Triggers TriggersYAML `yaml:"triggers"`
	ResolvesTo ResolutionYAML `yaml:"resolves_to"`
	SideEffects SideEffectsYAML `yaml:"side_effects"`
	Priority int          `yaml:"priority"`
}

// TriggersYAML is the YAML-loadable form of Triggers.
type TriggersYAML struct {
	UserPhraseAnyOf      []string `yaml:"user_phrase_any_of"`
	ScopeTierHint        string   `yaml:"scope_tier_hint"`
	ExecutionPatternHint string   `yaml:"execution_pattern_hint"`
}

// ResolutionYAML is the YAML-loadable form of Resolution.
type ResolutionYAML struct {
	Pattern string `yaml:"pattern"`
	Role    string `yaml:"role"`
	Profile string `yaml:"profile"`
}

// SideEffectsYAML is the YAML-loadable form of SideEffects.
type SideEffectsYAML struct {
	ModeSignal  string `yaml:"mode_signal"`
	DispatchVia string `yaml:"dispatch_via"`
}

// LoadUserReflexes globs ~/.nanite/reflexes/*.yaml and parses each file as a
// ReflexYAML. Invalid entries are skipped with an error logged to stderr (so
// one bad user file doesn't block startup). The returned slice is sorted
// descending by Priority (same order as BuiltinReflexes).
//
// If the directory does not exist, LoadUserReflexes returns (nil, nil) — a
// missing overrides dir is not an error.
func LoadUserReflexes(dir string) ([]Reflex, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("reflex: resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".nanite", "reflexes")
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reflex: glob user reflexes: %w", err)
	}

	var out []Reflex
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reflex: skip %s: read error: %v\n", path, err)
			continue
		}

		var ry ReflexYAML
		if err := yaml.Unmarshal(data, &ry); err != nil {
			fmt.Fprintf(os.Stderr, "reflex: skip %s: parse error: %v\n", path, err)
			continue
		}

		r, err := reflexFromYAML(ry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reflex: skip %s: schema error: %v\n", path, err)
			continue
		}
		out = append(out, r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Priority > out[j].Priority
	})
	return out, nil
}

// reflexFromYAML converts a parsed ReflexYAML to the canonical Reflex type,
// validating required fields.
func reflexFromYAML(ry ReflexYAML) (Reflex, error) {
	if ry.ID == "" {
		return Reflex{}, fmt.Errorf("id is required")
	}
	if len(ry.Triggers.UserPhraseAnyOf) == 0 {
		return Reflex{}, fmt.Errorf("triggers.user_phrase_any_of must have at least one entry")
	}

	scopeTier, err := parseScopeTier(ry.Triggers.ScopeTierHint)
	if err != nil {
		return Reflex{}, fmt.Errorf("triggers.scope_tier_hint: %w", err)
	}
	execPattern, err := parseExecPattern(ry.Triggers.ExecutionPatternHint)
	if err != nil {
		return Reflex{}, fmt.Errorf("triggers.execution_pattern_hint: %w", err)
	}

	return Reflex{
		ID: ry.ID,
		Triggers: Triggers{
			UserPhraseAnyOf:      ry.Triggers.UserPhraseAnyOf,
			ScopeTierHint:        scopeTier,
			ExecutionPatternHint: execPattern,
		},
		ResolvesTo: Resolution{
			Pattern: ry.ResolvesTo.Pattern,
			Role:    ry.ResolvesTo.Role,
			Profile: ry.ResolvesTo.Profile,
		},
		SideEffects: SideEffects{
			ModeSignal:  ry.SideEffects.ModeSignal,
			DispatchVia: ry.SideEffects.DispatchVia,
		},
		Priority: ry.Priority,
	}, nil
}

// parseScopeTier maps the YAML string form to the classify.ScopeTier constant.
// Empty string maps to 0 (no hint).
func parseScopeTier(s string) (classify.ScopeTier, error) {
	switch s {
	case "", "none":
		return 0, nil
	case "trivial":
		return classify.TierTrivial, nil
	case "small":
		return classify.TierSmall, nil
	case "medium":
		return classify.TierMedium, nil
	case "large":
		return classify.TierLarge, nil
	case "open":
		return classify.TierOpen, nil
	default:
		return 0, fmt.Errorf("unknown value %q (want: trivial|small|medium|large|open)", s)
	}
}

// parseExecPattern maps the YAML string form to the classify.ExecutionPattern
// constant. Empty string maps to 0 (no hint).
func parseExecPattern(s string) (classify.ExecutionPattern, error) {
	switch s {
	case "", "none":
		return 0, nil
	case "inline":
		return classify.PatternInline, nil
	case "subagent":
		return classify.PatternSubagent, nil
	case "background":
		return classify.PatternBackground, nil
	default:
		return 0, fmt.Errorf("unknown value %q (want: inline|subagent|background)", s)
	}
}
