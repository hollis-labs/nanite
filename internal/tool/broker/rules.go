package broker

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuleSet holds the merged broker rules from all sources.
type RuleSet struct {
	Rules           []Rule            `yaml:"rules"`
	Presets         map[string]Preset `yaml:"presets"`
	AlwaysAvailable []string          `yaml:"always_available"`
}

// Rule maps intent patterns to tool name patterns with a priority.
type Rule struct {
	Match    string   `yaml:"match"`    // tool name glob (e.g. "mcp__dev__*")
	Intent   []string `yaml:"intent"`   // intents this rule applies to
	Priority int      `yaml:"priority"` // higher = more preferred
}

// Preset is a named set of tools for a mode.
type Preset struct {
	Tools []string `yaml:"tools"`
}

// brokerYAML is the on-disk format for broker.yaml files.
type brokerYAML struct {
	ToolBroker struct {
		Rules           []Rule            `yaml:"rules"`
		Presets         map[string]Preset `yaml:"presets"`
		AlwaysAvailable []string          `yaml:"always_available"`
	} `yaml:"tool_broker"`
}

// LoadRules loads and merges broker rules from the standard locations.
// Priority (highest to lowest): project (.nanite/broker.yaml) → user (~/.nanite/broker.yaml).
// workingDir is the project root.
func LoadRules(workingDir string) *RuleSet {
	merged := &RuleSet{
		Presets: make(map[string]Preset),
	}

	// Load user-level first (lower priority — project overrides).
	home, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(home, ".nanite", "broker.yaml")
		if rs, err := loadRuleFile(userPath); err == nil {
			mergeRuleSet(merged, rs)
		}
	}

	// Load project-level (higher priority).
	if workingDir != "" {
		projectPath := filepath.Join(workingDir, ".nanite", "broker.yaml")
		if rs, err := loadRuleFile(projectPath); err == nil {
			mergeRuleSet(merged, rs)
		}
	}

	if len(merged.Rules) > 0 || len(merged.Presets) > 0 {
		log.Printf("broker/rules: loaded %d rules, %d presets, %d always-available",
			len(merged.Rules), len(merged.Presets), len(merged.AlwaysAvailable))
	}

	return merged
}

// loadRuleFile parses a single broker.yaml file.
func loadRuleFile(path string) (*RuleSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc brokerYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	presets := doc.ToolBroker.Presets
	if presets == nil {
		presets = make(map[string]Preset)
	}

	return &RuleSet{
		Rules:           doc.ToolBroker.Rules,
		Presets:         presets,
		AlwaysAvailable: doc.ToolBroker.AlwaysAvailable,
	}, nil
}

// mergeRuleSet merges src into dst. src rules are appended. src presets
// override dst presets (project > user). AlwaysAvailable is unioned.
func mergeRuleSet(dst, src *RuleSet) {
	dst.Rules = append(dst.Rules, src.Rules...)
	for name, preset := range src.Presets {
		dst.Presets[name] = preset
	}
	seen := make(map[string]bool)
	for _, t := range dst.AlwaysAvailable {
		seen[t] = true
	}
	for _, t := range src.AlwaysAvailable {
		if !seen[t] {
			dst.AlwaysAvailable = append(dst.AlwaysAvailable, t)
			seen[t] = true
		}
	}
}

// MatchesIntent checks if a rule applies to the given intent keywords.
func (r *Rule) MatchesIntent(keywords []string) bool {
	for _, kw := range keywords {
		for _, ri := range r.Intent {
			if strings.EqualFold(kw, ri) {
				return true
			}
		}
	}
	return false
}

// MatchesTool checks if the rule's pattern matches a tool name.
// Supports simple glob: "*" matches any suffix, "prefix*" matches prefix.
func MatchPattern(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(name, prefix)
	}
	return pattern == name
}
