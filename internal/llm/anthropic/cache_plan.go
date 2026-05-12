package anthropic

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// maxCacheControlMarkers is Anthropic's per-request cap on cache_control
// blocks. Exceeding it returns HTTP 400 "A maximum of 4 blocks with
// cache_control may be provided. Found N".
const maxCacheControlMarkers = 4

// cachePlan describes which cache_control markers to emit on a single
// request, post-budget-enforcement. Each builder consults this plan
// instead of checking hints directly so the cap is enforced centrally.
type cachePlan struct {
	System         bool
	Tools          bool
	SlotBoundary   bool // mark last unchanged slot block (single marker)
	RecentMessages int  // number of trailing user messages to mark
}

// planCacheMarkers computes the budget-enforced cache plan for a request.
// Priority order under pressure: system > tools > slot-boundary >
// recent_message. RecentMessages drops first; SlotBoundary drops second.
// System + Tools are never dropped — if both are requested they fit
// within the cap even with no other markers.
func (c *Client) planCacheMarkers(req llmtypes.ChatRequest) cachePlan {
	plan := cachePlan{
		System:         c.hasCacheHint("system"),
		Tools:          c.hasCacheHint("tools"),
		RecentMessages: c.recentMessageCacheCount(),
	}
	// SlotBoundary requires at least one non-empty unchanged slot AND
	// a "system" hint (the slot section is part of the system context;
	// marking the slot boundary without a system marker would be a
	// disconnected cache fragment).
	if plan.System {
		for _, s := range req.SlotBlocks {
			if s.Content != "" && !s.Changed {
				plan.SlotBoundary = true
				break
			}
		}
	}
	// Enforce cap by dropping in reverse priority.
	total := plan.RecentMessages
	if plan.System {
		total++
	}
	if plan.Tools {
		total++
	}
	if plan.SlotBoundary {
		total++
	}
	for total > maxCacheControlMarkers && plan.RecentMessages > 0 {
		plan.RecentMessages--
		total--
	}
	if total > maxCacheControlMarkers && plan.SlotBoundary {
		plan.SlotBoundary = false
		total--
	}
	// System + Tools alone is <= 2, never need to drop those.
	return plan
}
