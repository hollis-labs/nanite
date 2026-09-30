# Context assembly

Every LLM turn Nanite dispatches is assembled from a fixed, ordered list of
named slots. The list is `SlotOrder` in `internal/context/slot.go`, and the
Context Broker emits one decision per slot, in that order, on every turn.

The thing to understand before changing any of it is that **the order and the
positions are load-bearing**, not a presentation choice. Prompt caching is
priced on a stable prefix: the provider matches the longest run of leading
content it has seen before, so a slot that moves, or disappears on some turns
and not others, shifts everything behind it and turns a cache hit into a cache
miss. The cost of getting this wrong is not a wrong answer, it is a bill.

## One shape, every dispatch flavor

Chat, synchronous subagent, asynchronous subagent and background agent all
produce a plan with the same slots in the same positions. They differ in what
the slots *contain*, never in which slots exist.

That uniformity is the point. A subagent whose plan omitted two slots would
have its own prefix, its own cache, and no way to share either with the chat
turn that spawned it — and the difference would show up as an unpredictable
bill rather than as a failure.

An empty slot is therefore **still a decision**. It ships as a skip with a
reason attached, keeping its place in the sequence.

## Cache markers are a scarce resource

The provider caps how many cache breakpoints one request may carry. Nanite
spends them on the two slots at the front that do not change between turns, and
never on a slot whose content varies per turn.

Marking dynamic content would be worse than not marking at all: the marker
would invalidate on every turn while still consuming one of the few available.
This is why "which slots may carry a marker" is a codified list rather than a
heuristic.

## The mode slot is empty on purpose

**This is the part that gets broken by someone trying to help.**

Agent Mode and Session Mode were features. They were cut in full — the tables,
the accessors, and the per-turn classifier that suggested mode switches are all
gone. Reading only that, the obvious next step is to delete the now-pointless
slot.

**Do not.** `SlotOrder` still carries `SlotMode`, in its original position,
carrying nothing. The content source was removed; the slot was not. It ships as
an ordinary empty-content skip, exactly like any other slot with nothing to say.

Removing it would renumber every position behind it and change the sent shape —
breaking the stable-prefix guarantee above and the codified marker priority at
the same time. The slot is cheap. The renumbering is not.

If you are reading this because you found an always-empty slot and wondered
whether it was dead code: it is inert, deliberately, and the emptiness is the
documented state rather than a bug to fix.

## The contract, and where it actually lives

`internal/context/INVARIANTS.md` is the contract — seven invariants, each stated
with what relies on it and which test pins it. Every one is enforced by
`internal/service/slot_invariants_test.go`, which also carries
deliberate-violation tests, so each assertion is shown to catch a real break
rather than merely to run.

That file is the specification and this document is not a summary of it. Read it
before changing slot order, slot identity, compactability, or marker placement.

`AGENTS.md` states the rule that governs edits here: change the invariants
document and the test together, or change neither. A contract and its
enforcement drifting apart is worse than either being absent, because the test
still passes and the document still reads as authoritative.

## What fills each slot, by agent type

Nanite runs two kinds of agent. An **API-driven** agent is Nanite's own model
loop: every turn is the slot plan above, sent to a provider. A **CLI-launched**
agent is a coding CLI (Claude Code, Codex, OpenCode) running in a per-session
boot directory; Nanite does not send it slots. It sees a handful of planted
files, the per-turn user payload, and whatever the CLI itself brings.

The slot plan is still assembled for a CLI session on every turn. Only the
System, Agent, Mode and Rules slots (as a hash) and the UserContext slot (as
text prefixed to the user message) are consumed from it; the rest are not
delivered to the CLI.

| Slot | API-driven agent | CLI-launched agent |
|---|---|---|
| Universal | `UniversalRulesBlock`, position 0 | Not delivered. The boot prompt does not include it. |
| System | Think-tool block (`ThinkToolBlock`, or the dispatcher-selected v2 form) | Not delivered. The boot prompt instead ends with a fixed narration instruction and a mandatory re-read-after-compaction instruction. |
| Memory | Context Broker items whose source is memory | Not delivered. |
| Agent | The profile's system prompt, plus a post-compaction disclosure when one is fresh | `CLAUDE.md` "Operating Instructions": the profile prompt plus role and mode framing, any resolved dynamic-context blocks, and the two fixed instructions above. Identity, allowed tools, directories, tags and constraints go to `.sandbox/agent-context.md`. |
| Mode | Empty by design (see above) | Not applicable. |
| Rules | Agent tags and tool allowlist as Markdown | Carried in `.sandbox/agent-context.md`, not as a rules block. |
| Permissions | Rendered path-access summary | Not delivered. The CLI applies its own permission model. |
| Workspace | `AGENTS.md`, `CLAUDE.md` walk-up from the session working directory | Not delivered by Nanite. The CLI reads project instruction files itself. |
| Skills | Name and description listing; full skill loaded on demand | Granted skills are planted as real files in the CLI's native skill location; nothing is added to the prompt. |
| Tools | Selected tool definitions, with a lazy remainder | The CLI's own tools plus Nanite's self and dev tools served by the `nanite mcp` subprocess. |
| Session | Small session identifiers | Not delivered. |
| Context | Context Broker items whose source is not memory | Not delivered. |
| UserContext | Session context prompt and included documents | Prefixed to each user message (`composeUserPayload`). |
| Handoff | Pinned compaction handoff | Not delivered. The CLI compacts its own transcript. |
| Conversation | Serialized message history | The CLI's own transcript. |

Two consequences follow from the table. The universal rules block, the
permissions summary and the memory and context enrichment never reach a
CLI-launched agent, so behavior those slots enforce for an API agent is absent
for a CLI one. And a CLI agent gets its self-tool results through the MCP
proxy, which applies the result cache and model-aware truncation but not the
chat loop's per-tool cap, turn ceiling or stuck-loop handling.

Sizes are deliberately not recorded here: they change with every profile, skill
grant and tool grant. Measure them against a real database when needed.

## Verify

```bash
# which slots the plan carries, in order
grep -n -A18 'var SlotOrder' internal/context/slot.go
# what the CLI boot prompt is built from
grep -n 'func composeSystemPrompt\|func ResolveSystemPrompt\|func withCLINarration' internal/runtime/agent/prompt.go
# the slots a CLI session consumes from the plan
grep -n 'slotsChangedFor\|composeUserPayload' internal/service/chat_boot_drive.go
# the universal block has one call site outside its definition and comments;
# it is a slot source, and no CLI boot-content builder calls it
grep -rn 'UniversalRulesBlock()' --include='*.go' internal | grep -v '_test\|//'
```

## What this does not cover

- **How each slot's content is computed.** The table above says what reaches
  each agent type; the sources are spread across the chat, agent, plugin and
  broker packages and change more often than the shape does.
- **Compaction.** Which slots may be compacted under pressure, and in what
  order, is its own subject.
- **Provider specifics.** The caching model described here is the one Nanite
  targets; the wire details belong with the provider integration.
