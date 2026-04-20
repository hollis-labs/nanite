// Package broker implements progressive tool resolution for Nanite.
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
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/tool/enrichment"
)

// Broker performs progressive tool resolution across three layers.
type Broker struct {
	registry *Registry
	rules    *RuleSet
	enricher enrichment.Enricher
}

// Option configures a Broker at construction time.
type Option func(*Broker)

// WithEnricher wires an enrichment.Enricher into the broker so Select() populates
// Selection.OverrideBlock. Nil is permitted and makes overrides a no-op.
func WithEnricher(e enrichment.Enricher) Option {
	return func(b *Broker) {
		b.enricher = e
	}
}

// New creates a Broker with the given tool registry and rule set.
// If rules is nil, an empty rule set is used.
func New(registry *Registry, rules *RuleSet, opts ...Option) *Broker {
	if rules == nil {
		rules = &RuleSet{}
	}
	b := &Broker{
		registry: registry,
		rules:    rules,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
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

	// OverrideBlock is the markdown section produced by enrichment.ComposeOverrideBlock
	// for the selected tools, ready to append to the system prompt. Empty when no
	// tool has enrichment or no Enricher is configured on the broker.
	OverrideBlock string `json:"-"`
}

// Select performs progressive resolution across all layers.
func (b *Broker) Select(ctx context.Context, signals IntentSignals) *Selection {
	start := time.Now()

	// Snapshot signals for decision logging.
	sigJSON, _ := json.Marshal(signals)

	// Layer 1: Explicit.
	if sel := b.layerExplicit(signals); sel != nil {
		sel.Signals = string(sigJSON)
		b.applyOverrideBlock(ctx, sel)
		slog.Debug("broker: layer 1 (explicit) resolved", "count", len(sel.Tools), "duration", time.Since(start))
		return sel
	}

	// Layer 2: Rule-based.
	if sel := b.layerRules(signals); sel != nil {
		sel.Signals = string(sigJSON)
		b.applyOverrideBlock(ctx, sel)
		slog.Debug("broker: layer 2 (rules) resolved", "count", len(sel.Tools), "duration", time.Since(start))
		return sel
	}

	// Layer 3: Classifier.
	if sel := b.layerClassifier(signals); sel != nil {
		sel.Signals = string(sigJSON)
		b.applyOverrideBlock(ctx, sel)
		slog.Debug("broker: layer 3 (classifier) resolved", "count", len(sel.Tools), "duration", time.Since(start))
		return sel
	}

	// Fallback: always-available set + top tools by category diversity.
	sel := b.fallback(signals)
	sel.Signals = string(sigJSON)
	b.applyOverrideBlock(ctx, sel)
	slog.Debug("broker: fallback resolved", "count", len(sel.Tools), "duration", time.Since(start))
	return sel
}

// applyOverrideBlock composes Selection.OverrideBlock from the configured enricher,
// if any. Errors are logged and swallowed — selection cannot fail on enrichment,
// worst case no overrides ship this turn.
func (b *Broker) applyOverrideBlock(ctx context.Context, sel *Selection) {
	if b.enricher == nil || sel == nil {
		return
	}
	block, err := enrichment.ComposeOverrideBlock(ctx, sel.ToolNames, b.enricher)
	if err != nil {
		slog.Warn("broker: compose override block failed", "err", err)
		return
	}
	sel.OverrideBlock = block
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
