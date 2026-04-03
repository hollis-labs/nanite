// Package broker implements progressive tool resolution for Conduit.
//
// Selection flows through three layers in order:
//   - Layer 1 (Explicit): caller-specified tools, skill bindings, agent config
//   - Layer 2 (Rule-Based): project/user rules, MCP sets, context mode rules
//   - Layer 3 (Classifier): signal scoring → mode preset → tool set
//
// Each layer either resolves a tool set (short-circuiting deeper layers)
// or passes through. After selection, token budget pruning and max-tools
// caps are applied externally.
package broker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/hollis-labs/conduit/internal/tool"
)

// Broker performs progressive tool resolution across three layers.
type Broker struct {
	registry *Registry
	rules    *RuleSet
}

// New creates a Broker with the given tool registry and rule set.
// If rules is nil, an empty rule set is used.
func New(registry *Registry, rules *RuleSet) *Broker {
	if rules == nil {
		rules = &RuleSet{}
	}
	return &Broker{
		registry: registry,
		rules:    rules,
	}
}

// IntentSignals carries all available context for tool selection.
type IntentSignals struct {
	// Explicit tool names requested by the caller.
	ExplicitTools []string

	// Agent configuration.
	AgentID       string
	AgentToolSet  []string // tools declared in agent config
	AgentMode     string   // agent's default mode (code, chat, etc.)

	// Session state.
	SessionMode    string   // explicit mode (plan, code, etc.)
	EnvelopeHints  []string // tags from recent envelopes
	LastToolUsed   string   // most recent tool category
	MessageKeywords []string // extracted from user message

	// Skill bindings — active skill constrains tool set.
	SkillTools []string
}

// Selection is the result of a broker Select call.
type Selection struct {
	Tools        []tool.Tool `json:"-"`
	ToolNames    []string    `json:"tool_names"`
	LayerReached string      `json:"layer_reached"` // "explicit", "rules", "classifier", "fallback"
	Intent       string      `json:"intent"`
	Signals      string      `json:"signals"` // JSON-encoded IntentSignals snapshot
}

// Select performs progressive resolution across all layers.
func (b *Broker) Select(ctx context.Context, signals IntentSignals) *Selection {
	start := time.Now()

	// Snapshot signals for decision logging.
	sigJSON, _ := json.Marshal(signals)

	// Layer 1: Explicit.
	if sel := b.layerExplicit(signals); sel != nil {
		sel.Signals = string(sigJSON)
		log.Printf("broker: layer 1 (explicit) resolved %d tools in %v", len(sel.Tools), time.Since(start))
		return sel
	}

	// Layer 2: Rule-based.
	if sel := b.layerRules(signals); sel != nil {
		sel.Signals = string(sigJSON)
		log.Printf("broker: layer 2 (rules) resolved %d tools in %v", len(sel.Tools), time.Since(start))
		return sel
	}

	// Layer 3: Classifier.
	if sel := b.layerClassifier(signals); sel != nil {
		sel.Signals = string(sigJSON)
		log.Printf("broker: layer 3 (classifier) resolved %d tools in %v", len(sel.Tools), time.Since(start))
		return sel
	}

	// Fallback: always-available set + top tools by category diversity.
	sel := b.fallback(signals)
	sel.Signals = string(sigJSON)
	log.Printf("broker: fallback resolved %d tools in %v", len(sel.Tools), time.Since(start))
	return sel
}

// fallback returns the always-available tools plus a best-effort selection.
func (b *Broker) fallback(_ IntentSignals) *Selection {
	var tools []tool.Tool

	// Always-available tools from rules.
	if len(b.rules.AlwaysAvailable) > 0 {
		tools = append(tools, b.registry.GetByNames(b.rules.AlwaysAvailable)...)
	}

	// If still empty, return up to 5 tools from the registry.
	if len(tools) == 0 {
		all := b.registry.All()
		limit := min(5, len(all))
		tools = all[:limit]
	}

	return &Selection{
		Tools:        tools,
		ToolNames:    toolNames(tools),
		LayerReached: "fallback",
		Intent:       "general",
	}
}

// toolNames extracts names from a tool slice.
func toolNames(tools []tool.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}
