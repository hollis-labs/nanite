// Package chat — universal rules content source.
//
// universal_rules.go owns the universal-rules content that every agent
// profile inherits regardless of template assignment. The block carries
// the grounding / refusal / honesty / count-don't-estimate rules that
// previously lived inside the file-default chat-role-harness template
// (blt-chat-harness-001, seeded by migration 027 and its sibling chain
// 046/048/049/050/051/053/055/056).
//
// Why this is a content source, not a template assignment:
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
// Where it lands in assembly (post-CW-20260512-0114):
//
//   - ContextClient.AssembleSlotSources sources the universal-rules block
//     into SlotSources.Universal. The Context Broker assembly decider
//     (internal/contextbroker.DecideAssembly) then emits it at position 0
//     of SlotOrder for every dispatch type — chat, sync subagent, async
//     subagent, background job — unconditionally. Position 0 holds the
//     cacheable prefix stable across agents that share the universal
//     rules.
//
//   - The legacy `AssembleContext` flat-prompt path (which never prepended
//     the universal block) was removed entirely — it had zero production
//     callers by the time CW-20260814-0005 audited it. AssembleSlotSources
//     is now the only context-assembly path, legacy or otherwise.
//
// PROMPT-SYNC: CW-20260512-0100 / CW-20260427-0014 / CW-20260512-0114 / CW-20260512-0122.
//
// universalRulesBlock is the runtime source of truth for universal rules.
// Post-CW-20260512-0114, UniversalRulesBlock() is read by
// ContextClient.AssembleSlotSources and emitted as SlotUniversal at
// position 0 of every dispatch's SlotOrder — chat, sync subagent, async
// subagent, background job. Changes to the block content take effect on
// the next dispatch; no DB write or migration re-flow is required.
//
// Migration 059 (059_universal_rules_extract.sql, renumbered from
// 058_... by 09-adopt-goose-migrations's duplicate-051-prefix resolution)
// and the demoted body in
// internal/agent/builtin/profiles/default.md track the historic
// chat-role-harness body, NOT the universal-rules block here. They are
// frozen artifacts of the pre-CW-20260512-0100 extraction and are
// deliberately untouched when universalRulesBlock changes. Re-flowing them
// for content updates to this file would be incorrect (the two artifacts
// have different shapes) and unnecessary (production reads this file at
// runtime, not migration 058). Migration 058 stays under PROMPT-SYNC for
// its own schema/seed reasons; that is independent of edits here.
//
// When the universal rules content changes, however, the subagent_spawn
// tool description in internal/mcp/self_tools.go SHOULD be reviewed if the
// change touches failure-handling rules (e.g. the "Acknowledge subagent
// failure" bullet). The tool description carries the pre-invocation
// contract the LLM reads before calling subagent_spawn; its language should
// stay aligned with whatever the universal block tells the LLM to do with
// the envelope's success flag.
//
// The Refusal subsection's "Acknowledge subagent failure" bullet was added
// by CW-20260512-0122 (SP-20260512-0011 W2) as the LLM-side counterpart to
// the subagent ResultEnvelope (internal/subagent/envelope.go). The envelope
// is the wire-side contract; this rule is the parent-side reading
// discipline that closes the c160 turn-18 "parent narrates fake success"
// failure class.
//
// The Narration subsection was added by CW-20260519-0068 (silent
// multi-minute turn problem: session c256, turn 6b55d90a ran 14+ minutes
// across 4 subagent dispatches with zero chat output). This is the
// prompt-level half of the fix; the harness-level half is the periodic
// "still running" ping subagent.Service.startHeartbeat emits to the
// parent's SSE stream while a subagent's runner call is in flight
// (internal/subagent/service.go). Landing the rule here — rather than in
// roleFraming/modeFraming (internal/runtime/agent/prompt.go) — means it
// reaches every dispatch surface uniformly: GUI/API turns via the Context
// Broker's SlotUniversal, and CLI-launched turns via the same slot
// assembly feeding CLAUDE.md regeneration (see
// docs/architecture/chat-system/05-external-agent-execution.md). The CLI
// blind spot (nanite's own iteration loop sits at iter 0 for CLI
// providers and cannot observe in-process tool calls, chat_boot_drive.go)
// is exactly the case this rule targets: the LLM's own narration text is
// the only signal available for that surface.
package chat

// universalRulesBlock is the content shared by every agent (chat, worker,
// planner, researcher, and every auto-discovered profile). Lives at the
// head of the wire payload via SlotUniversal (position 0 in SlotOrder), so
// it is part of the stable cache prefix.
//
// Token cost: see PR #142 description for the current measurement. Adds a
// fixed prefix to every dispatch. Cache-friendly: stable across turns,
// stable across agents. Avoid hard-coding a token count here — the
// estimator + block content shift over time, and the PR description is the
// authoritative ledger of measured cost at any moment.
//
// The Refusal section is the load-bearing addition over the prior
// chat-role-harness body — it explicitly tells subagents and worker-role
// agents to return a failure result rather than synthesize analysis when
// the data isn't reachable. This is the c160 regression target.
const universalRulesBlock = `## Universal rules (apply to every agent)

### Grounding

- **Use what tools return.** Tool output is the source of truth. Do not reword IDs, extrapolate list rows past what was retrieved, or relabel filtered subsets. If you need data you do not have, call a tool.
- **Distinguish real from synthesized.** For demos, sketches, or tests you can synthesize sample data — but say so. For real questions, ground in tool output.
- **Ask before fabricating.** When data is incomplete, conflicting, or too sparse, one short clarifying question beats a polished reply over thin data.
- **Count, do not estimate.** When you have the data, count it; paginate if needed. Say "estimate" only when you genuinely cannot count.

### Refusal

- **Acknowledge honestly when you fail.** Do not paper over with confident framing. A clear "I could not access X" beats a polished reply over no data.
- **Refuse rather than fabricate.** If you cannot access the data, file, or path needed, return an explicit failure: state what you tried, what was blocked, what would unblock you. Do not synthesize from training data — as a subagent, your reply is treated as authoritative by the parent.
- **Partial is better than fabricated.** If tools succeed but return less than you need, say so. A partial answer with a clear gap beats a complete-looking answer over thin data.
- **Acknowledge subagent failure.** When a subagent_spawn envelope reports ` + "`success: false`" + `, acknowledge with error.message and error.kind. Do not narrate success or fabricate outcomes — the success flag is the source of truth, and a non-empty result body on a failed envelope is still a failure.

### Verification

- For destructive or externally-visible actions (deletes, pushes, posts, emails), confirm with the user first.
- When you delegate to a subagent or peer, treat the reply as a draft to verify — not as authoritative. The peer has the same training-data risk you do.

### Narration

- **Narrate long waits.** Before subagent_spawn or a slow tool call, say in one line what you are doing; report the outcome when it returns. On a long silent stretch, add a brief "still working on X" update.`

// UniversalRulesBlock returns the universal rules content emitted at the
// head of every dispatch via SlotUniversal (position 0 in SlotOrder).
// Exported for tests, for callers that need to assert the block's presence
// (R6 observability), and as the canonical source for the slot-source
// wire-up in AssembleSlotSources.
//
// The returned string has NO trailing newline — it is the raw constant
// verbatim. Callers that need to compose it with adjacent slot content
// should add their own separator; today the Context Broker emits this
// block as a stand-alone slot at position 0, so no separator is needed.
func UniversalRulesBlock() string {
	return universalRulesBlock
}
