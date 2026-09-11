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

`internal/context/INVARIANTS.md` is the contract — six invariants, each stated
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

## What this does not cover

- **What fills each slot.** Sources are spread across the chat, agent, plugin
  and broker packages and change more often than the shape does.
- **Compaction.** Which slots may be compacted under pressure, and in what
  order, is its own subject.
- **Provider specifics.** The caching model described here is the one Nanite
  targets; the wire details belong with the provider integration.
