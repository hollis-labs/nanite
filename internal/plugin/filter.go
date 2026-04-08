package plugin

import (
	"fmt"
	"sort"
	"sync"
)

// FilterView specifies what context subset a filter receives.
type FilterView string

const (
	// FilterViewFull is the default — filter sees all context.
	FilterViewFull FilterView = "full"
	// FilterViewReasoningBlind — filter sees user messages + tool calls only,
	// never assistant reasoning or internal scratchpad content.
	FilterViewReasoningBlind FilterView = "reasoning_blind"
)

// reasoningBlindStripKeys are the top-level map keys removed when applying
// the reasoning-blind view. This is a best-effort defence-in-depth measure;
// the specific keys will be tuned as safety classifiers are built.
var reasoningBlindStripKeys = []string{
	"assistant_content",
	"thinking",
	"reasoning",
}

// stripForView returns a (possibly filtered) copy of data appropriate for the
// given view. For FilterViewFull the original data is returned unchanged. For
// FilterViewReasoningBlind, if data is a map[string]interface{} it is
// deep-copied with reasoning keys removed. Non-map data is returned as-is.
func stripForView(data interface{}, view FilterView) interface{} {
	if view == FilterViewFull {
		return data
	}

	m, ok := data.(map[string]interface{})
	if !ok {
		return data
	}

	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}

	if view == FilterViewReasoningBlind {
		for _, key := range reasoningBlindStripKeys {
			delete(out, key)
		}
	}

	return out
}

// FilterFunc transforms data through a synchronous pipeline. Each handler
// receives the output of the previous handler. Return an error to abort the
// chain; the error propagates to the caller of ApplyFilter.
type FilterFunc func(data interface{}, ctx FilterContext) (interface{}, error)

// FilterContext provides metadata to filter handlers about the current
// operation being filtered.
type FilterContext struct {
	SessionID string
	AgentID   string
	Metadata  map[string]interface{}
}

// filterEntry binds a filter handler to a named filter point with a priority.
// Lower priority values execute earlier in the chain.
type filterEntry struct {
	PluginID string
	Priority int
	View     FilterView
	Fn       FilterFunc
}

// FilterRegistry manages named filter chains. Each filter name (e.g.
// "system_prompt", "tool_result") has its own ordered chain of handlers.
// Thread-safe for concurrent registration and application.
type FilterRegistry struct {
	mu     sync.RWMutex
	chains map[string][]filterEntry
}

// NewFilterRegistry creates an empty filter registry.
func NewFilterRegistry() *FilterRegistry {
	return &FilterRegistry{
		chains: make(map[string][]filterEntry),
	}
}

// Register adds a filter handler to the named chain at the given priority.
// Lower priority values execute earlier. If the same pluginID registers
// multiple handlers on the same chain, all are kept (ordered by priority).
// The handler uses FilterViewFull (sees all data).
func (r *FilterRegistry) Register(name, pluginID string, priority int, fn FilterFunc) error {
	return r.RegisterWithView(name, pluginID, priority, FilterViewFull, fn)
}

// RegisterWithView adds a filter handler with an explicit view. The view
// controls what subset of data the handler sees when the chain is applied.
func (r *FilterRegistry) RegisterWithView(name, pluginID string, priority int, view FilterView, fn FilterFunc) error {
	if name == "" {
		return fmt.Errorf("filter name must not be empty")
	}
	if fn == nil {
		return fmt.Errorf("filter function must not be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry := filterEntry{PluginID: pluginID, Priority: priority, View: view, Fn: fn}
	r.chains[name] = append(r.chains[name], entry)

	// Re-sort by priority (stable so equal-priority entries keep insertion order).
	sort.SliceStable(r.chains[name], func(i, j int) bool {
		return r.chains[name][i].Priority < r.chains[name][j].Priority
	})

	return nil
}

// Apply runs all handlers registered for the named filter in priority order.
// Each handler receives the output of the previous one. If no handlers are
// registered, data is returned unchanged (passthrough). If any handler returns
// an error, the chain aborts and the error is returned.
func (r *FilterRegistry) Apply(name string, data interface{}, ctx FilterContext) (interface{}, error) {
	r.mu.RLock()
	chain, exists := r.chains[name]
	if !exists || len(chain) == 0 {
		r.mu.RUnlock()
		return data, nil
	}
	// Copy the slice so we don't hold the lock during execution.
	handlers := make([]filterEntry, len(chain))
	copy(handlers, chain)
	r.mu.RUnlock()

	current := data
	for _, entry := range handlers {
		// Apply the view: strip data the handler should not see.
		visible := stripForView(current, entry.View)
		result, err := entry.Fn(visible, ctx)
		if err != nil {
			return nil, fmt.Errorf("filter %q (plugin %q, priority %d): %w",
				name, entry.PluginID, entry.Priority, err)
		}
		current = result
	}
	return current, nil
}

// Len returns the number of handlers registered for the named filter.
func (r *FilterRegistry) Len(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chains[name])
}

// RemoveByPlugin removes all filter entries registered by the given plugin ID
// across all chains. Returns the total number of entries removed.
func (r *FilterRegistry) RemoveByPlugin(pluginID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	for name, chain := range r.chains {
		filtered := chain[:0]
		for _, entry := range chain {
			if entry.PluginID == pluginID {
				removed++
			} else {
				filtered = append(filtered, entry)
			}
		}
		r.chains[name] = filtered
	}
	return removed
}

// Standard filter point names. Plugins reference these when registering filters.
const (
	FilterSystemPrompt     = "system_prompt"      // string → string
	FilterUserMessage      = "user_message"       // string → string
	FilterToolResult       = "tool_result"         // string → string
	FilterAssistantResponse = "assistant_response" // string → string
	FilterContextWindow    = "context_window"      // []provider.ChatMessage → []provider.ChatMessage
	FilterEnvelopeData     = "envelope_data"       // map[string]interface{} → map[string]interface{}
)
