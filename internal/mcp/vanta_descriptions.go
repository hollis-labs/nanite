package mcp

// Vanta tool descriptions (Bucket 2 chat-surface descriptions)
//
// CW-20260501-0005 sub-ticket 2 — scaffold for the chat-surface integration of
// Vanta MCP tools (memory_*, knowledge_*, context_*). This file holds curated,
// chat-surface-shaped descriptions that override Vanta's own tools/list
// description when the tool is presented to the chat agent.
//
// Why override? Vanta's tool descriptions are written for the broader Mux/Vanta
// surface (CLI users, plugin authors, sub-agent orchestrators). The chat agent
// has different needs — fewer turns, reactive recovery posture, no preemptive
// "always recall before X" gates. A bespoke description per Bucket 2 of
// docs/architecture/agent-context-architecture.md ("elevator pitch + skill
// pointer") fits the chat surface better than a generic Vanta description.
//
// Sub-ticket 3 (rollout) will wire this map into the tool-description path
// when the tool is exposed on ChatToolSurface. This file is **registry only**
// — adding a description here does not by itself put the tool on the chat
// surface.
//
// Authoring rules (for future entries):
//   - One paragraph elevator pitch (what + when to use).
//   - Contract block: required + optional args, output shape.
//   - One golden example block.
//   - Cross-references via VantaToolRelations: related tools + related skills.
//   - Reactive framing only — no preemptive gates ("must call X before Y" is
//     the c114 anti-pattern).

// VantaToolDescriptions maps Vanta-origin tool names to a chat-surface
// description that overrides whatever description the upstream tools/list
// returned. Entries are inserted at the chat-surface layer (sub-ticket 3),
// not at the Manager.DiscoverTools layer — `Manager.GetTool` continues to
// return Vanta's own description for non-chat callers.
var VantaToolDescriptions = map[string]string{
	"memory_recall": memoryRecallChatSurfaceDescription,
}

// VantaToolRelations records cross-references for Vanta-origin tools so
// `nanite_tool_describe` (renamed `tool_describe` in sub-ticket 3) can surface
// related tools/skills inline. Same shape as the existing self-tool
// describeRelations map, kept separate so the rename in sub-ticket 3 doesn't
// have to thread Vanta entries into a self-tool registry.
var VantaToolRelations = map[string]struct {
	RelatedTools  []string
	RelatedSkills []string
}{
	"memory_recall": {
		// Sibling write-side and the discovery primitives that pair with
		// recall during reactive recovery (after a tool fails, the agent
		// recalls past lessons → describes the failed tool → tries again).
		// Tool names use the post-rename convention from sub-ticket 3 (no
		// `nanite_` prefix); pre-rollout call sites still see the legacy
		// names. Sub-ticket 3 swaps the entries together with the rename
		// commit so the registry stays consistent in a single PR.
		RelatedTools: []string{
			"memory_write",
			"knowledge_get",
			"context_search",
			"tool_describe",
			"lesson_capture",
		},
		RelatedSkills: []string{
			"capture-to-vanta",
			"end-of-session",
		},
	},
}

// memoryRecallChatSurfaceDescription is the Bucket-2 chat-surface description
// for `memory_recall`. Bucket 2 (per docs/architecture/agent-context-architecture.md)
// is "elevator pitch + skill pointer" — enough to know whether to call the
// tool and roughly how, with `see skill <slug>` for the deep dive.
//
// Authoring rationale:
//   - Reactive framing — the description explicitly tells the agent NOT to
//     sweep memory on every turn, mirroring `nanite_memory_recall`'s
//     description (which exists for the local in-process store and was
//     scaffolded against the c114 describe-gate anti-pattern).
//   - Contract block matches Vanta's own argument schema (queries +
//     namespaces + filters), but framed for chat-loop intuition.
//   - Golden example shows the canonical reactive use case: recall lessons
//     captured for a specific tool after a contract-shape failure.
//   - Cross-references to capture-to-vanta + end-of-session skills so the
//     agent can find the deeper write-side discipline when needed.
const memoryRecallChatSurfaceDescription = "Retrieve durable memories from Vanta — decisions, lessons, follow-ups, and " +
	"limitations captured across sessions. Vanta is the canonical memory substrate (`vanta-primary-since: 2026-04-19`); " +
	"file-based auto-memory is legacy fallback.\n\n" +
	"**When to use:** Reactively. After a tool call fails with a confusing schema/contract error, " +
	"recall on the failed tool name before retrying — past-you may have left a lesson explaining " +
	"the shape mistake. Also fine when the user references a prior decision (\"the way we agreed " +
	"last week\") and you need to ground the answer. The lens is reactive, not preemptive: do NOT " +
	"call this on every turn or as a precondition for normal action.\n\n" +
	"**When NOT to use:** Don't sweep memory at the start of every turn — that re-introduces the " +
	"c114 describe-gate anti-pattern (preemptive recovery layer becomes a friction tax). The harness " +
	"already injects relevant memories via slot extensions; explicit recall is for targeted lookups " +
	"when those didn't surface what you need.\n\n" +
	"**Contract:**\n" +
	"- `query` (required): natural-language description of what you want to recall — used for hybrid " +
	"BM25 + cosine relevance ranking.\n" +
	"- `namespaces` (optional): list of Vanta namespaces to search (e.g. `[\"user/<user>/memory\"]`). " +
	"Default cascades user-scoped namespaces.\n" +
	"- `tags` (optional): filter by tags (e.g. `[\"decision\"]`, `[\"followup\"]`, `[\"limitation\"]`).\n" +
	"- `limit` (optional): max memories returned (default 10).\n" +
	"- `since` (optional): RFC3339 timestamp; only memories captured after this point are returned.\n\n" +
	"**Output shape:** Structured list of `{namespace, key, summary, body, tags, status, " +
	"revision_id, captured_at}`. Empty match returns an explanatory text block.\n\n" +
	"**Golden example (reactive recovery):** `{query: \"card_show envelope schema\", tags: " +
	"[\"learning\", \"tool:card_show\"], limit: 3}` — recalls the most-relevant captured lessons " +
	"about envelope contracts after a `card_show` failure. See the `capture-to-vanta` skill for the " +
	"write-side discipline that put those lessons there in the first place, and `end-of-session` for " +
	"how decisions/follow-ups/limitations get captured at session-close.\n\n" +
	"**Cross-references:** see `memory_write` (write side), `knowledge_get` (canonical knowledge " +
	"entries vs. memories), `context_search` (the older keyword-search path), `tool_describe` " +
	"(invariant tool contract), and `lesson_capture` (one-sentence-lesson recorder)."
