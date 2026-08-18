# Nanite — Release Vision

**Created:** 2026-08-17
**Status:** Draft — for review
**Source:** `docs/system-audit/2026-08-17/` (the system-level audit) + direct conversation on 2026-08-17 about what "released" should mean

## Why this doc exists, and why it isn't another `vnext-mvp.md`

`docs/vnext-mvp.md` and `docs/post-mvp-plan.md` are phased implementation checklists — features, in order, with a "beta-ready" goal. This doc is upstream of those: it's what Nanite is *for*, so that the next round of "what do we cut, combine, or build" decisions has something concrete to align to, instead of continuing the organic, explore-by-building pattern that got Nanite this far. That pattern was the right call up to now — it's how you build, and it's produced real, working value (the system audit is proof: CLI-wrapped agents work well, a relay-session driving other sessions works today, durable agents are doing real autonomous work). This doc is about deciding, now that there's real usage and real opinions, what the shape of the *next* phase is.

## The bar for "released"

**Personal daily-driver + portfolio piece.** Not enterprise. Not chasing adoption or monetization. The bar is: it works great for you specifically, and it's public/documented/demoable enough that sharing it is worthwhile — as working software, as a writing/content subject, and as portfolio material. Its value doesn't depend on anyone else using it.

Nanite itself should depend on nothing outside itself to exist and run (see "No dependencies," below) — but it's explicitly fine, expected even, for Nanite to demonstrate real integrations with the rest of your stack. The distinction that matters: Nanite's *own* existence and operation can't require anything external; what gets *built on top of* Nanite can require whatever it needs.

## Origin story (why Nanite exists — this is real content/portfolio value on its own)

1. Raw CLI-based chat UX was too slow. You wanted GUI elements — especially for the moments an agent needs feedback from you, where a GUI card beats scrolling terminal text.
2. That led to wanting to work with **more than one agent at once** — a GUI that could hold multiple sessions side by side, still fundamentally separate conversations.
3. That led to wanting **one chat session that relays/drives the others** — a single point of control coordinating a small fleet of agent sessions rather than you context-switching between them. This works today, mostly.
4. The CLI-wrapping idea came from originally trying to control Claude-based CLI sessions specifically, because at the time, driving an agent through raw API calls couldn't match what the CLI tool itself was capable of. You built the GUI on top of CLI subprocesses for that reason.
5. API-based chat is far more capable today than when you started, and Nanite grew a large, genuinely sophisticated harness around it (context/tool/skill/agent brokers, a reflex engine, a slot-based prompt assembler, a strategy planner) — but per the audit and your own experience, that path still has real reliability, steering, and drift problems that the CLI-wrapped path doesn't.
6. Most recently, you added **Agent Workflows** — a first-party DAG/step-based system in the spirit of Google ADK / LangChain / LangGraph — because free-form chat-loop steering alone isn't reliable enough for some tasks (writing a task to Torque, curating a wiki page) that need predictable, auditable, multi-step execution rather than "trust the prompt."
7. The loop this actually runs on, in practice: a Claude Code session implements or fixes a piece of Nanite, then immediately drives a *live* Nanite session — the Orchestrator, Curator, or another durable agent — to do real work, and whatever breaks under real use becomes the next ticket. Nearly every finding in the system audit was surfaced exactly this way, not through routine testing. See "A day in the life," below.

## Nanite is a runtime; GUI, CLI, API, and MCP are consumers of it

This is the cleaner mental model to build toward, and it's not fully realized in the current code. Nanite is one runtime. **GUI, CLI, API, and MCP are four different doors into that same runtime, for four different kinds of callers**: a human at the browser (GUI), a human at a terminal (CLI), a system/automation calling in (API), and another agent calling in (MCP). None of these should be a distinct execution path with its own rules — they're access surfaces onto one thing.

The system audit's finding that there are nine distinct launch entry points (`03-launch-paths.md`) is exactly this pattern showing up as more doors than intended, or without a clean shared frame: `nanite launch`, `nanite chat`, the browser UI, durable-agent Start/Resume/Wake (API), the internal scheduler tick, an external webhook, A2A JSON-RPC, in-session subagent delegation, and the workflow-runner. They already converge on two shared execution primitives underneath (CLI-boot vs. direct API call) — the audit calls this out as proliferation in *entry points*, not in underlying mechanisms. Worth keeping the "one runtime, several doors" framing in view as a north star for what "the right number of entry points" should look like, without prescribing the answer here.

## What released Nanite is for

Four usage modes, in the order they emerged and roughly in order of how proven they are today:

### 1. GUI-driven interactive agent work (CLI-wrapped) — the primary mode

A human sits at the GUI and drives a CLI-wrapped coding/general-purpose agent (Claude, Codex, or OpenCode running as a subprocess Nanite manages), getting the speed and structured-feedback benefits of a real UI — envelopes, approval cards, structured question forms — instead of raw terminal scrollback. **Per your call, this is the primary execution path going forward.** The audit backs this up: `03-launch-paths.md` and `06-provider-llm-roundtrip.md` both confirm the CLI-wrapped subprocess bridge is architecturally sound and is where "Nanite adds value" today, in your own words. (Note: despite the "pty-claude" naming convention still used in provider strings and the audit's own original prose, no real pseudo-terminal is allocated in production — Claude's long-lived sessions use structured NDJSON over plain stdin/stdout pipes, not terminal emulation. See `06-provider-llm-roundtrip.md`'s correction note.)

### 2. Multiple simultaneous agent sessions in one interface

Working with more than one agent at a time without juggling terminal tabs or windows — each still a distinct session, but visible and reachable from one place.

### 3. A single relay/driver session coordinating the others

One conversation you actually talk to, which in turn dispatches, delegates to, and coordinates the other sessions — subagent spawn, delegation, inter-agent messaging (`12-inter-agent-messaging.md`). This is the "control plane" pattern and, per you, it works today, mostly. The audit's inter-agent-messaging findings (subagent completions not reliably surfacing to the parent, three documented occurrences over three months) are the concrete "mostly" — worth keeping in view as this mode gets more real use.

### 4. Unattended, scheduled background agents — a core pillar, not a side experiment

Durable agents that wake on a schedule or an external trigger and do real, narrowly-scoped autonomous work with no human turn-by-turn in the loop. **You've called this core, not exploratory.** Every durable-agent instance observed in the live DB runs via the API path today (`09-durable-agents-runtime.md`), which is why the API-harness's job narrows to serving this mode specifically (see below) — though that's an observation about current practice, not a hard rule; the boot-launched CLI transcripts (`chat-analysis/05`) show CLI-wrapped agents running perfectly well unattended too, just as one-shot task runs rather than agents with a persistent, repeatedly-woken identity. This is also the mode with the roughest edges in the audit: the Loom Wiki Pilot integration needed nine distinct root-cause fixes across three codebases before a single successful end-to-end run.

**Important ownership clarification**: agents like Curator aren't Nanite's own agents that happen to talk to Loom — **Curator belongs to Loom.** Nanite is the runtime it happens to run on, the same way a database doesn't belong to the applications built on it. This matters for scoping: mode 4 isn't "Nanite has some example integrations with the portfolio" — it's "Nanite is infrastructure other systems build their agents on top of," and Loom is simply the first real consumer of that.

## No dependencies — Nanite depends on nothing outside itself

This is a hard invariant, not a preference: **any agent Nanite runs must be able to exist and run using nothing but Nanite itself.** Two things that look like dependencies in the current setup are not:

- **Cerberus** runs Nanite as a daemon. This is a deployment convenience — the equivalent of using systemd or pm2 — not an architectural dependency. Nanite has to be able to run without Cerberus.
- **Tether's Agent Mux** is an optional MCP proxy that saves you from configuring the same MCP servers in multiple places and adds observability into agent activity. It is purely optional. The audit's tool-naming-collision incident (`07-tool-calling-mcp.md` §4 — a same-named upstream proxy silently stealing `dev_bash`'s slot) is a concrete illustration of what goes wrong when an *optional convenience* leaks failure modes into what looks like core reliability. Nanite must work fully with Agent Mux entirely absent.

And the dependency direction with consumer apps only ever points one way: **Loom depends on Nanite. Nanite does not depend on Loom** (or Torque, or Fragments Engine, or anything else built on top of it). Curator is Loom's agent, hosted by Nanite's runtime — Nanite doesn't need to know or care that Loom exists for its own core operation to be complete and demonstrable.

## Decided: platform, with MCP as the primary interface for external systems

Resolved, after weighing both shapes explicitly. The deciding factor: Nanite's config surface is large — provider keys, MCP server registrations, agent profiles, boot catalogs, tool-broker rules, durable-agent YAML — not the "one shared key plus an agent profile" shape that would make N independent copies cheap to keep in sync. Your other apps are already responsible for their own config and runtime; replicating Nanite's config surface N times on top of that would be a real, ongoing debt, not a one-time cost. And per you, nearly every app in the portfolio will want to be a consumer as soon as this works — well past the point where a shared platform's compounding-reliability-fixes advantage outweighs an embedded instance's simpler-boundary advantage.

**External systems talk to the platform through MCP, not through bespoke integrations.** This is a real architectural change, not a formalization of something already true. Today, external access to Nanite is a hodgepodge: a purpose-built HTTP webhook hardcoded to Loom's specific payload shape (`POST /api/loom/curator-wake`), a generic Durable Agent Wake API that — per its own doc comment — "cannot serve an arbitrary external caller's payload shape" and needs a new bespoke decode handler per consumer, and an A2A JSON-RPC protocol attempt that's fully wired but has never been called by anything (`03-launch-paths.md`, `09-durable-agents-runtime.md`, `12-inter-agent-messaging.md`). MCP-as-primary replaces all of that with one standard, self-describing contract: external systems discover what's callable via `tools/list` and call standard control-plane tools (wake this durable agent, send this message, check this status) instead of Nanite growing a new bespoke HTTP handler every time a new consumer shows up.

This also sharpens the "GUI/CLI/API/MCP are consumers" framing above: **GUI and CLI are first-party clients** — they can call whatever internal API is convenient and evolve in lockstep with the backend. **MCP is the boundary contract for everyone else** — external systems, other agents, anything not shipped as part of Nanite itself. That's a clean, principled line, and it's symmetrical with what Nanite already does on the other side: it's already an MCP *client* (consuming tools from Agent Mux, Torque, etc. for its own hosted agents) — this makes it an MCP *server* too, for the same reason and via the same protocol.

The boundary this closes also satisfies "no dependencies" cleanly: consumers depend on Nanite's MCP surface; Nanite's own operation never needs to know a given consumer exists.

Two corollaries worth naming here, not deciding — they're now sharper candidates for the next planning pass:

- **A2A's role gets more questionable.** It was built specifically to be "a protocol adapter, not a new execution substrate," has zero real callers in the live DB, and is self-described in its own code comments as unverified against the live spec. If MCP is now the primary external-system interface, A2A looks like a strong candidate for elimination or consolidation rather than something to keep building out.
- **Namespace separation matters from day one.** Nanite already has a documented history of exactly this failure mode — commit `5144590`, where an external MCP proxy's tools collided with Nanite's own builtin tool names and silently won the slot (`07-tool-calling-mcp.md` §4). The tools Nanite exposes *to its own hosted agents* (self-tools like `dev_bash`, `message_send`) and the tools it exposes *to external control-plane callers* (wake this agent, get this status) need to be cleanly separate surfaces from the start, not merged into one MCP server and disambiguated later.

## The two execution substrates, and their now-clarified jobs

Distinct from the consumers-of-runtime question above, this is about *how an agent's LLM calls actually happen* once a session is running:

- **CLI-wrapped subprocess agents** (Claude/Codex/OpenCode) are the primary path for modes 1–3. This is the part of Nanite that already works well.
- **The custom API-harness** — context broker/slot system, tool broker, agent broker, strategy planner, reflex engine, direct-HTTP provider abstraction — no longer needs to be a general-purpose competitor to CLI-wrapping for interactive chat. Its job narrows to one thing: **be great at running unattended background agents reliably (mode 4).**

That's a real narrowing worth naming explicitly, not deciding here: the audit found this machinery was built (across many separate tickets, over time) as if it needed to generally serve *every* kind of agent interaction — five independently-evolved decision layers, a 14-slot prompt assembler, three non-interoperating messaging substrates, a third of the schema never actually exercised in production. If its actual job is now "run background agents well" rather than "be a general chat engine," that's a much smaller, more tractable target than what currently exists — and a natural candidate for the "what can we eliminate, combine, or streamline" pass you mentioned wanting to do next.

## Two ways to script a durable agent's behavior — coexisting, not a hierarchy

Within usage mode 4, there are two distinct mechanisms available for defining what a background agent actually does:

- **Free-form chat loop + steering** (reflexes, the broker/strategy layers, prompted instructions) — flexible, generalizes to open-ended tasks, but per you, "boot prompts/steering won't ever be enough for some use cases." The audit's evidence backs the limits here concretely: reflex-catalog phrase collisions misrouting a wake to the wrong agent identity, five decision layers independently judging the same turn, tool-name mismatches an agent has no way to discover on its own.
- **Agent Workflows** (the DAG/step-based system, ADK/LangGraph-shaped) — for tasks that need predictable, auditable, multi-step execution rather than trusting a prompt to get it right every time.

**These are not a replacement relationship** — Workflows is not meant to eventually subsume steering. Per you, they may turn out to be genuinely mutually exclusive — the right tool depends on the task — but that boundary isn't clear yet, because Agent Workflows only just landed. The audit's live-DB check found exactly **one recorded execution of Agent Workflows in the entire database's history, and it failed** (`chat-analysis/07-synthesis.md`, Pattern F) — so neither mechanism can be called dependable yet, and the question of "when do I reach for which" is genuinely open, not a roadmap item.

## A day in the life

These aren't hypotheticals — they're drawn directly from the audit's evidence base, because the last several days of actual sessions are a representative sample of how Nanite gets built and used.

**Driving a fleet through one session (mode 3).** A single Orchestrator session (`66f0330a…`, live DB) ran for roughly 20 hours, dispatching worker/reviewer/researcher subagents through the agent broker, self-diagnosing a tool-hallucination problem mid-session ("declared in my system prompt allowlist but not actually loaded"), and — despite that front-loaded confusion — going on to successfully coordinate five-plus real task dispatches later in the same session. This is the relay/driver pattern working, with its rough edges fully visible in the same session (`chat-analysis/06-live-runtime-db.md`, incident 4).

**A background agent doing real unattended work for another system (mode 4, and "Curator belongs to Loom" in practice).** Loom's Curator agent, woken by an external Fragments Engine callback with zero human turn-by-turn involvement in the actual compile step, successfully compiled a wiki page end-to-end (compile job #5) — after nine distinct root-cause fixes spanning wake-payload plumbing, session-reuse semantics, a stale model pin, MCP tool-visibility, a tool-binding regression, tool-naming mismatches, and a stale MCP proxy subprocess, discovered across both the Nanite and Loom repos over the better part of a day (`chat-analysis/03-loom-fragments-engine-dev-sessions.md`). This is exactly the "Curator is Loom's agent, hosted by Nanite" relationship in action, including what it currently costs to keep that relationship working.

**Building Nanite by dogfooding Nanite.** The single most common pattern across the entire audit period: implement or fix a piece of the harness, then immediately put a live Nanite session to real work to see if it holds up. The Orchestrator durable agent couldn't be launched at all for most of a day because one `class` value was missing from a validation enum — found only by trying to actually launch it, not by any test (`chat-analysis/02-nanite-dev-sessions-late.md`, `07317d39`). This build-then-dogfood-then-fix loop is effectively the primary development methodology, not a side effect of testing.

**One flavor not yet well represented in this evidence base:** the audited transcripts are Claude Code dev sessions plus direct DB queries — genuinely interactive, GUI-native chat sessions (mode 1/2 from the human side, browser open, talking to a single CLI-wrapped agent) aren't distinctly captured in what was reviewed. Worth a dedicated look if a sharper picture of that specific experience is needed.

## Explicitly not the goal

- No monetization.
- No enterprise features — no multi-tenancy, no auth/teams, no hardened ops posture.
- No chasing broad adoption or generalized "works for anyone's stack" resilience.
- No commitment to support arbitrary LLM providers beyond what you actually use (the audit found only Anthropic and OpenAI are currently registered as live providers, despite several code comments narrating a wider roster — that's fine as-is, not a gap to close for its own sake).

## Open questions for the next planning pass

Not blocking this doc — flagged for when we move from vision to concrete decisions:

1. **What the MCP control-plane tool surface should actually look like** — which operations get exposed as MCP tools (wake, status, message send, session create?), how stable/versioned that contract needs to be once external systems depend on it, and the concrete design for keeping it a cleanly separate namespace from the self-tools surface hosted agents already use.
2. **A2A protocol's fate** — retire it in favor of the new MCP-primary surface (it has zero real callers and largely duplicates what MCP-as-primary now does), or keep it as a spec-compliance bet for some future non-portfolio external caller that specifically wants A2A.
3. **Which existing subsystems are the first candidates for elimination or consolidation**, given "CLI-wrapping primary, API-harness scoped to durable agents" plus the platform/MCP decision above? The audit surfaces plenty of candidates (five broker/decision layers, three non-interoperating messaging substrates, a third of the schema never exercised, two "reflex" systems, two "workflow" packages) — deciding what actually goes is the next pass, not this doc.
4. **The GUI-native interactive experience** is the one usage mode this audit's evidence base doesn't directly capture — worth a dedicated look (a real GUI walkthrough or a targeted look at genuinely interactive live-DB sessions) before the next planning pass, if that mode's current rough edges matter to the decisions ahead.

## Next step

Per your ask: this is the document to review and correct. Once it reflects what you actually want, the next pass is deciding — informed by both this vision and the system audit — what gets cut, combined, streamlined, or left alone.
