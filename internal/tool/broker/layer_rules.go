package broker

import (
	"sort"

	"github.com/hollis-labs/conduit/internal/tool"
)

// layerRules is Layer 2: deterministic, zero-cost rule-based resolution.
// Matches intent keywords against rules, applies context mode constraints.
func (b *Broker) layerRules(signals IntentSignals) *Selection {
	if b.rules == nil || (len(b.rules.Rules) == 0 && len(b.rules.Presets) == 0) {
		return nil
	}

	keywords := signals.MessageKeywords

	// Context mode rules: plan mode → read-only tools only.
	mode := signals.SessionMode
	if mode == "" {
		mode = signals.AgentMode
	}

	// Check if a preset exists for the current mode.
	if mode != "" {
		if preset, ok := b.rules.Presets[mode]; ok && len(preset.Tools) > 0 {
			tools := b.registry.GetByNames(preset.Tools)
			if len(tools) > 0 {
				return &Selection{
					Tools:        tools,
					ToolNames:    toolNames(tools),
					LayerReached: "rules",
					Intent:       "preset:" + mode,
				}
			}
		}
	}

	// Match rules against intent keywords.
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		tool     tool.Tool
		priority int
	}

	var matches []scored
	seen := make(map[string]bool)

	// Sort rules by priority (highest first).
	sortedRules := make([]Rule, len(b.rules.Rules))
	copy(sortedRules, b.rules.Rules)
	sort.Slice(sortedRules, func(i, j int) bool {
		return sortedRules[i].Priority > sortedRules[j].Priority
	})

	for _, rule := range sortedRules {
		if !rule.MatchesIntent(keywords) {
			continue
		}
		// Find tools matching the rule's pattern.
		for _, t := range b.registry.All() {
			if seen[t.Name()] {
				continue
			}
			if MatchPattern(rule.Match, t.Name()) {
				// If in plan mode, only allow read-only tools.
				if mode == "plan" && !t.IsReadOnly(nil) {
					continue
				}
				seen[t.Name()] = true
				matches = append(matches, scored{tool: t, priority: rule.Priority})
			}
		}
	}

	if len(matches) == 0 {
		return nil
	}

	// Already sorted by priority via rule ordering.
	tools := make([]tool.Tool, len(matches))
	for i, m := range matches {
		tools[i] = m.tool
	}

	// Append always-available tools.
	for _, name := range b.rules.AlwaysAvailable {
		if seen[name] {
			continue
		}
		if t := b.registry.Get(name); t != nil {
			tools = append(tools, t)
		}
	}

	return &Selection{
		Tools:        tools,
		ToolNames:    toolNames(tools),
		LayerReached: "rules",
		Intent:       "rule-match",
	}
}
