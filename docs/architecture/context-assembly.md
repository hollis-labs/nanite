# Context assembly

Every LLM turn Nanite dispatches is assembled from a fixed, ordered list of
named slots. The list is `SlotOrder` in the pinned substrate agent module’s `context/slot.go`, and the
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

The pinned substrate agent module’s `context/INVARIANTS.md` is the contract — seven invariants, each stated
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
| Agent | The profile's system prompt, plus a post-compaction disclosure when one is fresh | `CLAUDE.md` "Operating Instructions": the profile prompt plus role and mode framing, any resolved dynamic-context blocks, a "Project folder" section naming the session's work root when it has one, and the two fixed instructions above. Identity, allowed tools, directories, tags and constraints go to `.sandbox/agent-context.md`. |
| Mode | Empty by design (see above) | Not applicable. |
| Rules | Agent tags and tool allowlist as Markdown | Carried in `.sandbox/agent-context.md`, not as a rules block. |
| Permissions | Rendered path-access summary | Not delivered. The CLI applies its own permission model. |
| Workspace | `AGENTS.md`, `CLAUDE.md` walk-up from the session working directory | Not delivered by Nanite. The CLI runs in its boot directory; the work root (a durable agent's `work_root`, else the project's `repo_path`) is granted as an extra directory, and the boot prompt names it and tells the agent to read its instruction files. |
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

## When a prompt change takes effect

Nanite has no mechanism that selects a different prompt by model, context
window, capability or task, and none that swaps a variant in mid-session. That
is a decision, not an omission: a variant chosen per turn would fragment the
shared prefix the first sections of this document protect, and a mid-session
swap would invalidate the cache. Variation is expressed by launching with a
different profile, not by switching a running session.

What the prompt is derived from is persisted state: the agent definition and
its grants, the session and its assignment, and the launch profile. Slot order
is the stable-prefix contract; the content derived into each slot is not
frozen. That gives three timings, and which one applies depends on the agent
type:

| Change | API-driven agent | CLI-launched agent |
|---|---|---|
| Edit to the agent's stored definition (prompt, tags, tool allowlist) | Next turn: the plan is rebuilt from the database every turn | Next turn, when the System, Agent, Mode or Rules slots hash differently: `CLAUDE.md` and `.sandbox/agent-context.md` are rewritten and the running session is told to re-read them |
| Skill grant or revoke | Next turn | Next turn: granted skills are re-planted into the boot directory every turn |
| Session context prompt and included documents | Next turn, by design | Next turn, prefixed to the user message |
| Change to project instruction files under the working directory | Next turn (the walk-up is re-checked by modification time) | Whenever the CLI itself rereads them |
| Agent prompt text stored in the database (including one updated by a migration) | Next turn | Next turn, through the same slot-hash re-plant as an edit to the definition |
| Prompt text compiled into Nanite (the universal rules block, the think-tool block, the fixed instructions appended to a CLI boot prompt) | Next turn on a server running the new binary | Only when `CLAUDE.md` is next written: at boot, or by a re-plant that a slot-hash change happens to trigger. The hash covers slot content, not the compiled-in CLI instructions, so a change to those alone reaches a running CLI session only on relaunch or resume |
| Resolved dynamic-context blocks, a boot prompt override, the launch profile | Fixed at launch | Fixed at launch. The re-plant reuses the dynamic-context blocks resolved at boot rather than resolving again, and carries no override, so changing these needs a relaunch or resume |

So the rule to hold is narrower than "static per launch": nothing selects or
swaps prompt variants, and only launch inputs (plus, for a CLI agent, compiled-in
boot instructions) need a relaunch to change.
Anything volatile belongs in a per-turn slot, or behind a tool the agent calls
when it needs it, rather than in launch-time content. A long-running API session
does not need a relaunch to pick up an improved prompt. A long-running CLI
session does for compiled-in CLI instructions, for a different launch profile,
and for freshly resolved dynamic context.

Adding a variant-selection or hot-swap mechanism later is a design change to
the prefix contract above, and it should start from that contract's
invariants rather than from the slot content.

## Verify

```bash
# which slots the plan carries, in order
agent_module=$(go list -m -f '{{.Dir}}' github.com/hollis-labs/substrate/agent)
grep -n -A18 'var SlotOrder' "$agent_module/context/slot.go"
# what the CLI boot prompt is built from
grep -n 'func composeSystemPrompt\|func ResolveSystemPrompt\|func withCLINarration' internal/runtime/agent/prompt.go
# the CLI re-plant: slot-hash check, rewrite of boot-dir files, launch-time blocks
grep -n 'func (s \*chatServiceImpl) regenerateBootDirSlots\|activeSessionContextBlocks' internal/service/chat_boot_drive.go
# per-turn plan rebuild on the API path
grep -n 'AssembleSlots(' internal/service/chat_generate.go
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

## Manual conversation clear

`/clear` calls `Container.ClearSession` through `POST /api/sessions/{id}/clear`.
It resets the working conversation, keeping the full transcript and adding a
“Conversation cleared here” message. Boot/system context, included documents,
session pins, verified definitions and host grants survive. The default removes
that session's Glass4 handoff stash; `/clear --keep-handoff` retains it for the
handoff injection path. Undo is outside this command's contract.

An active or queued turn requires explicit confirmation. The first request
returns `409 clear_confirmation_required` with `active_turn_ids`. A confirmed
request supplies `confirm_cancel: true` and the exact `expected_turn_ids`; a
changed set requires new confirmation. The service fences session admissions,
cancels each captured turn through its existing cancellation owner, and waits
for safe settlement before stopping the provider runtime. A refused or timed-out
reset retains the transcript and stash; cancellation or runtime stop may already
have happened, so this is not an atomic rollback of provider effects.

The durable cut lives in `session_events`, independently of presentation
metadata. The marker, event, optional stash removal and provider resume-ID reset
commit in one transaction. Context assembly, intent, retry and restart recovery
read messages after that cut; transcript browsing, search and export retain the
full history. A transcript-copying fork carries the cut with its new marker ID.
Equal-second timestamps are ordered by the retained message rows, and the cut
is re-resolved from its marker ID after reopening the database.

### Cached result references during replay

A cache footer describes availability when its preview was made. Cached tool
bodies retain the configured one-hour default TTL and per-result 1 MiB cap;
the cap is not an aggregate eviction policy. Context assembly and each native
provider request reconcile references with the session-scoped cache reader.
Expired, missing/purged or metadata-only bodies become an explicit unavailable
marker while keeping the preview. An unsuccessful availability check is marked
unknown, not reported as expiry. Stored transcripts and original results are
not rewritten, and retention loss does not change the original tool outcome.
No source call is repeated automatically; a fresh query requires current tool
permissions. Retrieval itself remains the final availability check if a body
expires after projection.

CLI recovery packs reconcile replayed references before rendering. Availability
notices survive bounded history clipping. A resumed provider owns its prior
history, so Nanite cannot edit those old messages: each turn carries an explicit
historical-reference correction plus unavailable IDs from the working history
window. The correction covers older provider-held references too; it does not
claim that opaque provider history was rewritten. Clear boundaries continue to
exclude prior working history from both replay and these corrections.
