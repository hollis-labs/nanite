package chat

import (
	"fmt"
	"os"
	"sort"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// G-HOT-SWAP-DEAD activation: per-session partition that ships only an
// "essential" subset of an agent's tool universe inline, surfacing the
// remainder via a LoadHint pointer the agent reaches for via request_tools.
//
// Layered above the S3b tool-cache pipeline (internal/service/context.go):
// PartitionTools picks the tool universe; S3b's intent classifier still
// decides per-turn whether the essential subset is pointer-only / partial
// / fully hydrated. The two are independent levers: PartitionTools bounds
// what's *eligible* for inline rendering, S3b modulates *how* it's rendered
// per turn.

// ToolEssentialCap mirrors SkillEssentialCap (chat/context.go). When the
// candidate-essential set for a session exceeds this many tools, overflow
// is folded into the lazy-load pointer rather than ballooning the Tools
// slot. Tunable in step with SkillEssentialCap; matched at 25 by default
// per the implementer-prompt locked decision.
const ToolEssentialCap = 25

// RecentToolWindow is the trailing-assistant-message window scanned for
// tool_use blocks when computing the recently-used signal. Three turns
// is short enough to react to topic shifts without thrashing the cap.
const RecentToolWindow = 3

// ToolHysteresisFloor is the minimum number of consecutive turns a tool
// stays in the essential set after first promotion. Without hysteresis,
// any tool that briefly tapers in usage falls back into lazy and bounces
// the Tools-slot CacheKey on the next invocation. Five turns keeps the
// cache stable across normal usage rhythm.
const ToolHysteresisFloor = 5

// ToolPartitionState carries hysteresis across turns within a session.
// The caller (typically the chat service) owns one map per session and
// passes the previous state into PartitionTools, receiving an updated
// state to store for the next turn.
type ToolPartitionState struct {
	// Turn is the monotonic turn counter for this session. Increments by
	// 1 each time PartitionTools runs.
	Turn int
	// PromotedAt records, for each tool name currently essential, the
	// turn at which it was first promoted. Hysteresis pins a tool until
	// Turn - PromotedAt[name] >= ToolHysteresisFloor, after which it can
	// fall back to lazy if no rule re-promotes it.
	PromotedAt map[string]int
}

// ToolPartition is the result of PartitionTools.
type ToolPartition struct {
	// Essential is the subset of input tools shipped inline as full
	// schemas. Always preserves the input order so callers can replace
	// `tools = essential` without disrupting downstream ordering
	// invariants.
	Essential []llmtypes.ToolDefinition
	// Lazy is the subset announced via the LoadHint pointer. The agent
	// reaches for them via request_tools.
	Lazy []llmtypes.ToolDefinition
}

// PartitionTools splits an agent's tool universe into Essential and Lazy
// sets per the G-HOT-SWAP-DEAD locked decisions:
//
//  1. Recently-used: invoked in the last RecentToolWindow assistant turns.
//  2. Hysteresis: pinned for ToolHysteresisFloor turns post-promotion.
//  3. Filler: remaining input tools, in input order.
//
// All input tools that don't make the cut after the cap is applied move
// into Lazy. Meta-tools (request_tools, fetch_tool_result, …) are
// always essential — the agent's escape hatches must stay reachable.
//
// The cap is applied to Essential. When len(candidates) <= cap, every
// input tool is essential and Lazy is empty.
//
// recentNames must be the de-duplicated list returned by
// RecentlyUsedToolNames. prev is the session's prior partition state
// (zero-value PromotedAt map is acceptable for fresh sessions).
//
// Returns the partition plus the new state to persist for the next turn.
//
// Phase 0 item 21 ("Cut Modes, in full") removed this function's
// `modeSpec store.ToolOverrideSpec` parameter and the two highest-priority
// scoring tiers it drove (mode-explicit tool_overrides.allow names,
// mode-pattern tool_overrides.allow_patterns matches) — both were sourced
// exclusively from Session Mode's Mode.ToolOverrides, which is gone.
// Recently-used and hysteresis are unaffected; they never depended on mode.
func PartitionTools(
	tools []llmtypes.ToolDefinition,
	recentNames []string,
	prev ToolPartitionState,
	cap int,
) (ToolPartition, ToolPartitionState) {
	if cap <= 0 {
		cap = ToolEssentialCap
	}

	nextTurn := prev.Turn + 1

	// Empty input → empty output; carry turn forward but reset PromotedAt.
	if len(tools) == 0 {
		return ToolPartition{}, ToolPartitionState{Turn: nextTurn, PromotedAt: map[string]int{}}
	}

	recentSet := make(map[string]struct{}, len(recentNames))
	for _, n := range recentNames {
		recentSet[n] = struct{}{}
	}

	// Score each input tool. Higher score = stronger essential candidate.
	// The score determines trim order when overflow occurs.
	type scored struct {
		idx   int
		def   llmtypes.ToolDefinition
		score int
		// hyst indicates the tool is hysteresis-pinned (must stay essential
		// regardless of whether other rules re-elected it this turn).
		hyst bool
	}

	const (
		scoreMeta   = 1000
		scoreRecent = 300
		scoreHyst   = 100
		scoreFiller = 0
	)

	scoredTools := make([]scored, 0, len(tools))
	for i, t := range tools {
		s := scoreFiller
		hyst := false

		if isMetaToolName(t.Name) {
			// Meta-tools are non-negotiable: always essential, never lazy.
			s = scoreMeta
		} else {
			if _, ok := recentSet[t.Name]; ok {
				s = max(s, scoreRecent)
			}
			if prev.PromotedAt != nil {
				if first, ok := prev.PromotedAt[t.Name]; ok && (nextTurn-first) < ToolHysteresisFloor {
					if s < scoreHyst {
						s = scoreHyst
					}
					hyst = true
				}
			}
		}

		scoredTools = append(scoredTools, scored{idx: i, def: t, score: s, hyst: hyst})
	}

	// Sort by score desc, then by original index asc to keep deterministic
	// ordering (input order breaks ties — preserves locality / readability).
	sort.SliceStable(scoredTools, func(i, j int) bool {
		if scoredTools[i].score != scoredTools[j].score {
			return scoredTools[i].score > scoredTools[j].score
		}
		return scoredTools[i].idx < scoredTools[j].idx
	})

	// Top `cap` (or less when input is smaller) become essential. Hysteresis
	// pinning keeps a tool essential even if it would otherwise be dropped —
	// implemented by ensuring all hyst-pinned tools land in the cap window
	// before non-pinned, lower-scored tools are considered.
	essentialMask := make(map[int]bool, cap)
	count := 0

	// First pass: take everything scoring above scoreFiller, up to cap.
	for _, st := range scoredTools {
		if count >= cap {
			break
		}
		if st.score == scoreFiller {
			continue
		}
		essentialMask[st.idx] = true
		count++
	}

	// Second pass: fill remaining cap slots with filler-scored tools in
	// input order.
	if count < cap {
		for _, st := range scoredTools {
			if count >= cap {
				break
			}
			if st.score != scoreFiller {
				continue
			}
			essentialMask[st.idx] = true
			count++
		}
	}

	// Materialize Essential and Lazy in input order.
	essential := make([]llmtypes.ToolDefinition, 0, count)
	lazy := make([]llmtypes.ToolDefinition, 0, len(tools)-count)
	for i, t := range tools {
		if essentialMask[i] {
			essential = append(essential, t)
		} else {
			lazy = append(lazy, t)
		}
	}

	// Build the new PromotedAt: existing pin if still essential and within
	// floor, else current turn for newly-essential tools.
	newPromoted := make(map[string]int, len(essential))
	for _, t := range essential {
		if prev.PromotedAt != nil {
			if first, ok := prev.PromotedAt[t.Name]; ok {
				newPromoted[t.Name] = first
				continue
			}
		}
		newPromoted[t.Name] = nextTurn
	}

	return ToolPartition{Essential: essential, Lazy: lazy},
		ToolPartitionState{Turn: nextTurn, PromotedAt: newPromoted}
}

// RecentlyUsedToolNames scans the most recent assistant messages for
// tool_use blocks and returns up to one entry per distinct tool name in
// reverse-recency order (most recent first). msgs may be in any order;
// this function locates assistant messages via Role and walks them
// newest → oldest, capping at `n` assistant turns.
//
// Ignores user / tool_result messages. Returns empty when no tool_use
// blocks are found within the window.
func RecentlyUsedToolNames(msgs []llmtypes.ChatMessage, n int) []string {
	if n <= 0 || len(msgs) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	turns := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		turns++
		if turns > n {
			break
		}
		for _, blk := range msgs[i].ContentBlocks {
			if blk.Type != "tool_use" {
				continue
			}
			name := blk.Name
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

// RenderToolLazyHint produces the LoadHint string surfaced to the agent
// when len(lazy) > 0. Format is locked per the implementer-prompt:
//
//	Tool catalog (lazy): N tools available. Use `request_tools` with
//	{names: [...]} to retrieve specific tool schemas.
//	Available tools: <name1>, <name2>, ...
//
// Names are emitted in input order (which mirrors broker ranking) so
// the agent sees the most-relevant lazy tools first. No descriptions —
// schemas are paid for via request_tools.
func RenderToolLazyHint(lazy []llmtypes.ToolDefinition) string {
	if len(lazy) == 0 {
		return ""
	}
	names := make([]string, len(lazy))
	for i, t := range lazy {
		names[i] = t.Name
	}
	return fmt.Sprintf(
		"[Tool catalog (lazy): %d tools available. Use `request_tools` with {names:[...]} to retrieve specific tool schemas.\nAvailable tools: %s]",
		len(lazy),
		strings.Join(names, ", "),
	)
}

// IsToolsLazyLoadEnabled gates G-HOT-SWAP-DEAD activation. Default is ON
// per docs/architecture-decision-log-2026-08-17.md §12 and
// docs/engineering/architecture/04-harness.md ("Tool lazy-loading"): the
// mechanism is fully built and tested, so it's enabled by default and
// tuned based on real behavior rather than gated behind opt-in telemetry.
// Recognized explicit opt-outs — "false"/"0"/"no"/"off" (case-insensitive,
// trimmed) — still disable it. Any other non-empty value that isn't a
// recognized true/false token (e.g. a typo) falls back to the safe
// default of ON rather than being treated as an implicit disable.
func IsToolsLazyLoadEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_TOOLS_LAZY_LOAD")))
	switch v {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// isMetaToolName checks the meta-tool exemption list. Mirrors
// toolclient.MetaToolNames keys without depending on the toolclient
// package — the chat package can't import service-layer types and the
// list is stable.
func isMetaToolName(name string) bool {
	switch name {
	case "request_tools", "fetch_tool_result", "search_tool_result":
		return true
	}
	return false
}

