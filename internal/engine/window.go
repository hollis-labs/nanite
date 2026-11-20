package engine

import (
	"encoding/json"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
)

// sizeWindow makes the context window account for everything a request
// carries, so compaction fires before the request overflows:
//   - the tool schemas offered this turn fill the Tools slot. They are sent
//     as the request's tools, not as a slot block, but they take the same room;
//   - CompactAt, when set, replaces the window's total budget.
//
// The slot blocks for the request were fixed at assembly; this changes only
// the window's accounting.
func (x *execution) sizeWindow() {
	window := x.assembled.Window
	if window == nil {
		return
	}
	if at := x.engine.cfg.CompactAt; at > 0 {
		window.TotalBudget = at
	}
	if len(x.specs) == 0 {
		return
	}
	raw, err := json.Marshal(x.specs)
	if err != nil {
		return
	}
	content := string(raw)
	if host := window.Slot(ctxpkg.SlotTools); host != nil && host.Content != "" {
		// Host-written tool notes are in the request's system blocks; count
		// both.
		content = host.Content + "\n" + content
	}
	window.SetContent(ctxpkg.SlotTools, content)
}

// needsCompaction reports whether the conversation should be compacted: the
// estimate says the request no longer fits the budget, or the provider's own
// count for the last request already exceeds it.
func (x *execution) needsCompaction(window *ctxpkg.ContextWindow) bool {
	return window.NeedsCompaction() || (x.lastRequestTokens > 0 && x.lastRequestTokens > window.TotalBudget)
}
