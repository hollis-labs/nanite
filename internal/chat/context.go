package chat

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// renderCompactionDisclosure returns the CompactionContract disclosure block
// for the given session, or "" if no disclosure should be injected.
//
// **Freshness invariant** (P8A, CW-20260420-0025): a compaction event is
// "fresh" when its created_at is greater than or equal to the most recent
// assistant message's created_at for the same session. After the LLM completes
// a turn following compaction, the new assistant message's timestamp surpasses
// the event's, the event stops being fresh, and the disclosure no longer
// injects.
//
// Rationale for choosing this invariant over an explicit
// `disclosure_acknowledged` column on compaction_events:
//   - No additional migration / column needed.
//   - The signal is already in the data: the model produced an assistant turn
//     post-compaction, so it has implicitly seen and acted on the disclosure.
//   - "No assistant turn since the event" is a simple monotonic comparison —
//     no risk of a forgotten flag-flip drifting the system into permanent
//     disclosure injection.
//
// Edge case: if a session has zero assistant messages yet (e.g., compaction
// fired during a long pre-amble / first turn), the disclosure injects on
// every turn until the first assistant reply lands. Acceptable for v1 — the
// pre-first-assistant case is exotic enough not to warrant a special branch.
//
// Returns "" on any failure path (store error, no event); the caller treats
// "" as "no disclosure to inject".
func renderCompactionDisclosure(s *store.Store, sessionID string) string {
	ctx := context.Background()
	evt, err := s.GetLatestCompactionEvent(ctx, sessionID)
	if err != nil {
		slog.Warn("chat: GetLatestCompactionEvent failed during disclosure check",
			"err", err, "session_id", sessionID)
		return ""
	}
	if evt == nil {
		return ""
	}
	if !isCompactionEventFresh(s, sessionID, evt.CreatedAt) {
		return ""
	}

	return interpolateDisclosure(evt)
}

// isCompactionEventFresh returns true when no assistant message in the session
// has a created_at >= the event's created_at. See renderCompactionDisclosure
// for the freshness rationale.
func isCompactionEventFresh(s *store.Store, sessionID, eventCreatedAt string) bool {
	if eventCreatedAt == "" {
		return false
	}
	// 200 is a generous limit — assistant messages are sparse relative to
	// the limit, and we only need the timestamp of the latest one.
	msgs, err := s.ListMessages(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID, 200)
	if err != nil {
		slog.Warn("chat: ListMessages failed during disclosure freshness check",
			"err", err, "session_id", sessionID)
		return false
	}
	// Scan from newest to oldest; first assistant message wins.
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			// Fresh iff event was created at-or-after the latest assistant turn.
			return eventCreatedAt > msgs[i].CreatedAt
		}
	}
	// No assistant message yet — the event is fresh.
	return true
}

// compactionDisclosureTemplate is the single, universal, hardcoded
// compaction-disclosure message (Phase 0 item 29, TASKS.md Phase 0 Cuts).
//
// Relocated from the DB-backed prompt_templates mechanism (four
// mode-branched variants — general/code/plan/research — seeded by migration
// 030 and selected via disclosureSlugForMode/CompactionMode) into a single
// hardcoded message here. The four originals shared ~90% identical
// structure (Compaction Notice → Preserved/Lost → Recovery: handoff stash
// id, chat_search, summary metadata) and differed mainly in which nouns got
// preserved/lost per domain; collapsing them keeps the substance (what's
// preserved vs. lost, the two concrete recovery actions) without a mode
// lookup. This is independent of 21-cut-modes — classifyModeFromAgentTags /
// CompactionPipeline.Mode / summarySystemPrompt are untouched and continue
// to select the *summarizer's* system prompt; only the disclosure-template
// selection is collapsed here.
//
// %s verbs (in order): handoff stash id, coverage window start, coverage
// window end. %d verbs (in order): summary token count, evicted cache
// pointer count, preserved source count.
const compactionDisclosureTemplate = `## Compaction Notice

This conversation was compacted just before your turn. An LLM-generated summary replaced the older messages. Treat it as a lossy paraphrase, not a transcript.

**Preserved:** decisions, problems, findings, and outcomes — file paths, symbols, and ticket/ID references the summarizer flagged as load-bearing.
**Lost:** raw text of older turns, full tool inputs/outputs, exact numbers/quotes, verbatim diffs or source excerpts.

**Recovery:**
- **Handoff stash id:** %s — if set, holds decisions, open questions, file refs, and ticket IDs from pre-compaction. Read before answering about earlier-session state.
- **chat_search** {query, scope?, limit?} — search pre-compaction turns for specifics the summary omits.
- **Summary metadata:** window %s → %s, ≈ %d tokens, %d cache pointer(s) evicted, %d preserved source(s).

If the summary is silent on prior detail, search rather than guess.`

// interpolateDisclosure renders compactionDisclosureTemplate against the
// latest compaction_events row. Nullable fields (handoff_stash_id,
// coverage_window_*) render as "(none)" / "(unknown)" so the LLM gets a
// literal placeholder rather than an empty region that might read as "the
// value was lost".
func interpolateDisclosure(evt *store.CompactionEvent) string {
	stashID := "(none)"
	if evt.HandoffStashID != nil && *evt.HandoffStashID != "" {
		stashID = *evt.HandoffStashID
	}
	startTurn := "(unknown)"
	if evt.CoverageWindowStart != nil && *evt.CoverageWindowStart != "" {
		startTurn = *evt.CoverageWindowStart
	}
	endTurn := "(unknown)"
	if evt.CoverageWindowEnd != nil && *evt.CoverageWindowEnd != "" {
		endTurn = *evt.CoverageWindowEnd
	}

	return fmt.Sprintf(compactionDisclosureTemplate,
		stashID, startTurn, endTurn,
		evt.SummaryTokenCount, len(evt.EvictedCachePointers), len(evt.PreservedSources))
}

// thinkToolBlock is the v0 think-tool instruction (baseline for eval A/B).
// Active when NANITE_THINK_BLOCK_V1 is unset or "false".
const thinkToolBlock = `

## Think Tool
Use the think tool to organize your reasoning before acting:
- When new information changes your approach, think through the implications first.
- Before complex multi-step tool sequences, plan the steps.
- When checking completeness against requirements, verify coverage.`

// thinkToolBlockV1 is the richer v1 hint block (CW-20260420-0021).
// Active when NANITE_THINK_BLOCK_V1=true.
//
// Token budget: ≤ 200 tokens. Measured via EstimateTokens (chars/4).
// All four affordances must appear: scratchpad, memory, playbooks, peer-query.
//
// TODO(F5/CW-20260420-0022): Replace this static list with dynamic hint
// selection once the playbook runtime (CW-20260419-0027) and PeerQuery
// dispatch land. F5 scores the reflex catalog (internal/agent/reflexes'
// DB-backed agent_reflexes rows — the retired internal/promptrouter
// package's in-memory BuiltinReflexes() this comment used to name was
// migrated onto that table by TASKS/phase-4/
// 03-migrate-promptrouter-to-reflexes.md) against session context and
// injects only the top-N affordance hints.
const thinkToolBlockV1 = `

## Before Responding — Consider Your Affordances
Use the think tool to plan before multi-step tool sequences or when new context changes your approach.

- **Scratchpad** (scratchpad_write/read): stash interim values within a turn; avoid re-fetching.
- **Memory** (memory_recall/write): recall durable facts before research or planning; save conclusions.
- **Playbooks** (reflex catalog): reach for a pre-defined pattern (researcher, planner, reviewer, worker) before improvising.
- **Peer-query** (forthcoming — F5/CW-20260420-0022): agent-to-agent consultation not yet wired; use memory/scratchpad to share state.`

// IsThinkBlockV1Enabled returns true when NANITE_THINK_BLOCK_V1=true is set
// in the environment. Default is ON (v1 active); opt back to v0 by setting
// NANITE_THINK_BLOCK_V1=false for eval comparison against the baseline.
func IsThinkBlockV1Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_THINK_BLOCK_V1")))
	// Explicit opt-out: "false", "0", "no" → v0.
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	// Default ON — any other value (including empty) activates v1.
	return true
}

// ThinkToolBlock returns the active think-tool block depending on the feature
// flag. v1 (richer hint list) is the default; v0 is the eval baseline.
//
// To enable v2 dynamic selection pass a HintDispatcher via
// ThinkToolBlockWithDispatch — this zero-arg form cannot dispatch and always
// returns v0 or v1.
func ThinkToolBlock() string {
	if IsThinkBlockV1Enabled() {
		return thinkToolBlockV1
	}
	return thinkToolBlock
}

// ThinkToolBlockWithDispatch returns the active think-tool block, upgrading to
// v2 dynamic selection when NANITE_THINK_BLOCK_V2_ENABLED=true and dispatcher
// is non-nil. Falls back to ThinkToolBlock() (v0/v1) otherwise.
//
// Parameters mirror ThinkToolBlockDynamic — see hint_dispatch.go.
func ThinkToolBlockWithDispatch(ctx context.Context, dispatcher HintDispatcher, userInput, scopeTier, reflexMatchID string) string {
	if !IsThinkBlockV1Enabled() {
		// V1 flag is the gate: if v1 is off, bypass both v1 and v2.
		return thinkToolBlock
	}
	if IsThinkBlockV2Enabled() && dispatcher != nil {
		return ThinkToolBlockDynamic(ctx, dispatcher, userInput, scopeTier, reflexMatchID)
	}
	return thinkToolBlockV1
}

// SkillEssentialCap is the soft ceiling for inline-rendered assigned skills.
// Glass-5 (CW-20260502-0012): when an agent has more than this many mode-passing
// assigned skills, the overflow is folded into the discoverability LoadHint
// rather than ballooning the Agent slot. 25 is a pragmatic placeholder — large
// enough that no real-world agent's curated set hits the cap today, small
// enough to keep init-time tokens bounded as agents accumulate skills.
// Tune off Glass-2 telemetry once data accumulates.
//
// Phase 0 item 22 (decision log §11): this used to default to the Skill
// Broker's MaxSelectedSkills constant (internal/skillbroker, now retired —
// "no separate ranking abstraction"). Kept as a plain local constant with
// the same value so the cap is unchanged.
const SkillEssentialCap = 25

// buildSkillListForSession is the skill-list renderer for an agent's
// assigned skills.
//
// Phase 0 item 22 (decision log §11): this used to run assigned skills
// through the Skill Broker (internal/skillbroker.SelectSkills) — a
// keyword/agent-tag/mode-bonus ranking pass. The broker was functionally
// inert in every real environment (zero rows workspace-wide in the old
// per-agent skill assignment join table, per the decision log), so it's
// retired in favor of a direct cap: s.ListAgentSkills already returns rows
// ordered by name (`ORDER BY sk.name`), which gives a stable, deterministic
// "first SkillEssentialCap" selection with no scoring heuristic to
// maintain. TASKS/skills/02: that old join table is dropped outright and
// ListAgentSkills is rewired onto agent_known_skills — see
// internal/store/skills.go's doc comment.
//
// Glass-5 (CW-20260502-0012): the rendered list is partitioned into
// "essentials" (the first SkillEssentialCap assigned skills) and
// "discoverable" (everything else in the catalog). Essentials are inlined;
// discoverable count is surfaced via a LoadHint pointer at the tail of the
// rendered string. The pointer references real MCP tools (skill_list,
// tool_list) and is framed as invitation, not warning — the agent should
// feel the catalog has every skill it needs and only carries what it
// currently uses.
func buildSkillListForSession(_ context.Context, s *store.Store, agentID, _ string) string {
	skills, err := s.ListAgentSkills(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, agentID)
	if err != nil {
		slog.Warn("chat: failed to load agent skills", "err", err)
		return ""
	}

	// Phase 0 item 21 ("Cut Modes, in full") deleted the E2 mode filter
	// (filterAgentSkillsByMode) that used to run here — it resolved
	// s.GetSessionMode(sessionID), which no longer exists. There is no
	// more session-scoped mode to gate skills on; every agent skill is a
	// candidate now.
	rendered := skills
	if len(rendered) > SkillEssentialCap {
		rendered = rendered[:SkillEssentialCap]
	}

	// TASKS/skills/02: store.Skill.ToolBindings is dropped along with the
	// index-only redesign (docs/engineering/architecture/20-skills.md's
	// "The model") — an index row no longer carries a tool-binding list to
	// render inline, so this loop is back to plain name/description.
	var sb strings.Builder
	for _, sk := range rendered {
		fmt.Fprintf(&sb, "- %s: %s\n", sk.Name, sk.Description)
	}

	if hint := skillCatalogLoadHint(s, len(rendered)); hint != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(hint)
		sb.WriteString("\n")
	}

	return sb.String()
}

// skillCatalogLoadHint returns the discoverability pointer text appended
// after the inline essentials. Returns "" when the catalog has nothing
// beyond what was rendered (no point hinting at zero discoverable skills).
// Glass-5 (CW-20260502-0012).
func skillCatalogLoadHint(s *store.Store, renderedCount int) string {
	catalog, err := s.ListSkills(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
	if err != nil {
		slog.Debug("chat: skillCatalogLoadHint ListSkills failed", "err", err)
		return ""
	}
	discoverable := len(catalog) - renderedCount
	if discoverable <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"[%d additional skills are available in your catalog. Browse via `skill_list(category:\"<term>\")` or `tool_list(filter:\"<term>\")` for the full tool surface — we have skills for nearly any task. If your first lookup misses, widen the search before concluding nothing matches.]",
		discoverable,
	)
}
