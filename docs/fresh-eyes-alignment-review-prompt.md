# Fresh-Eyes Alignment Review — Prompt

**Purpose of this file:** a self-contained prompt to hand to a new session (no memory of the audit or vision-doc conversations) to independently review Nanite's current state against its stated release vision and produce a real, opinionated alignment plan. Copy the section below the line into a fresh session as its first message.

---

You're doing an independent architecture-alignment review of Nanite, an agent-agnostic multi-agent chat harness. Two documents already exist that you should treat as your starting evidence base — read both in full before forming any opinion:

1. **`docs/system-audit/2026-08-17/00-overview.md`** (and its linked sub-documents — 16 code-architecture docs, 6 chat-analysis docs pulling from real dev-session transcripts and a live production DB, a cross-source synthesis, and a taxonomy doc). This is a thorough, evidence-cited snapshot of how the system actually works today and where real usage shows it breaking. It was written to be neutral — no recommendations, no judgment calls, just cited findings.
2. **`docs/release-vision.md`**. This is the product owner's stated destination: what Nanite is for, who it's for, and a handful of already-decided architectural directions (CLI-wrapped subprocess agents are the primary execution path for interactive use; the custom API-harness's job narrows to running durable/background agents reliably; Nanite is a platform, not per-consumer embedded instances, with MCP as the primary interface for external systems).

## Your job

Compare what exists against what's wanted, and tell us **what you would actually do** to close the gap. This is explicitly not another neutral audit — form and state a real point of view. Be decisive. Where you think the vision doc's own reasoning doesn't hold up against the evidence, or a "decided" direction looks shakier once you dig in, say so directly and explain why. The value of a fresh session here is a genuinely independent second opinion, not confirmation.

**Ground rule:** don't just trust the audit's characterization of the code for anything your recommendation actually depends on. Read the real source for anything load-bearing to a conclusion you're drawing. The audit is thorough but was written across many separate passes over one snapshot in time — a second, skeptical read of the actual code can catch things it missed.

**Do not write or modify code.** This is a written report only.

## What to produce

A markdown report at `docs/alignment-review-<today's date>.md`, covering:

1. **Vision-doc sanity check.** Does it hold up against the audit's evidence? Any internal inconsistencies, unstated assumptions, or decisions the cited evidence doesn't actually support as strongly as claimed?
2. **A prioritized alignment plan.** Given the vision's decisions (CLI-wrapping primary; API-harness scoped to durable/background agents; platform model with MCP as the primary external-system interface), what would you cut, combine, streamline, or deliberately leave alone in the current codebase? Rank by actual impact — don't just enumerate the audit's findings back. Cite specific evidence for each call.
3. **Sequencing.** What order would you do this work in, and why? What has to happen first — for example, does the MCP control-plane tool surface need to exist before anything else, since external consumers (Loom, and per the vision doc, likely most of the portfolio) will come to depend on it?
4. **Named risks in the plan itself.** Anywhere you think a decision in the vision doc might not survive contact with actually building it (see the specific list below for starting points, but don't limit yourself to it).
5. **The smallest, honest "v1 that reflects this vision."** Not a comprehensive rebuild — what's genuinely required to actually release, versus desirable but deferrable? The project has no users and no monetization pressure, but it does have a real goal of getting to something shippable rather than staying in perpetual exploration.
6. **Audit gaps.** Anything the existing 25-document audit seems to have missed, under-weighted, or structurally couldn't see from what it worked from — it drew on ~4 days of dev-session transcripts, a live DB snapshot, and the code as of 2026-08-17. It explicitly does not capture genuinely interactive, GUI-native usage, for instance.
7. **Anything else you noticed that seems important and wasn't asked about directly.**

Lead the report with your overall recommendation, then support it — don't bury the conclusion.

## Specific things we'd like your independent POV on

These are tensions we've already noticed but haven't resolved. Treat them as starting points, not a checklist to confirm — agree, disagree, or reframe them as you see fit, and feel free to find your own instead.

- **Does MCP actually fit "wake a durable agent and track its status" well?** MCP's tool-call model is fundamentally request/response. Waking a long-running background agent and later checking on it is a different shape. Does the MCP-primary decision need a companion mechanism (a resource/subscription pattern, a callback, something else), or does plain request/response genuinely cover it?
- **Is "platform + a real tenancy primitive" actually less total work than the embedded-instance alternative it was chosen over?** Building clean agent-ownership/access-control boundaries between consumer systems, plus the GUI work to show "whose agent is this," could turn into its own multi-month sub-project. Worth a genuine gut-check, not just trusting the reasoning that favored platform.
- **The custom API-harness's job just narrowed from "general chat engine" to "run durable background agents reliably."** Is the existing five-layer broker/reflex/strategy/slot-assembly machinery actually the right shape for that narrower job, or does a much simpler, purpose-built durable-agent execution path make more sense than trying to trim the general-purpose harness down to size?
- **Is there real tension between "CLI-wrapping is primary" and "durable agents are core"?** Every durable-agent instance observed in the live DB runs via the API path, not CLI-wrapped — so "CLI primary" and "durable agents core" are, today, two different execution substrates pointed at two different priorities. Does that need reconciling, or is it fine as a permanent two-substrate split?
- **A2A protocol: retire it, or keep it?** It has zero real callers, is self-described in its own code as unverified against spec, and its stated purpose ("a protocol adapter, not a new execution substrate") largely overlaps with what MCP-as-primary now does.
- **The sheer number of naming collisions the audit found** (two "reflex" systems, five "broker" layers, two "workflow" packages, two "template" tables, four "mode" mechanisms) — is this ordinary tech debt to clean up incrementally, or evidence of a deeper structural gap (no shared vocabulary/registry across a system built in many separate passes) that a bigger structural fix would address more durably than a naming pass?
