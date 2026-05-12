package anthropic

import (
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// maxCacheControlMarkers is Anthropic's per-request cap on cache_control
// blocks. Exceeding it returns HTTP 400 "A maximum of 4 blocks with
// cache_control may be provided. Found N".
const maxCacheControlMarkers = 4

// cachePlan describes which cache_control markers to emit on a single
// request, post-budget-enforcement. Each builder consults this plan
// instead of checking hints directly so the cap is enforced centrally.
//
// CW-20260512-0109 (W3, SP-20260512-0008): cache marker placement is
// codified by priority. The leading stable prefix — SlotUniversal at
// position 0, then SlotSystem at position 1, then any further contiguous
// unchanged slots up to budget — receives explicit per-slot markers.
// Dynamic per-turn content (e.g. tool descriptions emitted by the
// describer registry, see internal/describer; selected workspace slots
// chosen per-intent) is NEVER marked.
type cachePlan struct {
	System         bool
	Tools          bool
	SlotMarkers    []string // slot names (in placement order) to mark with cache_control
	RecentMessages int      // number of trailing user messages to mark
}

// stablePrefixSlotPriority codifies the slot-marker priority order for
// the cacheable prefix. SlotUniversal is position 0; SlotSystem is the
// second tier. Subsequent slots in the prefix run are eligible in the
// order they appear in ctxpkg.SlotOrder — see planCacheMarkers for the
// contiguous-prefix walk.
//
// Per CW-20260512-0109: only slot positions whose content is structurally
// stable across turns belong here. Slots that the broker may swap
// (SlotContext, SlotMemory, SlotMode, SlotSession) are NOT in this list
// — they may still ride inside the cached prefix when unchanged, but a
// marker is never planted on them because their per-turn changeover
// would otherwise invalidate the marker offset.
//
// Tool descriptions emitted by the per-call describer registry (W1B,
// CW-20260512-0105) carry per-caller dynamic text. The describer hook
// fires AFTER slot assembly on the final tool array, so its dynamism is
// confined to the tools section. This planner therefore never marks
// individual tools by description; the Tools-section marker (when
// emitted) lands on the LAST tool, which is a stable position by
// construction. Adopters of the describe hook should be placed at the
// tool array TAIL so the cacheable head (static-description tools) is
// not invalidated per call — that constraint is documented in
// internal/describer/describer.go.
var stablePrefixSlotPriority = []string{
	ctxpkg.SlotUniversal,
	ctxpkg.SlotSystem,
}

// planCacheMarkers computes the budget-enforced cache plan for a request.
// Priority order under pressure: system > tools > slot-markers (first
// dropped from the TAIL of the stable prefix run) > recent_message.
// RecentMessages drops first; slot-markers drop second (tail-first so
// the SlotUniversal anchor is the last to go). System + Tools are never
// dropped — if both are requested they fit within the cap even with no
// other markers.
//
// SlotMarkers is populated by walking req.SlotBlocks in order, starting
// from position 0, and collecting consecutive unchanged non-empty slots
// whose names are in stablePrefixSlotPriority. The walk STOPS at the
// first Changed slot or the first non-priority slot — markers are never
// placed past a dynamic break, even if a later slot is unchanged.
//
// Empty plan.SlotMarkers means no slot-section marker emits this turn —
// either because no stable prefix slots are present, the "system" hint
// is absent (slot section is anchored to the system context), or the
// 4-marker cap consumed every slot slot.
func (c *Client) planCacheMarkers(req llmtypes.ChatRequest) cachePlan {
	plan := cachePlan{
		System:         c.hasCacheHint("system"),
		Tools:          c.hasCacheHint("tools"),
		RecentMessages: c.recentMessageCacheCount(),
	}
	// Slot markers require the "system" hint to be active — the slot
	// section is part of the system context; marking the slot boundary
	// without a system marker would be a disconnected cache fragment.
	if plan.System {
		plan.SlotMarkers = collectStablePrefixSlots(req.SlotBlocks)
	}
	// Enforce cap by dropping in reverse priority.
	total := plan.RecentMessages
	if plan.System {
		total++
	}
	if plan.Tools {
		total++
	}
	total += len(plan.SlotMarkers)
	for total > maxCacheControlMarkers && plan.RecentMessages > 0 {
		plan.RecentMessages--
		total--
	}
	// Drop slot markers from the TAIL — SlotUniversal (position 0) is
	// the last marker to go, preserving the Universal-first priority.
	for total > maxCacheControlMarkers && len(plan.SlotMarkers) > 0 {
		plan.SlotMarkers = plan.SlotMarkers[:len(plan.SlotMarkers)-1]
		total--
	}
	// System + Tools alone is <= 2, never need to drop those.
	return plan
}

// collectStablePrefixSlots walks SlotBlocks in order and returns the
// names of consecutive unchanged non-empty slots in the leading prefix
// that are also in stablePrefixSlotPriority. The walk STOPS at the
// first non-priority slot or the first Changed/empty slot — markers are
// never planted past a dynamic break.
//
// Returns slot names in placement order (matches req.SlotBlocks order),
// which is also the order in which buildSystemBlocks will plant the
// markers.
func collectStablePrefixSlots(blocks []llmtypes.SlotBlock) []string {
	if len(blocks) == 0 {
		return nil
	}
	priority := make(map[string]bool, len(stablePrefixSlotPriority))
	for _, name := range stablePrefixSlotPriority {
		priority[name] = true
	}
	var markers []string
	for _, s := range blocks {
		if s.Content == "" {
			// Empty slot — does NOT break the prefix run (it never makes
			// the wire either, see slotBlocksFor in chat_generate.go), but
			// also not eligible for a marker.
			continue
		}
		if s.Changed {
			// Dynamic slot breaks the prefix run.
			break
		}
		if !priority[s.Name] {
			// Not a stable-prefix slot — break the run rather than
			// silently skipping past it. A non-priority unchanged slot
			// in the middle of the prefix means we don't know whether
			// its content is structurally stable across turns; refusing
			// to mark past it preserves the determinism contract.
			break
		}
		markers = append(markers, s.Name)
	}
	return markers
}
