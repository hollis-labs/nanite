# Ideation → Review Workflow — Design Evaluation

**Date:** 2026-04-05
**Scope:** **Design evaluation only. No implementation details.** The goal is to decide whether this should become a first-class Nanite primitive, and if so, what shape it takes at the conceptual level. Implementation choice (workflow YAML vs plugin vs agent vs slash-command chain) is deferred.

**Inputs:**
- User's self-described flow: ideation → technical exploration → planning → execution → review
- `user-workflow-observed.md` (what actually happens in 25 recent sessions)
- Anthropic harness patterns (cluster digests 1-2, 3-4, 5)
- `nanite-architecture-snapshot.md` (what primitives already exist)

---

## 1. Reality check: is this one workflow or many?

**The user's mental model is five phases.** The corpus shows something different.

Observed reality: the five phases exist, but they **live in different sessions**, not in one. Of 25 sampled Nanite sessions:

- Pure ideation sessions: ~10%
- Pure exploration sessions (usually via a spawned read-only subagent): ~15%
- Planning: rarely its own session; mostly 0-2 turns at the tail of ideation or the head of execution
- Execution sessions: ~60% of corpus volume, 1-2 user turns driving 80-300 assistant turns
- Review: rarely its own session; 1-3 turns at the tail of execution

**The glue between phases is the boot prompt**, not in-context conversation. The user updates the boot prompt at the end of a phase, closes the session, and opens a new one on a different phase with a new role.

This changes the design center of gravity:

> The workflow we are evaluating is **a cross-session workflow mediated by shared artifacts**, not a single long-lived agent loop that traverses five phases in sequence.

That framing has implications for every downstream choice, so it goes first.

---

## 2. What each phase actually needs

Per-phase contract: the inputs, the success condition, the terminal artifact, and the primary friction point today.

### Phase 1 — Ideation

- **Purpose.** Convert a vague intuition into a bounded, agreed-upon task list that can be explored or planned.
- **Input.** A user prompt ranging from one sentence to a short paragraph. Often a reference to prior work.
- **Agent posture.** Sketch, clarify, push back, enumerate options. **Explicitly no code.** The observed user says this verbatim: "Do not write code. Produce a findings document."
- **Success condition.** The user says some variant of "yes, write that to the boot prompt" or "let's do this as a task."
- **Terminal artifact.** Entries appended to the boot prompt's `proposed` section and/or a short markdown brief under `docs/` or `docs/research/`.
- **Current friction.** There is no session-type signal that suppresses code emission. The user enforces it with words each time. The first few turns are often spent re-establishing the "this is ideation" frame after auto-injected skill hooks push the agent toward execution.

### Phase 2 — Technical Exploration

- **Purpose.** Replace assumption with evidence. Audit a corner of the codebase, survey an external ecosystem, read source, or produce a comparative analysis.
- **Input.** A bounded research question + scope (corpus, depth tier, output format). The user already answers these 3-5 scoping questions manually every time before spawning a subagent.
- **Agent posture.** Read-only. No edits, no writes except to the designated output file.
- **Success condition.** A finished findings document at a known path, referenced from the boot prompt.
- **Terminal artifact.** `docs/research/<topic>.md` or similar. The main session then distills 1-3 sentences into the boot prompt.
- **Current friction.** Pre-scoping questions are asked from scratch every time. Subagent descriptions are nearly identical across sessions (`Explore:Audit X`, `Explore:Review Y`). Output format conventions are held in the user's head.

### Phase 3 — Planning

- **Purpose.** Convert ideation + exploration findings into an ordered, dependency-aware list of work items with enough specificity that an execution agent can start without further clarification.
- **Input.** The boot prompt (current state), exploration artifacts, any open Engine tasks.
- **Agent posture.** Writer of plans, not code. Order by dependency, call out risks, identify acceptance criteria per item.
- **Success condition.** The user commits the updated plan to the boot prompt and/or a `docs/<feature>-plan.md` doc.
- **Terminal artifact.** Updated boot prompt + optional plan doc. Engine tasks may be created here or may be created inside execution.
- **Current friction.** Most observed planning happens in 0-2 turns embedded in ideation or execution sessions. There is no structured planning artifact separate from prose. Dependencies between tasks are ordered in the user's head. Plan Mode is used 3/25 times — the user has deliberately routed around it.

### Phase 4 — Execution

- **Purpose.** Turn plan items into code + tests + deployed artifacts, one item at a time.
- **Input.** "Boot <role>. TASK: <item from boot prompt>."
- **Agent posture.** Near-autonomous. The observed user's execution sessions average **1.4 user turns** driving **~130 assistant turns**. The user kicks off, goes quiet, and comes back for "looks good / commit."
- **Success condition.** Code compiles, tests pass, Cerberus rebuild succeeds, logs clean. **Not** a self-assessment — it is a ground-truth check.
- **Terminal artifact.** Committed code + an updated boot prompt marking the item `done` + optionally a PR.
- **Current friction.** None that show up as session pain. The friction is cross-session: the boot prompt goes stale because "done" items aren't auto-marked, and the user has to correct the next session's view of reality.

### Phase 5 — Review

- **Purpose.** Verify the execution output against the task's intent, not just its tests.
- **Input.** Diff, running app, Cerberus logs, PR comments from external reviewers (Copilot).
- **Agent posture.** Skeptical. This is where the GAN analogy matters most — review should not be a formality.
- **Success condition.** User either accepts ("looks good, let's commit") or flags specific issues ("the only issue is X…").
- **Terminal artifact.** A decision: merge, iterate, or reject. Plus updates to the boot prompt (done, blocked, or new follow-up tasks).
- **Current friction.** Review has no structured handoff. The execution agent does not emit a "ready for review" signal with a checklist of what changed, how it was verified, and what the user should look at. The user improvises this every time.

---

## 3. Where Anthropic's patterns fit

Mapping each phase to the most load-bearing Anthropic pattern:

| Phase | Primary pattern | Why |
|---|---|---|
| Ideation | Routing (`building-effective-agents`) + session-type specialization | Different posture (sketch vs code) is exactly what routing is for. |
| Exploration | Orchestrator-worker with read-only workers (`multi-agent-research-system`) + effort-scaling rules | Subagents with clear boundaries and required output format. |
| Planning | Planner role from Planner → Generator → Evaluator stack (`harness-design`) | Planning is a distinct role with its own prompt, not a mode of the executor. |
| Execution | Agent loop with ground-truth checks (`building-effective-agents`) + feature-list-as-backlog (`effective-harnesses-for-long-running-agents`) + one-thing-per-loop (`ralph`) | Already how execution sessions work — the user intuitively found this shape. |
| Review | Evaluator-optimizer / generator-evaluator split (`harness-design`, GAN) | Explicit: the agent judging should not be the agent doing. |

**Three cross-cutting patterns apply to all phases:**

- **Structured artifact handoff over in-context continuity** (`harness-design`, `effective-harnesses`). The boot prompt is this artifact, underpowered today.
- **Think tool** (`claude-think-tool`) for mid-loop integration of new observations. Cheapest win, applies in every phase.
- **Events + traces as first-class observability** (`multi-agent-research-system`). Needed so later phases can see what earlier phases did without re-reading them.

---

## 4. Design principles (what any implementation must satisfy)

Derived from the two inputs above. These are the non-negotiables.

1. **Cross-session first.** Whatever we build must treat "this work spans multiple sessions" as the default, not an edge case. Long single-session flows are acceptable but not required.
2. **Boot prompt is the spine.** Every phase reads from and writes to a shared durable artifact. Phases do not pass state to each other through conversation context.
3. **Roles, not modes.** Each phase has a distinct agent posture (ideation, research, planner, executor, reviewer). A single "phase switcher" inside one agent is less legible than five small, separable agent profiles.
4. **The reviewer has different tools than the executor.** At minimum: git diff, test runner, browser verification if applicable. Ideally a slightly different model. Matches `harness-design` directly.
5. **Phases are skippable.** Not every task needs all five. A small bug fix is `execution → review`. A speculative idea might be `ideation → (stop)`. The workflow must be opt-in per phase, not a fixed FSM.
6. **Transitions are explicit, terse, and artifact-driven.** The observed user says "let's proceed" / "looks good" / "let's do a commit." Do not invent ceremony.
7. **Evidence over self-report.** Every phase exit is backed by an artifact the user can inspect: a brief, a research doc, a plan doc, a diff + test output, a review note. No phase completes because the agent says it did.
8. **The harness should shrink as models improve.** Components that become unnecessary should be removable without redesign. (Per `harness-design`: "every component encodes an assumption about what the model can't do on its own.")
9. **Fits existing primitives.** Nanite has agent profiles, workflow engine, plugins, events, slash commands, Engine tasks. The workflow should compose these, not invent a parallel stack. (Concrete implementation mapping is explicitly out of scope for this doc.)

---

## 5. Three candidate shapes (structural comparison only)

Three ways this could live in Nanite conceptually. **No preference declared yet — the point is to make the trade-offs visible.**

### Shape A — Five agent profiles + a convention

Five agent profiles: `ideator`, `explorer`, `planner`, `executor`, `reviewer`. Each with its own system prompt, tool allowlist, and model choice. The user (or a meta-agent) chooses which to boot for each session. A convention — the boot prompt — carries state between them.

- **Pros.** Maximally composable. Nothing new to build at the harness level; the primitives exist. Each agent is simple and audit-able. Matches the observed pattern of per-session specialization. Easy to remove if unused.
- **Cons.** Coordination is manual. Nothing enforces phase order, nothing detects a stale boot prompt, nothing auto-generates the child boot prompt. Entirely depends on user discipline.
- **Key dependency.** Boot-prompt-as-primitive (gap B1). Without that, this is just what we have today with nicer names.

### Shape B — A workflow orchestrator that dispatches across phases

A single "workflow director" agent that tracks which phase a piece of work is in, spawns the appropriate phase agent, monitors its completion, updates the boot prompt, and moves to the next phase. Phase agents are the same five roles as Shape A, but they're invoked by the director, not the user.

- **Pros.** Automates the coordination that the user currently does by hand. Natural place for staleness detection, dependency ordering, "is this ready for review" signals. Matches `multi-agent-research-system`'s orchestrator-worker pattern at the session level.
- **Cons.** More to build. Failure modes are harder (director misroutes, director loses track of a child). Requires cross-session observability, which requires wired events (A1). User loses some fine-grained control unless the director exposes overrides.
- **Key dependencies.** B1 (boot prompts), B2 (parent-orchestrator primitive), A1 (events), all from `gaps-and-opportunities.md`.

### Shape C — Phases as a filter chain over a single-session agent loop

Instead of separate sessions/agents per phase, treat phases as filter-chain stages applied to a single evolving conversation. A session starts in "ideation" mode; a `phase.transition` event flips state; the filter chain swaps the system prompt, changes tool availability, and possibly adds a review turn at the end.

- **Pros.** Single-session UX. No boot-prompt handoff complexity.
- **Cons.** Fights the observed user workflow, which is aggressively cross-session. The user already demonstrated preference: Plan Mode used 3/25 times. Context budgets work against long multi-phase sessions. The reviewer-with-different-tools requirement is harder to satisfy inside one context.
- **Key dependency.** A2 (filter chain with view isolation).

---

## 6. Recommendation for the design decision (not the implementation)

### The shape likely to fit best: **Shape A now, migrate toward Shape B as confidence grows.**

Reasoning:

- **Start with Shape A** because every primitive it needs already exists in Nanite *except* a first-class boot prompt (gap B1). That is a small, high-leverage piece that has its own justification independent of this workflow (user-workflow-observed §"Patterns Worth Formalizing"). Shape A gives us the ideation-to-review discipline as a user convention reinforced by five simple agent profiles, and it is reversible at zero cost.
- **Earn the right to Shape B** by shipping A first, measuring where the friction is (almost certainly in planning and in review hand-offs), and adding the parent-orchestrator primitive (gap B2) only when the manual coordination cost becomes the bottleneck. This matches Anthropic's "strip the harness" posture inverted: *don't build the harness components until the measurement justifies them.*
- **Reject Shape C.** The evidence is unambiguous that the user operates cross-session, and forcing phases into one session fights both the observed behavior and the context budget.

### What gets built, conceptually, in what order

(Order only. No implementation detail per the scope restriction.)

1. **First-class boot prompt primitive** (gap B1). This is the prerequisite for any of the three shapes. Without it, phases have nothing to hand to each other.
2. **Explorer role + `/explore` convention** (gap B3). Already 72% of sessions do this informally; it is the cheapest phase to formalize and it proves the role-specialization approach at low risk.
3. **Ideator and reviewer roles.** Two new agent profiles, each with a short system prompt emphasizing posture. Ideator forbids code; reviewer is skeptical, has diff + test tools, emits a structured review envelope.
4. **Think tool** (gap B4), available to all five roles. Cheapest Anthropic-endorsed win and applies broadly.
5. **Review handoff signal.** The executor agent, upon finishing, emits a structured envelope (files touched, tests run, verification steps performed, outstanding risks). This becomes the reviewer's input. This is the *minimum viable generator-critic* from `harness-design`, without the full iteration loop. (Gap B5, reduced scope.)
6. **Measure.** Run the five-role flow on 3-5 real tasks end-to-end. Watch for which transitions generate friction. Use the existing session JSONL analysis pipeline to see what changed vs the current workflow.
7. **Only then** decide whether to add the parent-orchestrator (Shape B move) or stop here.

### What this explicitly does not propose

- A new DSL, YAML schema, or configuration format.
- Integration with the (currently dormant) `internal/workflow/` engine. That is the subject of gap C2 and should be decided separately.
- A plugin vs core choice for any of the above. That belongs in a build doc.
- Specific agent system prompts. Drafting those is part of the next phase of work.
- Changes to the chat engine loop. All five roles reuse the existing loop.
- Anything about evaluation harnesses. Evals (gap B7) are a separate thread that the workflow will eventually feed into.

---

## 7. Risks and open questions

1. **Stale boot prompts.** Already an observed pain point. B1 must include staleness detection against git log and Engine tasks, or the whole workflow drifts from reality within days. This is the single biggest execution risk.
2. **Reviewer model choice.** Anthropic's results came from using a different model than the generator. Nanite can do this but costs double. When does that cost pay for itself? Probably not on every execution turn. Maybe only on "review" of milestone-sized units of work (a feature, not a commit).
3. **Phase skipping has to feel natural.** If users are forced into a five-phase ritual for every small change, they will bypass it. The five phases must be opt-in defaults that the user picks from, not a prescriptive pipeline.
4. **Review envelope shape.** The current envelope system has known sync fragility (CLAUDE.md warning). Any new envelope type for review handoff must be covered by the contract generator proposed in gap A5, or it will silently drop on the frontend.
5. **Memory interaction.** Phase C memory (gap A4) and this workflow both want to write to durable storage at phase transitions. They should share infrastructure, not build parallel extraction paths. Sequencing matters: memory extraction hooks should be available before the workflow starts using them.
6. **Relationship to the observed parent-orchestrator pattern.** The user already runs meta-sessions that dispatch children. Shape B is essentially *formalizing that*. If we ship Shape A and the user continues to do parent-orchestration by hand, Shape B becomes urgent. If the user stops doing parent-orchestration because Shape A is sufficient, Shape B can wait indefinitely.
7. **Does this become an agent, a workflow, a set of skills, or a plugin?** Deferred to implementation. All four are viable carriers. The answer will depend on what else lands first (workflow engine revival, plugin maturity, agent profile feature richness).

---

## 8. Bottom line

The five-phase workflow is **real** in the user's head and **half-built** in the observed corpus. The biggest contribution Nanite can make is not inventing the workflow — the user has already invented it — but removing the friction that currently makes it hold together by prose and discipline. The most leveraged act is turning the boot prompt into a first-class primitive. Everything else in this document is a variation on that theme.

The design does not need a new subsystem. It needs a small set of additional roles, a structured cross-session artifact, and a handoff envelope between execution and review. Shape A delivers that with existing primitives plus one new one (B1). Shape B is a credible next step if and when the evidence demands it.

**Design decision to be made by the user:** Approve the Shape-A-then-B direction, or push back on any of the premises (cross-session framing, role-specialization, boot-prompt-as-spine, deferred workflow-engine integration) before the next doc — an implementation design — gets written.
