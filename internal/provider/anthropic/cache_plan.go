package anthropic

import (
	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
)

// maxCacheControlMarkers is Anthropic's per-request cap on cache_control
// blocks. Exceeding it returns HTTP 400 "A maximum of 4 blocks with
// cache_control may be provided. Found N".
const maxCacheControlMarkers = 4

// cachePlan places cache markers only at the stable context prefix.
type cachePlan struct {
	System         bool
	Tools          bool
	SlotMarkers    []string
	RecentMessages int
}

func planCacheMarkersWithHints(req llmtypes.ChatRequest, hints []llmcontracts.CacheHint) cachePlan {
	if !hasCacheHintIn(hints, "system") {
		return cachePlan{}
	}
	return cachePlan{System: true, SlotMarkers: collectStablePrefixSlots(req.SlotBlocks)}
}

// collectStablePrefixSlots stops at the first dynamic or changed slot.
func collectStablePrefixSlots(blocks []llmtypes.SlotBlock) []string {
	if len(blocks) == 0 {
		return nil
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
		if s.Name != ctxpkg.SlotUniversal && s.Name != ctxpkg.SlotSystem {
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
