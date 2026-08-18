# Alignment Review — Full-Context Pass

**Created:** 2026-08-17
**Author:** Claude, same session that produced the audit and the vision doc — full conversational context, not a fresh read
**Companion:** a genuinely independent review from a fresh session is running in parallel; where the two disagree, weight that one more heavily. This pass's value is different — I have the reasoning behind every decision in `release-vision.md`, including nuance that didn't make it into the written doc, which lets me stress-test it faster. Its risk is exactly that closeness: I may be defending conclusions I helped produce rather than genuinely challenging them. I've tried to counter that by actively hunting for holes rather than confirming what's already written, and I've flagged every place I think that bias risk is highest.

## Overall recommendation, up front

The vision doc's three locked decisions hold up well. The one place I think it's *incomplete* rather than wrong: it treats "CLI-wrapping is primary" and "durable agents are core" as two settled facts sitting next to each other, but never asks whether durable agents actually need the API-harness at all — every durable-agent instance observed happens to run on the API path today, but nothing structural requires that. If durable agents could run CLI-wrapped instead (spinning up a fresh, already-reliable CLI subprocess per wake, the same way Torque's one-shot agent launches already do), the amount of reliability work needed on the custom API-harness could shrink dramatically — because the harness that needs fixing might turn out not to be the one doing the actual work. I think this is worth a real, time-boxed investigation before committing more effort to hardening the five-layer broker/reflex/slot machinery for the durable-agent job it was just scoped down to.

Everything else below supports or qualifies that, plus the rest of the requested sections.

## 1. Vision-doc sanity check

**"CLI-wrapping is primary" is true but needs a qualifier.** The audit shows the *agent execution itself*, once a CLI subprocess is booted, is genuinely reliable (the full implement→review→merge cycle in `chat-analysis/05` ran cleanly end to end). But the layer that decides whether to boot CLI at all and gets it booted correctly has its own real, separate bugs: the stale `["pty"]` fallback chain misrouting API-intended sessions to CLI boot, the historical inverse ("c195," a CLI-intended session routed to the API instead), the `bootdir for provider "X" is not yet implemented` crash class, and a host-environment issue (the `tokf` hook silently failing on every Bash call in boot-profile-launched sandboxes). None of these are steering/reliability problems in the CLI-wrapped agent's own reasoning — they're boot-routing bugs in Nanite's own code, sitting directly in front of what's now the primary path. Making CLI primary doesn't make these bugs disappear; it makes them more consequential, since there's no longer a "well, at least the other path works" fallback framing.

**"Durable agents are core, not exploratory" holds up completely** — if anything the evidence for *why* it needs continued investment (not proof it's already solid) is overwhelming: the 38% all-time `subagent_runs` failure rate, the nine-fix Loom Curator saga, Agent Workflows' exactly-one-execution-ever-and-it-failed. "Core and currently roughest" isn't a contradiction.

**"Platform, with MCP as primary interface for external systems" is better-founded than it might look.** One thing worth surfacing that didn't make it into the vision doc: Nanite has *already* proven it can run as a real external-facing MCP server, not just an MCP client — the Agent Workflows external-engine integration (LangGraph/CrewAI callback tools) was validated via "an end-to-end smoke test" against a live MCP client (`chat-analysis/01-nanite-dev-sessions-early.md`, incident `180104ca`). That's a narrow slice of the eventual control-plane surface, but it means "build an MCP server" isn't starting from zero — it's broadening a mechanism that's already been exercised once, which is a meaningfully lower-risk starting position than the vision doc implies.

## 2. Prioritized alignment plan

Ranked by how much actually depends on each item, not by how many audit findings cluster around it.

**Tier 1 — blocks or materially de-risks the vision's own decisions:**
- Fix the CLI-boot/provider-routing bugs (stale fallback chain, hardcoded default, the bootdir-not-implemented crash class). This is now bugs in *the* primary path, not *a* path.
- Time-box an investigation into whether durable agents can run CLI-wrapped instead of API-based. This is the single highest-leverage open question — its answer determines how much of Tier 2 is even worth doing.
- Design and build a narrow MCP control-plane tool surface (wake + status, at minimum), namespace-separated from the self-tools surface, proven against one real consumer (Loom) before anything else depends on it.

**Tier 2 — real value, contingent on Tier 1's CLI-durable-agent answer:**
- If durable agents stay API-based: collapse the agent broker and strategy planner's duplicate reflex-catalog consultation into one decision (they currently run the identical match independently and weight it differently), and retire the tool broker's already-vestigial rule layer (`NaniteDefaultRules` is a single hardcoded catch-all; the config-loading path that would make it real is never called).
- Fix `FinalizeToolSelection`'s alphabetical-positional 15-tool cap — this is the direct, traceable root cause of Curator never seeing its own tools once the catalog grew past ~300. Small, surgical, worth doing regardless of any bigger architecture call.
- Retire A2A. Zero real callers, self-described as unverified against spec, and its stated purpose is now mostly redundant with MCP-as-primary.

**Tier 3 — worth doing, not urgent:**
- Clean up the naming collisions (reflex×2, broker×5, workflow×2, template×2, mode×4) — genuinely reduces future confusion cost, low risk.
- Apply the `migrate:skip-if-column-exists` guard pattern to every recreate-style migration, not just the three involved in the `e2273f8` crash-loop — the same failure class is still latent anywhere else that pattern was used (`043`, `089` per the audit).
- Prune obviously-dead schema opportunistically (`agent_boot_plans`, the unwritten `tool_enrichments` path, the never-constructed `gomsg` messaging layer) rather than as a dedicated project.

**Tier 4 — defer:**
- A real tenancy/ownership primitive for the platform model. Don't build this with full generality before a second real consumer beyond Loom actually shows up wanting isolation from the first — start with something as small as an `owning_system` tag, surfaced as a GUI filter, and grow it only when actually needed.
- Session-lifecycle refinements (wiring `compaction_events`, implementing `session_handoffs`) — real gaps, not blocking anything.

## 3. Sequencing

1. Fix the primary-path boot/routing bugs (Tier 1, item 1) — cheap, high-confidence wins, and they're in front of everything else.
2. Run the CLI-durable-agent investigation (Tier 1, item 2) before sinking more time into the API-harness. Its answer reshapes the rest of the plan.
3. Build the MCP control-plane surface against one real consumer (Loom) — this can happen in parallel with #2, since it's a different subsystem.
4. Branch on #2's answer: either extend CLI-wrapped durable-agent execution, or invest in the Tier 2 API-harness trims.
5. Tier 3 cleanup continuously, not as a blocking phase.
6. Tier 4 only once a second real platform consumer exists.

## 4. Named risks — answering the six items I posed to the fresh session

- **MCP's request/response shape vs. long-running agent work**: solvable, not a blocker. Nanite's own HTTP API already has the precedent (`202` + session ID, client tracks progress separately) — an MCP wake-tool can ack-and-return the same way, with a separate poll-style status tool rather than requiring the wake call itself to block until completion.
- **Platform + tenancy vs. embedded**: the user's own reasoning (large shared config surface makes N copies expensive) is sound. My addition: don't build the tenancy primitive with full generality up front — start minimal and grow it only when a second consumer actually needs isolation from the first. Building it fully now, before there's a second real consumer to design against, risks becoming its own multi-month side project.
- **Is the five-layer harness right-sized for "run durable agents reliably"?** Probably not as-is — but this is genuinely contingent on the CLI-durable-agent question above, not something to answer independently of it.
- **CLI-primary vs. durable-core tension**: this is real, and I don't think it should be accepted as a permanent two-substrate split without first checking whether it needs to be one. See the overall recommendation above — this is the one place I think the vision doc is incomplete rather than settled.
- **A2A**: retire.
- **Naming collisions — structural or cosmetic?** Leaning structural, not just accumulated sloppiness: the pattern (independently rediscovered 2-3 times before being named) points at a process gap — no shared glossary or "concepts already claimed" check that a new session consults before introducing new vocabulary — which is somewhat inherent to building via many separate, context-isolated AI sessions rather than a symptom of carelessness. A lightweight, actually-maintained concepts doc (not another one-time renaming pass) might be the more durable fix.

## 5. Smallest honest v1

- CLI-wrapped GUI interactive chat (modes 1–3), with the boot/provider-routing bugs fixed. Already close.
- **One** durable-agent use case proven reliably end-to-end — not all of Curator/Weaver/Atlas-Curator/Atlas-Librarian polished, just one. The Loom wiki-curation pattern is already ~90% there (compile job #5 succeeded).
- A minimal MCP control-plane surface (wake + status) replacing the bespoke Loom webhook, validated against that one consumer.
- The origin story and vision themselves, written up honestly — including what's still rough. Given the stated goal (sharing the work, feedback, portfolio/content value, not a monetized product), this is real value independent of how polished the underlying system is.

That's shippable without resolving platform/tenancy generality, without fixing all five broker layers, and without touching Agent Workflows further — it can stay explicitly experimental for v1.

## 6. Audit gaps

- GUI-native interactive usage isn't captured (already flagged in the vision doc — worth restating as a real gap, not just a caveat).
- **No performance/latency data anywhere in the 25-document audit.** Given the entire founding motivation was "CLI chat is too slow," the absence of any finding about how the GUI actually *feels* to use — responsiveness, perceived latency, time-to-first-token — is a notable blind spot for a project whose origin story is specifically about speed.
- **A likely-artifact-but-unresolved cost anomaly**: the live-DB analysis flagged `token_usage` rows with `cache_read_tokens` in the millions against `input_tokens` in the hundreds for the same row — explicitly noted as "far beyond any plausible context-window size" and never resolved as real vs. a telemetry bug. Worth a five-minute check before trusting any cost/usage numbers from this DB.
- **The frontend never got its own architecture doc.** Sixteen code-architecture docs cover the backend in real depth; the React/TypeScript side is only mentioned in passing (`plugin-loader.ts`, `ChatMessage.tsx`) inside other docs. Given "take advantage of GUI-based elements" is the founding motivation, the frontend not getting a dedicated pass is a real gap if UI/UX work is part of what comes next.

## 7. One more thing

The two things I'm least confident are actually resolved, despite writing confident-sounding paragraphs above: whether durable agents can genuinely run CLI-wrapped without losing something Nanite currently gets from owning the API-harness loop (fine-grained retry/escalation policy enforcement, for one — a CLI-wrapped session cedes its tool-calling loop to the wrapped tool's own harness, which might matter specifically for unattended background agents in a way it doesn't for interactive chat), and whether the cost/plan economics of CLI-wrapped vs. API-metered execution favor one path in a way nothing in this audit examined at all. Both are worth real answers before treating my Tier 1 recommendation as settled.
