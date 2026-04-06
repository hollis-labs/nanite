# Nanite — Gaps, Opportunities, and Open-Thread Cross-Reference

**Date:** 2026-04-05
**Inputs:** alignment matrix + four digests + user workflow observation
**Purpose:** Actionable backlog, prioritized by leverage × cost × fit with in-flight work. Opinionated. Design-only — no implementation.

Each item identifies:
- **What** — the gap or opportunity
- **Why** — Anthropic pattern(s) or observed-workflow signal that justifies it
- **Leverage** — high / medium / low (expected effect on perceived harness quality)
- **Cost** — s / m / l (relative effort)
- **Thread** — which in-flight effort this should merge into, or "new thread"
- **Risk of not doing it** — what breaks or stagnates if we skip

Items are grouped, not strictly ranked. Within each group they are ordered by leverage.

---

## Group A — Things we are already building, but should reshape based on findings

These are **open threads** in the backlog. The research changes the design, not whether to do it.

### A1. Wire dead events with an eye on the filter chain's final shape (not just the list)
- **What.** Task 1 of `plugin-hooks-events-filters.md` is to wire ~21 defined-but-never-emitted events. Current framing is "add emit calls at the right code points."
- **Why.** `writing-tools-for-agents` and `multi-agent-research-system` both treat structured observability as a first-class primitive — for privacy-preserving tracing, for tool-testing agents, and for eval harness wiring. Anthropic's lesson is: *events are what allow every later layer (evals, filters, safety, memory extraction) to exist.* If we wire events for "plugins can subscribe," we'll quietly miss their second and third uses.
- **Reshape.** Before emitting, agree on the payload shape that satisfies (a) plugin subscribers, (b) eval trajectory recording, (c) memory extraction triggers, (d) the future filter chain's inputs. Otherwise we'll pay for this twice.
- **Leverage.** High. **Cost.** m. **Thread.** plugin-hooks-events-filters Task 1. **Risk.** Shallow wiring now → re-work when evals and memory arrive.

### A2. Filter chain should include a "reasoning-blind gate" position
- **What.** Task 2 of the plugin-hooks-events-filters thread proposes a synchronous filter chain with six entry points (`system_prompt`, `user_message`, `tool_result`, `assistant_response`, `context_window`, `envelope_data`).
- **Why.** `claude-code-auto-mode` is built on a classifier that sees **only user messages + proposed tool calls** — reasoning-blind by design so the safety judge cannot be rhetorically captured. This is exactly what Nanite's 3-state YOLO mode needs if it is to scale past dev-mode use. The filter chain is the right container for it, but only if the chain can *selectively hide fields* from specific filter positions.
- **Reshape.** Add a first-class notion of "filter view" — each filter position declares what subset of context it sees. The `tool.executing` filter should be able to register under a reasoning-blind view that receives user message + tool call, never assistant reasoning. Get this into the interface now, not after a CVE.
- **Leverage.** High. **Cost.** s (design delta, not implementation). **Thread.** plugin-hooks-events-filters Task 2. **Risk.** Chain ships without view isolation → safety classifier plugin is impossible to add later without a breaking change.

### A3. Shell feature: revisit YOLO-toggle vs sandbox-first model
- **What.** Shell feature plans `ask / session / yolo` as modes + denylist (`shell/shell.go:9-42`, `shell/denylist.go:7-79`).
- **Why.** `claude-code-sandboxing` explicitly argues that *any* approve-each-command UX leads to approval fatigue and rubber-stamping, and that dual filesystem+network isolation is the right replacement. Nanite's current plan inherits the approval-fatigue problem in `ask` mode and the blast-radius problem in `yolo`.
- **Reshape.** Not necessarily "drop YOLO." But the *default* session mode should be closer to Anthropic's sandbox model: the shell runs inside an OS-level FS+network boundary (CWD + proxy-allowlisted network), which makes the denylist a second line rather than the first. `yolo` becomes "disable the sandbox," not "disable the denylist." Also: dev-mode today is single-user local, so the sandbox can start as a documented convention (`cd` into a scoped dir) before becoming real bubblewrap/seatbelt.
- **Leverage.** High. **Cost.** m–l depending on sandbox depth. **Thread.** shell feature Task 1 + Task 2. **Risk.** Ship shell exec with the approval-fatigue UX baked in, then have to rewrite when multi-user or cloud variants appear.

### A4. Phase C memory should use `extraction at compaction` + lightweight per-turn hooks
- **What.** Phase C investigation recommends a single `memory` type with subtypes, extraction at post-compact and per-turn. Still in design.
- **Why.** `multi-agent-research-system` externalizes memory precisely when approaching context limits, and `harness-design` advocates context resets with structured handoff. Per-turn extraction (lightweight: preferences, corrections, decisions) + compaction extraction (rich, structured) matches both posts. The Phase C doc already proposes this; the risk is quietly dropping per-turn in favor of compaction-only to ship faster.
- **Reshape.** Keep both triggers. Make per-turn extraction a filter-chain subscriber on `assistant_response` / `user_message` so it composes with A1/A2. This also means the memory extractor is just another plugin once the infra lands, not a special case.
- **Leverage.** High. **Cost.** m (the infra is mostly A1+A2). **Thread.** Phase C. **Risk.** Memory becomes a bolt-on; extraction logic ends up duplicated with other cross-cutting work.

### A5. Plugin extraction Phase 5-6 benefits from an envelope-contract generator
- **What.** Plugin extraction phases 5-6 pull connector plugins + remaining envelope types out of core.
- **Why.** `writing-tools-for-agents` treats tool descriptions as onboarding docs and recommends example-driven schemas. The same standard should apply to envelope contracts, which are currently sync-fragile (CLAUDE.md explicitly warns about silent drops). A plugin that ships an envelope type should ship a typed contract + test case that validates backend payload ↔ frontend shape automatically.
- **Reshape.** The plugin generator (`nanite plugin new --with-envelope`) already exists; extend its envelope scaffold to generate a JSON-Schema contract + a Go test + a TS type import in one go, so extracted plugins leave no manual-sync gap.
- **Leverage.** Medium. **Cost.** s. **Thread.** plugin extraction. **Risk.** Each extracted plugin inherits envelope-sync fragility; bug surface grows with each extraction.

---

## Group B — New threads worth opening

These are not on any current roadmap. Ordered by leverage.

### B1. First-class "boot prompt" primitive (cross-session plan artifact)
- **What.** Promote the boot prompt from "user convention in `.agentrc/boot-prompt.md`" to a harness primitive with: a schema (proposed / in-flight / done / blocked sections, per-role variants), staleness detection against git log and Engine tasks, commands to update it from execution outcomes, auto-loading at session start.
- **Why.** User-workflow observation: **84% of sessions open with "Boot <role>"**, 21 of 25 sampled sessions. It's the single highest-leverage artifact the user uses, and it is entirely unmanaged. Matches `effective-harnesses-for-long-running-agents` (progress file) and `ralph` (`fix_plan.md` + `AGENT.md`). Matches `harness-design`'s "structured handoff through artifacts, not conversation compaction."
- **Leverage.** **Highest on the entire list.** **Cost.** m. **Thread.** New. Natural sibling to Phase C. **Risk.** The most-used thing in the user's workflow stays brittle and out-of-band forever.

### B2. Parent-orchestrator session primitive
- **What.** A session type whose purpose is to dispatch and monitor N child sessions running in parallel, with structured visibility into each child's state (running / awaiting input / done / blocked) and boot-prompt-fragment generation for each child.
- **Why.** User workflow shows `02a8cedd` and `575ddd42` as canonical "meta-orchestration" sessions where the user relays state between children in prose. Mentat already has `Boot orchestrator`. This is the multi-session equivalent of `multi-agent-research-system`'s orchestrator-worker pattern — Nanite has intra-session delegation (`orchestrator.go`, `delegate.go`) but nothing for cross-session.
- **Leverage.** High. **Cost.** m–l. **Thread.** New. Depends on B1 (boot prompts as shared state) and A1 (events to observe child lifecycle). **Risk.** The user's most distinctive working style stays impossible to formalize.

### B3. Exploration subagent as a named primitive (`/explore`)
- **What.** Named skill/command that enforces: read-only scope, mandatory `docs/research/<topic>.md` output path, pre-asks a fixed set of scoping questions (corpus, depth, tier, output format), auto-references the result file from the active boot prompt on return.
- **Why.** 72% of sampled sessions spawn an Explore subagent. Spawn descriptions are nearly identical across sessions (`Explore:Audit X`, `Explore:Review Y`). The user already performs the pre-scoping ritual manually (see today's ideation session — 5 questions answered before dispatch). Formalizing saves a turn per session and enforces the pattern.
- **Leverage.** Medium-high (frequency × friction). **Cost.** s. **Thread.** New. Low-risk standalone. **Risk.** The most-used subagent pattern stays freestyle and inconsistently scoped.

### B4. Think-tool as a Nanite-native tool
- **What.** Expose a `think` tool to agents per `claude-think-tool`: no-op scratchpad, LLM calls it mid-loop to "integrate new information into the plan." Ship with (a) a default domain-neutral system-prompt block explaining when to call it, (b) per-agent override in agent YAML so specific agents can customize the completeness checklist.
- **Why.** Benchmark gains cited in the post are large (τ-bench airline 0.332 → 0.570, 54% relative) in policy-dense domains. The Nanite chat engine has exactly the shape of loop the think tool was designed for. Cost is trivial — it's a no-op tool definition + a prompt block.
- **Leverage.** Medium. **Cost.** s. **Thread.** New. **Risk.** Missing a free win.

### B5. Generator/critic pair as a first-class workflow shape
- **What.** A workflow primitive where a `generator` agent produces an artifact and a `critic` agent — with different tools, possibly a different model, and its own system prompt emphasizing skepticism — evaluates and returns structured feedback. Bounded iterations (3-5) with a "graduation" hook when the critic passes.
- **Why.** `harness-design` is explicit: separating the agent doing the work from the agent judging it is "a strong lever." The GAN analogy reinforces this structurally. The user's observed review phase is *already* this pattern in degenerate form ("looks good" / PR Copilot feedback) — we have evidence it fits the workflow.
- **Leverage.** High (this is the skeleton for the ideation-to-review workflow design). **Cost.** m. **Thread.** New, paired with the ideation-to-review workflow design doc. **Risk.** The ideation-to-review workflow stays a mental model, never a primitive.

### B6. Tool-testing agent as a scheduled job
- **What.** A recurring job that exercises each tool in the registry against held-out test prompts and records pass rate + token cost + description quality. Outputs surfaced as a dashboard and as hints for auto-description-rewrite suggestions.
- **Why.** `multi-agent-research-system` reports a **40% reduction in task completion time** from running a tool-testing agent against their tools. Nanite already has ~16 built-in tools + a growing MCP catalog — tool quality drift is inevitable. This is also the seed of the eval harness (see B7).
- **Leverage.** Medium. **Cost.** m. **Thread.** New, later. **Risk.** Tool-quality regressions stay silent.

### B7. Start an eval harness bootstrapping from real session transcripts
- **What.** Following `demystifying-evals-for-ai-agents`: mine 20-50 tasks from the user's recent session transcripts (the workflow-observation agent already proved this is possible). Each task has a recorded outcome, tool-call trajectory, and end-state. Start with outcome grading and transcript-level metrics (turns, tokens, latency). No LLM-judge yet.
- **Why.** Nanite has zero agent-level evals today. The sessions already exist, the outcomes are already committed to git, the trajectories are already in JSONL. Bootstrap cost is unusually low.
- **Leverage.** Medium initially, compounding over time. **Cost.** m. **Thread.** New, later. **Risk.** Every future model/tool/prompt change is flown blind.

---

## Group C — Things to consider stopping or simplifying

Anthropic's "simplicity-first, strip harness components as models improve" posture from `harness-design` and `building-effective-agents` suggests we should at least audit what's already complicated.

### C1. The `Engine` god-object is a pre-existing anti-pattern; use A1-A4 as a forcing function
- The backend anti-patterns doc already names this (`engine.go` 1760 lines). Don't refactor for its own sake, but as A1 (events), A2 (filter chain), and A4 (memory hooks) land, each should *pull* its concern out of `engine.go` rather than adding to it. If A1-A4 are done well, the Engine shrinks naturally.
- **Leverage.** Medium. **Cost.** distributed across A1-A4. **Thread.** existing. **Risk.** Cross-cutting concerns keep accreting in one file.

### C2. Workflow engine: decide whether to integrate or retire
- `internal/workflow/` is defined but not triggered from the chat loop. It is in a "zombie" state. Either:
  - (a) Integrate with the chat loop as the substrate for B5 (generator/critic) and the ideation-to-review workflow, *or*
  - (b) Explicitly retire and delete until a concrete use case demands it.
- Current posture (defined, untested, unused) is the worst of both worlds.
- **Leverage.** Low-medium (removes ambiguity). **Cost.** s (for the decision), m (for either direction). **Thread.** New decision. **Risk.** Engineers keep tripping on it; code rots in place.

### C3. Harden-as-deferred vs delete-as-YAGNI on the 21 dead events
- Right now the dead events are a TODO with a clear owner. Good. But some of them may be speculative — `config.changed`, `plugin.installed/uninstalled`, `workflow.started/complete/failed` — and worth auditing for *real* subscribers before we invest in wiring. Anthropic's "strip the harness" principle says: wire events we know will be consumed within the next two phases; delete the rest or mark them explicitly as `planned-v2`.
- **Leverage.** Low. **Cost.** s. **Thread.** plugin-hooks-events-filters Task 1. **Risk.** We emit events no one reads forever.

### C4. Progressive disclosure threshold (5 tools) is probably too aggressive
- The `advanced-tool-use` post suggests Tool Search Tool overhead only pays off at *many* tools; below ~10, loading them all is cheaper than the discovery round-trip. Nanite triggers progressive disclosure at 5. Worth running a small measurement before moving the number, but the default is likely wrong.
- **Leverage.** Low. **Cost.** s. **Thread.** tool system (no active thread; could piggy-back on A5). **Risk.** Paying the discovery cost when we don't need to.

---

## Group D — Things Anthropic says that don't apply (yet) and shouldn't distract

Named explicitly so they don't become shiny-object threads.

- **Code execution with MCP / Programmatic Tool Calling.** Huge token savings in Anthropic's post, but requires a hardened code-execution sandbox. Nanite's current sandbox is not that. Parking until the shell+sandbox thread (A3) resolves — at which point this becomes straightforward.
- **Infrastructure noise / container headroom.** Relevant only if Nanite starts hosting eval runs (see B7). Until then, N/A.
- **Rainbow deployments / production tracing.** Production deploy concerns. Nanite is dev-local. Revisit when a hosted variant appears.
- **Eval-awareness / benchmark leakage.** Not relevant until evals ship.
- **Contextual Retrieval (chunk preamble, hybrid BM25+embeddings).** Delegated to Cortex. Currently blocked on Cortex's embedding provider — that is *Cortex's* problem to solve, and the A4 memory design should not assume a timeline.
- **Web-variant git credential proxy.** Not in scope.
- **Ralph's literal bash while-loop.** Keep as a *reference implementation* for a future "long-horizon autonomous mode" but do not copy directly. The observed user workflow is much more interactive than Ralph assumes; and Ralph's honest failure modes (greenfield-only, non-deterministic search, $50k claim hides months of operator tuning) make it unsuitable as the default loop.

---

## Priority Rollup (opinionated top six)

If we do nothing else in the next phase, these are the highest leverage actions, in order:

1. **B1 — First-class boot-prompt primitive.** Highest-use artifact in the observed workflow, zero harness support.
2. **A1+A2 together — Wire events *and* design the filter chain with a reasoning-blind view from day one.** This unblocks memory extraction (A4), safety classifiers, eval recording, and the parent-orchestrator primitive (B2) in one pass.
3. **A4 — Phase C memory using the A1/A2 infra as its plumbing.** Avoids a one-off extraction path.
4. **B5 — Generator/critic workflow primitive.** Spine of the ideation-to-review workflow.
5. **A3 — Shell feature: sandbox-first model, denylist as second line.** Fix the UX direction before the feature ships.
6. **B4 — Think tool.** Free win. Ship alongside any of the above.

B2 (parent-orchestrator), B3 (`/explore`), B6 (tool-testing), B7 (evals) are the natural second wave after these six land.

---

## What this document does not do

- It does not propose implementation. The workflow design is in `ideation-to-review-workflow-design.md`.
- It does not rank items against each other across groups. Leverage/cost/risk are signals, not a total order.
- It does not assume the user agrees with every item. This is a research synthesis; decisions happen in discussion.
