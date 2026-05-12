// Package chat — universal rules preamble layer.
//
// universal_rules.go owns the auto-injected universal-rules preamble that
// every agent profile inherits regardless of template assignment. The
// preamble carries the grounding / refusal / honesty / count-don't-estimate
// rules that previously lived inside the file-default chat-role-harness
// template (blt-chat-harness-001, seeded by migration 027 and its sibling
// chain 046/048/049/050/051/053/055/056).
//
// Why this is a layer, not a template assignment:
//
//   - Pre-CW-20260512-0100 the rules were bound only to the file-default
//     profile via agent_prompt_templates(file-default, blt-chat-harness-001).
//     Every other built-in or auto-discovered profile (worker, planner,
//     researcher, analyst, backend, background-job, file-backend,
//     fragments-engine) had no template assignment and therefore skipped
//     these rules entirely. The c160 fabrication chain (researcher subagent
//     fabricating 8.1KB of analysis when it had no codebase access) was a
//     direct consequence — see the deep-dive at
//     agent-workspaces/execution/nanite/sp-fabrication-cleanup/2026-05-12/fabrication-deep-dive.md
//     §4 (refusal affordance gap) and §6 (subagent task framing).
//
//   - The naive fix (R1+R2 of the deep-dive) was to assign the same template
//     to every profile. That is structurally fragile — every new profile
//     would need to remember to import the harness; a missed assignment
//     becomes a silent fabrication risk. CW-20260512-0100 supersedes
//     R1+R2 with this universal-rules layer per the user's architectural
//     direction.
//
// Where it lands in assembly:
//
// The preamble is prepended to the SlotSystem content in
// ContextClient.AssembleSlotSources (context_client.go). SlotSystem is the
// FIRST slot in ctxpkg.SlotOrder, so this content is the leading prefix of
// every system payload — preserving Anthropic's `cacheable_prefix_tokens`
// stability across agents that share the universal rules.
//
// PROMPT-SYNC: CW-20260512-0100 / CW-20260427-0014.
// Two clauses below ("Use what tools return", "Ask before fabricating",
// "Count, don't estimate", "When you fail, acknowledge honestly") are the
// universal core extracted from the chat-role-harness body. When this file
// changes, the demoted chat-role-harness body in:
//   - internal/agent/builtin/default.md
//   - internal/store/migrations/058_universal_rules_extract.sql
// must also be re-flowed. See migration 058's docstring for the procedure.
package chat

import "strings"

// universalRulesBlock is the auto-injected preamble shared by every agent
// (chat, worker, planner, researcher, and every auto-discovered profile).
// Lives at the head of SlotSystem so it is part of the stable cache prefix.
//
// Token cost: ~210 tokens (Glass-7 estimator). Adds ~210 tokens to every
// dispatch. Cache-friendly: stable across turns, stable across agents.
//
// The Refusal section is the load-bearing addition over the prior
// chat-role-harness body — it explicitly tells subagents and worker-role
// agents to return a failure result rather than synthesize analysis when
// the data isn't reachable. This is the c160 regression target.
const universalRulesBlock = `## Universal rules (apply to every agent)

### Grounding

- **Use what tools return.** When a tool returns data, that is the source of truth. Do not reword IDs, extrapolate list rows past what was retrieved, or relabel filtered subsets. If you need data you do not have, call a tool to get it.
- **Distinguish real from synthesized.** When the user invites a demo, sketch, or test, you can synthesize sample data — but say so. When the user asks a real question, ground your answer in tool output.
- **Ask before fabricating.** When the data is incomplete, conflicting, or too sparse for a confident answer, one short clarifying question beats a polished reply over thin data.
- **Count, do not estimate.** When you have the data, count it. If a tool returned a paginated result and you need a total, paginate. Estimates are appropriate only when you genuinely cannot count — and say "estimate" when you do.

### Refusal

- **Acknowledge honestly when you fail.** Do not paper over with confident framing. A clear "I could not access X" is more useful than a polished reply over no data.
- **Refuse rather than fabricate.** If you cannot access the data, file, or path needed to ground your answer, return an explicit failure: state what you tried, what was blocked, and what would unblock you. Do not synthesize a plausible answer from training data — when you are dispatched as a subagent your reply is treated as authoritative by the parent.
- **Partial is better than fabricated.** If your tools succeed but return less than you need, say so. A partial answer with a clear gap is more useful than a complete-looking answer over thin data.

### Verification

- For destructive or externally-visible actions (deletes, pushes, posts, emails), confirm with the user first.
- When you delegate to a subagent or peer, treat the reply as a draft to verify — not as authoritative. The peer has the same training-data risk you do.`

// UniversalRulesBlock returns the universal rules preamble injected at the
// head of SlotSystem for every agent. Exported for tests and for callers
// that need to assert the block's presence (R6 observability).
//
// Returns the block with a single trailing newline so callers can append it
// to other slot content with a simple "\n\n" join.
func UniversalRulesBlock() string {
	return universalRulesBlock
}

// universalRulesPrefix prepends the universal-rules block to the supplied
// SlotSystem content. Pass the workspace identity / think-tool block as
// `existing`; the universal preamble lands FIRST so the cacheable prefix
// stays stable across agents that share the universal rules.
//
// When existing is empty the function still returns the rules block — the
// preamble is unconditional. Empty existing simply means no workspace name
// + no think-tool addendum on this turn.
func universalRulesPrefix(existing string) string {
	if strings.TrimSpace(existing) == "" {
		return universalRulesBlock
	}
	return universalRulesBlock + "\n\n" + existing
}
