package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// assembleSystemPrompt builds the full system prompt from agent profile, mode, and workspace context.
// This is the legacy path used when no prompt templates are assigned.
func assembleSystemPrompt(agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) string {
	var b strings.Builder

	b.WriteString(agent.SystemPrompt)

	if mode != nil && mode.PromptAddendum != "" {
		b.WriteString("\n\n")
		b.WriteString(mode.PromptAddendum)
	}

	if workspace != nil {
		b.WriteString(fmt.Sprintf("\n\nWorkspace: %s", workspace.Name))
		if workspace.Description != "" {
			b.WriteString(" - ")
			b.WriteString(workspace.Description)
		}
	}

	prompt := b.String()
	slog.Debug("chat: assembled system prompt", "chars", len(prompt))
	return prompt
}

// assembleSystemPromptFromTemplates uses ComposePromptForAgent from the prompt template system.
// Falls back to the legacy assembleSystemPrompt if no templates are assigned.
//
// When sessionID is non-empty and the most recent compaction_events row for
// the session is "fresh" (created after the last assistant message), a
// CompactionContract disclosure is appended (P8A, CW-20260420-0025). The
// disclosure variant is selected by the event's summary_mode and rendered
// with metadata interpolated from the event row.
//
// hintOpts carries optional v2 hint-selection parameters (F5 / CW-20260420-0022).
// Pass nil to use the v0/v1 static ThinkToolBlock path.
func assembleSystemPromptFromTemplates(s *store.Store, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, skillList, sessionID string, hintOpts *HintSelectOpts) string {
	// Build variables map for template resolution.
	vars := map[string]string{
		"agent_name":        agent.Name,
		"agent_description": agent.Description,
	}

	if mode != nil {
		vars["mode_addendum"] = mode.PromptAddendum
	}

	if workspace != nil {
		vars["workspace_name"] = workspace.Name
		vars["workspace_description"] = workspace.Description
	}

	if skillList != "" {
		vars["skill_list"] = skillList
	}

	// Schema v2 template variables.
	if agent.Tools != "" && agent.Tools != "[]" {
		vars["tools_allowlist"] = agent.Tools
	}
	if agent.Tags != "" && agent.Tags != "[]" {
		vars["agent_tags"] = agent.Tags
	}

	composed, err := s.ComposePromptForAgent(agent.ID, vars)
	if err != nil {
		slog.Warn("chat: ComposePromptForAgent failed — falling back to legacy", "err", err)
		return assembleSystemPrompt(agent, mode, workspace)
	}

	if composed == "" {
		// No templates assigned — use legacy path.
		composed = assembleSystemPrompt(agent, mode, workspace)
	} else {
		slog.Debug("chat: assembled system prompt from templates", "chars", len(composed))
	}

	// P8A CompactionContract disclosure: append before think-tool block when
	// the session has a fresh compaction event (one not yet acknowledged by an
	// assistant turn). Best-effort — empty disclosure means no fresh event,
	// no template seeded, or a store error (logged inside the helper).
	if sessionID != "" {
		if disclosure := renderCompactionDisclosure(s, sessionID); disclosure != "" {
			composed += "\n\n" + disclosure
		}
	}

	// Append think tool guidance (v0 baseline, v1 static, or v2 dynamic per feature flags).
	// hintOpts carries the per-request context needed for v2 dispatch; nil falls back to v0/v1.
	if hintOpts != nil && IsThinkBlockV2Enabled() {
		composed += ThinkToolBlockWithDispatch(
			hintOpts.Ctx,
			hintOpts.Dispatcher,
			hintOpts.UserInput,
			hintOpts.ScopeTier,
			hintOpts.ReflexMatchID,
		)
	} else {
		composed += ThinkToolBlock()
	}

	return composed
}

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
// Returns "" on any failure path (store error, no event, no template,
// missing summary_mode); the caller treats "" as "no disclosure to inject".
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

	slug := disclosureSlugForMode(evt.SummaryMode)
	tmpl, err := s.GetPromptTemplateBySlug(slug)
	if err != nil || tmpl == nil {
		if err != nil {
			slog.Warn("chat: GetPromptTemplateBySlug failed for disclosure",
				"slug", slug, "err", err)
		}
		return ""
	}

	return interpolateDisclosure(tmpl.Template, evt)
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
	msgs, err := s.ListMessages(sessionID, 200)
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

// disclosureSlugForMode maps a CompactionMode to the seeded disclosure
// template slug. Unknown modes fall back to the general variant so the
// disclosure path stays robust to mode-string drift.
func disclosureSlugForMode(mode string) string {
	switch mode {
	case "code":
		return "compaction-disclosure-code"
	case "plan":
		return "compaction-disclosure-plan"
	case "research":
		return "compaction-disclosure-research"
	default:
		return "compaction-disclosure-general"
	}
}

// interpolateDisclosure fills the {{var}} placeholders in a disclosure
// template with values from the latest compaction_events row. Nullable
// fields (handoff_stash_id, coverage_window_*) render as "(none)" /
// "(unknown)" so the LLM gets a literal placeholder rather than an empty
// region that might read as "the value was lost".
func interpolateDisclosure(tmpl string, evt *store.CompactionEvent) string {
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

	out := tmpl
	out = strings.ReplaceAll(out, "{{handoff_stash_id}}", stashID)
	out = strings.ReplaceAll(out, "{{coverage_window_start}}", startTurn)
	out = strings.ReplaceAll(out, "{{coverage_window_end}}", endTurn)
	out = strings.ReplaceAll(out, "{{summary_token_count}}", strconv.Itoa(evt.SummaryTokenCount))
	out = strings.ReplaceAll(out, "{{evicted_pointer_count}}", strconv.Itoa(len(evt.EvictedCachePointers)))
	out = strings.ReplaceAll(out, "{{preserved_source_count}}", strconv.Itoa(len(evt.PreservedSources)))
	return out
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
// dispatch land. F5 scores BuiltinReflexes() against session context and
// injects only the top-N affordance hints.
const thinkToolBlockV1 = `

## Before Responding — Consider Your Affordances
Use the think tool to plan before multi-step tool sequences or when new context changes your approach.

- **Scratchpad** (nanite_scratchpad_write/read): stash interim values within a turn; avoid re-fetching.
- **Memory** (nanite_memory_recall/save): recall durable facts before research or planning; save conclusions.
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

// buildSkillList creates a human-readable list of skills for the tool-awareness template.
func buildSkillList(s *store.Store, agentID string) string {
	skills, err := s.ListAgentSkills(agentID)
	if err != nil {
		slog.Warn("chat: failed to load agent skills", "err", err)
		return ""
	}

	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, sk := range skills {
		fmt.Fprintf(&sb, "- %s: %s", sk.Name, sk.Description)
		// Parse tool_bindings to show tools.
		var tools []string
		if err := json.Unmarshal([]byte(sk.ToolBindings), &tools); err == nil && len(tools) > 0 {
			fmt.Fprintf(&sb, " [tools: %s]", strings.Join(tools, ", "))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
