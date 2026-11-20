package anthropic

import (
	"fmt"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
)

// maxCacheControlMarkers is Anthropic's per-request cap on cache_control
// blocks. Exceeding it returns HTTP 400 "A maximum of 4 blocks with
// cache_control may be provided. Found N".
const maxCacheControlMarkers = 4

// cachePlan assigns at most four cache markers across system context, tools,
// and recent user messages. Slot markers only cover the unchanged prefix
// formed by the universal and system slots.
type cachePlan struct {
	System              bool
	Tools               bool
	SlotMarkers         []string
	RecentMessages      int
	SystemTTL, ToolsTTL string
	RecentTTLs          map[int]string
}

var stablePrefixSlotPriority = []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem}

// planCacheMarkers reads hints configured through SetCacheHints.
func (c *Client) planCacheMarkers(req llmtypes.ChatRequest) cachePlan {
	return planCacheMarkersWithHints(req, c.cacheHints)
}

// planCacheMarkersWithHints is the race-free variant: hints come in
// explicitly per call rather than being read from c.cacheHints. Identical
// budget enforcement and priority order as planCacheMarkers — see that
// doc for the contract.
func planCacheMarkersWithHints(req llmtypes.ChatRequest, hints []llmcontracts.CacheHint) cachePlan {
	plan := cachePlan{
		System:         hasCacheHintIn(hints, "system"),
		Tools:          hasCacheHintIn(hints, "tools"),
		RecentMessages: recentMessageCacheCountIn(hints),
	}
	for _, hint := range hints {
		switch hint.Position {
		case "system":
			plan.SystemTTL = hint.TTL
		case "tools":
			plan.ToolsTTL = hint.TTL
		case "recent_message":
			if hint.TTL != "" {
				if plan.RecentTTLs == nil {
					plan.RecentTTLs = make(map[int]string)
				}
				plan.RecentTTLs[hint.Index] = hint.TTL
			}
		}
	}
	// Slot markers require the "system" hint to be active — the slot
	// section is part of the system context; marking the slot boundary
	// without a system marker would be a disconnected cache fragment.
	if plan.System {
		plan.SlotMarkers = collectStablePrefixSlots(req.SlotBlocks)
	}

	// Tools only consume a wire-level cache marker if tools are configured on the request.
	toolsTakesMarker := plan.Tools && len(req.Tools) > 0

	// Static prefix budget: cap static markers at 2 so that up to 2 markers
	// remain available for RecentMessages (Anthropic 4-marker total cap).
	//
	// When both SlotMarkers and Tools are present, the last Tool marker
	// already caches everything preceding it (including SlotSystem, SlotAgent,
	// SlotRules, etc.). Preserving SlotUniversal (anchor 0) and Tools gives 2
	// static markers; dropping SlotSystem frees a marker for RecentMessages.
	if toolsTakesMarker && len(plan.SlotMarkers) > 1 && plan.RecentMessages > 0 {
		plan.SlotMarkers = plan.SlotMarkers[:1]
	}

	// Calculate static marker count on wire:
	// - System prompt: 1 marker (either from plan.SlotMarkers, or plan.System if no slot markers emit)
	// - Tools: 1 marker (if toolsTakesMarker)
	staticCount := len(plan.SlotMarkers)
	if staticCount == 0 && plan.System {
		staticCount = 1
	}
	if toolsTakesMarker {
		staticCount++
	}

	total := plan.RecentMessages + staticCount

	// Drop extra recent messages beyond cap if still over budget (e.g. > 2 requested):
	for total > maxCacheControlMarkers && plan.RecentMessages > 0 {
		plan.RecentMessages--
		total--
	}
	// Drop slot markers from TAIL if still over budget (SlotUniversal is last to go):
	for total > maxCacheControlMarkers && len(plan.SlotMarkers) > 0 {
		plan.SlotMarkers = plan.SlotMarkers[:len(plan.SlotMarkers)-1]
		total--
	}

	plan.normalizeTTLs(req)
	return plan
}

// normalizeTTLs follows Anthropic's logical cache order: tools, system, then
// messages from oldest to newest. If custom hints put a longer TTL after a
// shorter one, fall back to five minutes rather than extend cache billing.
// Omitted TTLs stay omitted; they already mean five minutes.
func (p *cachePlan) normalizeTTLs(req llmtypes.ChatRequest) {
	short, invalid := false, false
	visit := func(ttl string) {
		if ttl == "1h" {
			invalid = invalid || short
		} else {
			short = true
		}
	}
	if p.Tools && len(req.Tools) > 0 {
		visit(p.ToolsTTL)
	}
	if p.System {
		visit(p.SystemTTL)
	}
	count := 0
	for _, m := range req.Messages {
		if m.Role == "user" {
			count++
		}
	}
	if count > p.RecentMessages {
		count = p.RecentMessages
	}
	for i := count - 1; i >= 0; i-- {
		visit(p.RecentTTLs[i])
	}
	if !invalid {
		return
	}
	if p.ToolsTTL == "1h" {
		p.ToolsTTL = "5m"
	}
	if p.SystemTTL == "1h" {
		p.SystemTTL = "5m"
	}
	for i, ttl := range p.RecentTTLs {
		if ttl == "1h" {
			p.RecentTTLs[i] = "5m"
		}
	}
}

// collectStablePrefixSlots walks SlotBlocks in order and returns the
// names of slots eligible for cache markers. A slot is eligible when its
// name appears in stablePrefixSlotPriority (today exactly [SlotUniversal,
// SlotSystem]) AND it is unchanged AND its content is non-empty. The
// walk STOPS at the first slot whose name is NOT in the priority list
// and at the first Changed slot — the priority list exhaustively defines
// the cacheable prefix; there is no contiguous-walk past it. Empty-
// content slots are SKIPPED (not breaking) since they never make the
// wire either.
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
			// the wire either), but
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

// validateCacheTTLs fails before credentials or network access. The hints
// are per request, so concurrently used clients do not share TTL state.
func (c *Client) validateCacheTTLs(req llmtypes.ChatRequest) error {
	for _, hint := range c.effectiveCacheHints(req) {
		if err := hint.ValidateTTL(); err != nil {
			return fmt.Errorf("cache hint %s TTL %q: %w", hint.Position, hint.TTL, err)
		}
	}
	return nil
}
