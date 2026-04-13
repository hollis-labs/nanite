// Package contextbroker provides universal context retrieval for Mentat.
// It aggregates context from multiple sources (Vanta Conduit, PCC, Engine, Session)
// and returns a budget-bounded context packet for any consumer.
//
// This package will move to core/context during library consolidation.
package contextbroker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// Broker is the universal context retrieval service.
// It queries multiple ContextSource adapters, applies budget constraints,
// and returns a merged ContextPacket.
type Broker struct {
	sources []ContextSource
	budget  BudgetConfig
}

// BudgetConfig controls how much context to retrieve.
type BudgetConfig struct {
	// MaxTokens is the total token budget for context retrieval.
	// Default: 50000.
	MaxTokens int

	// SourceWeights maps source names to relative weight (0.0–1.0).
	// Sources not listed get equal share of remaining budget.
	// Example: {"conduit": 0.4, "pcc": 0.3, "engine": 0.15, "session": 0.15}
	SourceWeights map[string]float64
}

// DefaultBudget returns a sensible default budget configuration.
func DefaultBudget() BudgetConfig {
	return BudgetConfig{
		MaxTokens: 50000,
		SourceWeights: map[string]float64{
			"conduit": 0.25,
			"memory":  0.15,
			"pcc":     0.30,
			"engine":  0.15,
			"session": 0.15,
		},
	}
}

// ContextSource is the interface that all context adapters must implement.
type ContextSource interface {
	// Name returns the source identifier (e.g. "conduit", "pcc", "engine", "session").
	Name() string

	// Fetch retrieves context items for the given intent within a token budget.
	// Implementations should respect the budget and return items sorted by relevance.
	Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error)
}

// Intent describes what context is needed and why.
type Intent struct {
	// Type is the intent category (e.g. "resume_task", "boot_project", "write_code").
	Type string

	// Keywords are extracted from the user query for relevance matching.
	Keywords []string

	// Scope limits context to a project, namespace, or other boundary.
	Scope string

	// SessionID is the current chat session (for session-aware sources).
	SessionID string

	// AgentID is the requesting agent (for agent-aware sources).
	AgentID string
}

// ContextItem is a single piece of retrieved context.
type ContextItem struct {
	// Source identifies which adapter produced this item.
	Source string

	// Key is a unique identifier within the source (namespace/key, file path, task ID, etc).
	Key string

	// Content is the actual context text.
	Content string

	// TokenEstimate is the estimated token count for this item.
	TokenEstimate int

	// Relevance is a 0.0–1.0 score indicating how well this matches the intent.
	Relevance float64

	// Metadata holds source-specific extra information.
	Metadata map[string]string
}

// ContextPacket is the output of a Fetch call — a budget-bounded collection
// of context items with a manifest describing what was retrieved.
type ContextPacket struct {
	Items         []ContextItem
	Manifest      Manifest
	TokenEstimate int
}

// Manifest records what the broker did during retrieval.
type Manifest struct {
	Intent        Intent            `json:"intent"`
	SourcesUsed   []string          `json:"sources_used"`
	ItemCount     int               `json:"item_count"`
	TokenEstimate int               `json:"token_estimate"`
	TokenBudget   int               `json:"token_budget"`
	Truncated     bool              `json:"truncated"`
	Timings       map[string]string `json:"timings,omitempty"`
}

// New creates a new Broker with the given sources and budget.
func New(budget BudgetConfig, sources ...ContextSource) *Broker {
	if budget.MaxTokens <= 0 {
		budget.MaxTokens = DefaultBudget().MaxTokens
	}
	return &Broker{
		sources: sources,
		budget:  budget,
	}
}

// AddSource registers an additional context source.
func (b *Broker) AddSource(src ContextSource) {
	b.sources = append(b.sources, src)
}

// Fetch retrieves context from all registered sources, merges and trims
// to fit within the budget, and returns a ContextPacket.
func (b *Broker) Fetch(ctx context.Context, intent Intent) (*ContextPacket, error) {
	if len(b.sources) == 0 {
		return &ContextPacket{
			Manifest: Manifest{
				Intent:      intent,
				TokenBudget: b.budget.MaxTokens,
			},
		}, nil
	}

	budgets := b.allocateBudgets()
	timings := make(map[string]string)
	var allItems []ContextItem

	// Query each source with its allocated budget.
	for _, src := range b.sources {
		srcBudget := budgets[src.Name()]
		if srcBudget <= 0 {
			continue
		}

		start := time.Now()
		items, err := src.Fetch(ctx, intent, srcBudget)
		elapsed := time.Since(start)
		timings[src.Name()] = elapsed.String()

		if err != nil {
			slog.Warn("contextbroker: source error", "source", src.Name(), "err", err)
			continue
		}

		slog.Debug("contextbroker: source returned",
			"source", src.Name(), "items", len(items), "duration", elapsed, "budget", srcBudget)
		allItems = append(allItems, items...)
	}

	// Sort by relevance (highest first).
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].Relevance > allItems[j].Relevance
	})

	// Trim to fit total budget.
	packet := b.trimToBudget(allItems, intent)
	packet.Manifest.Timings = timings

	slog.Info("contextbroker: assembled packet",
		"items", packet.Manifest.ItemCount, "tokens", packet.TokenEstimate,
		"budget", b.budget.MaxTokens, "truncated", packet.Manifest.Truncated)

	return packet, nil
}

// allocateBudgets distributes the total token budget across sources
// according to configured weights.
func (b *Broker) allocateBudgets() map[string]int {
	budgets := make(map[string]int)
	total := b.budget.MaxTokens

	if len(b.budget.SourceWeights) > 0 {
		allocated := 0
		unweighted := 0
		for _, src := range b.sources {
			if w, ok := b.budget.SourceWeights[src.Name()]; ok {
				budget := int(float64(total) * w)
				budgets[src.Name()] = budget
				allocated += budget
			} else {
				unweighted++
			}
		}
		// Distribute remaining budget equally among unweighted sources.
		if unweighted > 0 {
			remaining := total - allocated
			perSource := remaining / unweighted
			for _, src := range b.sources {
				if _, ok := budgets[src.Name()]; !ok {
					budgets[src.Name()] = perSource
				}
			}
		}
	} else {
		// Equal distribution.
		perSource := total / len(b.sources)
		for _, src := range b.sources {
			budgets[src.Name()] = perSource
		}
	}

	return budgets
}

// trimToBudget selects items from the sorted list until the budget is exhausted.
func (b *Broker) trimToBudget(items []ContextItem, intent Intent) *ContextPacket {
	var kept []ContextItem
	var totalTokens int
	truncated := false
	sourcesUsed := make(map[string]bool)

	for _, item := range items {
		if totalTokens+item.TokenEstimate > b.budget.MaxTokens {
			truncated = true
			continue
		}
		kept = append(kept, item)
		totalTokens += item.TokenEstimate
		sourcesUsed[item.Source] = true
	}

	var sources []string
	for s := range sourcesUsed {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	return &ContextPacket{
		Items:         kept,
		TokenEstimate: totalTokens,
		Manifest: Manifest{
			Intent:        intent,
			SourcesUsed:   sources,
			ItemCount:     len(kept),
			TokenEstimate: totalTokens,
			TokenBudget:   b.budget.MaxTokens,
			Truncated:     truncated,
		},
	}
}

// EstimateTokens uses the standard (len+3)/4 heuristic for token estimation.
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// FormatPacket renders a ContextPacket as a string suitable for injection
// into a system prompt. Items are grouped by source with clear delimiters.
func FormatPacket(packet *ContextPacket) string {
	if packet == nil || len(packet.Items) == 0 {
		return ""
	}

	// Group items by source for readability.
	groups := make(map[string][]ContextItem)
	var order []string
	for _, item := range packet.Items {
		if _, exists := groups[item.Source]; !exists {
			order = append(order, item.Source)
		}
		groups[item.Source] = append(groups[item.Source], item)
	}

	var result string
	result = "--- Context (auto-retrieved) ---\n"
	for _, src := range order {
		items := groups[src]
		result += fmt.Sprintf("\n## %s\n", src)
		for _, item := range items {
			if item.Key != "" {
				result += fmt.Sprintf("\n### %s\n", item.Key)
			}
			result += item.Content + "\n"
		}
	}
	result += "\n--- End Context ---\n"
	return result
}
