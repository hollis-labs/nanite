package broker

import (
	"strings"

	"github.com/hollis-labs/nanite/internal/tool"
)

// classifierMode is an inferred operating mode from signal scoring.
type classifierMode struct {
	name  string
	score int
}

// modeKeywords maps mode names to keywords that score towards them.
var modeKeywords = map[string][]string{
	"code":     {"code", "edit", "write", "fix", "bug", "refactor", "implement", "function", "class", "file", "test", "lint", "build", "compile"},
	"research": {"search", "find", "look", "research", "fetch", "read", "documentation", "docs", "learn", "explore", "investigate"},
	"plan":     {"plan", "design", "architecture", "strategy", "approach", "outline", "breakdown", "task", "sprint", "epic", "roadmap"},
	"task":     {"task", "create", "update", "assign", "sprint", "backlog", "ticket", "issue", "track"},
	"chat":     {"chat", "explain", "describe", "help", "question", "answer", "clarify", "summarize"},
}

// modeCategories maps modes to the tool categories they prefer.
var modeCategories = map[string][]string{
	"code":     {tool.CategoryCoreIO, tool.CategorySearch},
	"research": {tool.CategorySearch, tool.CategoryContext},
	"plan":     {tool.CategoryAgent, tool.CategorySession, tool.CategoryMode},
	"task":     {tool.CategoryAgent, tool.CategorySession},
	"chat":     {tool.CategoryContext, tool.CategorySession},
}

// classifyConfidenceThreshold is the minimum score to trust the classifier.
const classifyConfidenceThreshold = 3

// layerClassifier is Layer 3: near-zero-cost heuristic classification.
// Scores available signals against known modes and selects tools by category.
func (b *Broker) layerClassifier(signals IntentSignals) *Selection {
	scores := make(map[string]int)

	// Score from message keywords.
	for _, kw := range signals.MessageKeywords {
		kwLower := strings.ToLower(kw)
		for mode, keywords := range modeKeywords {
			for _, mk := range keywords {
				if kwLower == mk {
					scores[mode] += 2
				}
			}
		}
	}

	// Score from agent mode.
	if signals.AgentMode != "" {
		scores[signals.AgentMode] += 3
	}

	// Score from session mode.
	if signals.SessionMode != "" {
		scores[signals.SessionMode] += 5 // explicit mode is a strong signal
	}

	// Score from envelope hints.
	for _, hint := range signals.EnvelopeHints {
		hintLower := strings.ToLower(hint)
		for mode, keywords := range modeKeywords {
			for _, mk := range keywords {
				if hintLower == mk {
					scores[mode]++
				}
			}
		}
	}

	// Score from last tool used — boost the mode whose category matches.
	if signals.LastToolUsed != "" {
		for mode, cats := range modeCategories {
			for _, cat := range cats {
				if signals.LastToolUsed == cat {
					scores[mode]++
				}
			}
		}
	}

	// Find highest scoring mode.
	best := classifierMode{}
	for mode, score := range scores {
		if score > best.score {
			best = classifierMode{name: mode, score: score}
		}
	}

	// Below confidence threshold — pass through to fallback.
	if best.score < classifyConfidenceThreshold {
		return nil
	}

	// Check if we have a preset for this mode.
	if b.rules != nil {
		if preset, ok := b.rules.Presets[best.name]; ok && len(preset.Tools) > 0 {
			tools := b.registry.GetByNames(preset.Tools)
			if len(tools) > 0 {
				return &Selection{
					Tools:        tools,
					ToolNames:    toolNames(tools),
					LayerReached: "classifier",
					Intent:       "classified:" + best.name,
				}
			}
		}
	}

	// No preset — select by category.
	categories, ok := modeCategories[best.name]
	if !ok {
		return nil
	}

	seen := make(map[string]bool)
	var tools []tool.Tool
	for _, cat := range categories {
		for _, t := range b.registry.ByCategory(cat) {
			if !seen[t.Name()] {
				seen[t.Name()] = true
				tools = append(tools, t)
			}
		}
	}

	// Append always-available tools.
	if b.rules != nil {
		for _, name := range b.rules.AlwaysAvailable {
			if !seen[name] {
				if t := b.registry.Get(name); t != nil {
					seen[t.Name()] = true
					tools = append(tools, t)
				}
			}
		}
	}

	if len(tools) == 0 {
		return nil
	}

	return &Selection{
		Tools:        tools,
		ToolNames:    toolNames(tools),
		LayerReached: "classifier",
		Intent:       "classified:" + best.name,
	}
}
