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
	// CompactAt is the root conversation's budget; a subtask keeps its own
	// window, which may be a different model's.
	if at := x.engine.cfg.CompactAt; at > 0 && x.info.ParentID == "" {
		window.TotalBudget = at
	}
	// Host context blocks are already in the request, so clearing either
	// slot in the window would free nothing real; the compaction pipeline
	// must not count on it.
	for _, name := range []string{ctxpkg.SlotContext, ctxpkg.SlotUserContext} {
		if s := window.Slot(name); s != nil {
			s.Compactable = false
		}
	}
	if len(x.specs) == 0 {
		return
	}
	raw, err := json.Marshal(x.specs)
	if err != nil {
		return
	}
	content := string(raw)
	x.schemaTokens = ctxpkg.DefaultEstimator{}.Estimate(content)
	if host := window.Slot(ctxpkg.SlotTools); host != nil && host.Content != "" {
		// Host-written tool notes are in the request's system blocks; count
		// both.
		content = host.Content + "\n" + content
	}
	window.SetContent(ctxpkg.SlotTools, content)
}

// calibrate scales the window's budget by how far the last request's size
// estimate fell short of what the provider counted, so the estimate-based
// checks (here and inside the compaction pipeline) see the request's real
// size. It returns a function that restores the budget.
func (x *execution) calibrate(window *ctxpkg.ContextWindow) func() {
	base := window.TotalBudget
	if x.lastRequestTokens > 0 && x.lastRequestEstimate > 0 && x.lastRequestTokens > x.lastRequestEstimate {
		window.TotalBudget = int(float64(base) * float64(x.lastRequestEstimate) / float64(x.lastRequestTokens))
	}
	return func() { window.TotalBudget = base }
}

// staticTokens is the estimated size of everything in a request except the
// conversation: slot blocks and tool schemas.
func staticTokens(window *ctxpkg.ContextWindow) int {
	total := 0
	for _, name := range ctxpkg.SlotOrder {
		if name != ctxpkg.SlotConversation {
			if s := window.Slot(name); s != nil {
				total += s.TokenCount
			}
		}
	}
	return total
}

// needsCompaction reports whether the conversation should be compacted:
// the (calibrated) estimate says the request no longer fits the budget.
// When the fixed part of the request alone fills the budget, compaction
// cannot help; it is skipped, with one warning per turn.
func (x *execution) needsCompaction(window *ctxpkg.ContextWindow) bool {
	if staticTokens(window) >= window.TotalBudget {
		if !x.warnedStatic {
			x.warnedStatic = true
			x.engine.cfg.Logger.Warn("nanite: slots and tool schemas alone fill the compaction budget; compaction cannot help. Offer fewer tools or raise the budget.",
				"conversation", x.info.ID, "static_tokens", staticTokens(window), "budget", window.TotalBudget)
		}
		return false
	}
	return window.NeedsCompaction()
}

// estimateRequest records the estimated size of the request about to be
// sent, to compare with what the provider then reports.
func (x *execution) estimateRequest() {
	window := x.assembled.Window
	if window == nil {
		return
	}
	raw, err := json.Marshal(x.messages)
	if err != nil {
		return
	}
	x.lastRequestEstimate = staticTokens(window) + ctxpkg.DefaultEstimator{}.Estimate(string(raw))
}
