package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule is a single permission rule that matches tool invocations.
type Rule struct {
	Tool     string   `yaml:"tool"`     // tool name or glob pattern
	Pattern  string   `yaml:"pattern"`  // input pattern (glob on first string arg)
	Behavior Decision `yaml:"behavior"` // allow, deny, ask
	Source   string   `yaml:"-"`        // where this rule came from (for debugging)
}

// RuleSet holds an ordered list of rules and the permission mode.
type RuleSet struct {
	Mode  Mode   `yaml:"mode"`
	Rules []Rule `yaml:"rules"`
}

// PermissionsFile is the YAML structure for .conduit/permissions.yaml.
type PermissionsFile struct {
	Permissions RuleSet `yaml:"permissions"`
}

// Evaluate checks rules against a tool invocation. Returns the first matching
// rule's result, or nil if no rule matches.
//
// Evaluation priority: deny > ask > allow. Within the same behavior,
// first match wins.
func (rs *RuleSet) Evaluate(toolName string, input map[string]any) *CheckResult {
	// Sort by priority: deny first, then ask, then allow.
	var denyRules, askRules, allowRules []Rule
	for _, r := range rs.Rules {
		switch r.Behavior {
		case DecisionDeny:
			denyRules = append(denyRules, r)
		case DecisionAsk:
			askRules = append(askRules, r)
		case DecisionAllow:
			allowRules = append(allowRules, r)
		}
	}

	// Check deny rules first.
	for _, r := range denyRules {
		if r.Matches(toolName, input) {
			return &CheckResult{
				Decision:    DecisionDeny,
				MatchedRule: &r,
				Reason:      fmt.Sprintf("denied by rule: tool=%s pattern=%s", r.Tool, r.Pattern),
			}
		}
	}

	// Then ask rules.
	for _, r := range askRules {
		if r.Matches(toolName, input) {
			return &CheckResult{
				Decision:    DecisionAsk,
				MatchedRule: &r,
				Reason:      fmt.Sprintf("requires approval: tool=%s pattern=%s", r.Tool, r.Pattern),
			}
		}
	}

	// Then allow rules.
	for _, r := range allowRules {
		if r.Matches(toolName, input) {
			return &CheckResult{
				Decision:    DecisionAllow,
				MatchedRule: &r,
				Reason:      fmt.Sprintf("allowed by rule: tool=%s pattern=%s", r.Tool, r.Pattern),
			}
		}
	}

	return nil
}

// Matches checks if a rule matches the given tool name and input.
func (r *Rule) Matches(toolName string, input map[string]any) bool {
	// Match tool name (supports glob patterns).
	if !matchGlob(r.Tool, toolName) {
		return false
	}

	// If no pattern, tool name match is sufficient.
	if r.Pattern == "" {
		return true
	}

	// Match pattern against input. Check common input fields.
	return matchInputPattern(r.Pattern, input)
}

// matchGlob does simple glob matching. Supports * and ** wildcards.
func matchGlob(pattern, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return pattern == name
	}
	return matched
}

// matchInputPattern checks the rule pattern against tool input fields.
// Looks at common path-bearing fields: path, file, directory, command.
func matchInputPattern(pattern string, input map[string]any) bool {
	// Check path-like fields.
	for _, key := range []string{"path", "file", "directory", "file_path"} {
		if val, ok := input[key]; ok {
			if s, ok := val.(string); ok {
				if matchPathGlob(pattern, s) {
					return true
				}
			}
		}
	}

	// Check command field (for shell tools).
	if cmd, ok := input["command"]; ok {
		if s, ok := cmd.(string); ok {
			if strings.Contains(s, pattern) {
				return true
			}
		}
	}

	return false
}

// matchPathGlob matches a glob pattern against a file path.
func matchPathGlob(pattern, path string) bool {
	matched, err := filepath.Match(pattern, path)
	if err == nil && matched {
		return true
	}
	// Try prefix match for directory patterns like "/src/**".
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// LoadRulesFromFile reads a permissions YAML file.
func LoadRulesFromFile(path string) (*RuleSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read permissions file: %w", err)
	}

	var pf PermissionsFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse permissions file: %w", err)
	}

	// Tag rules with their source.
	for i := range pf.Permissions.Rules {
		pf.Permissions.Rules[i].Source = path
	}

	return &pf.Permissions, nil
}

// MergeRuleSets merges multiple rule sets in priority order (first = highest).
// Mode comes from the highest-priority set that has one specified.
func MergeRuleSets(sets ...*RuleSet) *RuleSet {
	merged := &RuleSet{}
	for _, rs := range sets {
		if rs == nil {
			continue
		}
		if merged.Mode == "" && rs.Mode != "" {
			merged.Mode = rs.Mode
		}
		merged.Rules = append(merged.Rules, rs.Rules...)
	}
	return merged
}

// SaveRulesToFile writes a permissions YAML file.
func SaveRulesToFile(path string, rs *RuleSet) error {
	pf := PermissionsFile{Permissions: *rs}
	data, err := yaml.Marshal(pf)
	if err != nil {
		return fmt.Errorf("marshal permissions: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create permissions dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
