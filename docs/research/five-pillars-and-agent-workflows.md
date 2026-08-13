# Nanite: Five Pillars & the Agent Workflow Gap

Status: exploratory — captures a strategic discussion, not a design doc or implementation plan. No code changes resulted from this session.

## Why this came up

Nanite began as an effort to make local coding agents (Claude, Codex, etc.) easy to use from one GUI. That specific problem — a unified launcher for CLI agent tools — is now well-served by other apps, which opened room to think about Nanite's role more precisely. Two things pushed this:

1. Deeper exposure to agent frameworks (LangChain, LangGraph, Google ADK) at work, raising the question of whether Nanite should adopt one, build an equivalent, or stay out of that business.
2. A recurring suspicion that CLI coding tools will stay programmatically limited, making direct LLM API access the more durable long-term integration path.

Underneath both: Nanite's four original informally-separated concerns — **GUI, Harness, Launching, Agents** — turned out to already be partially real in the code, unevenly so. This session mapped the current state, pressure-tested it against two sibling apps (Hadron, Torque) that independently explored adjacent territory, and landed on a fifth pillar.

## The five pillars

| Pillar | What it owns | Current state in Nanite |
|---|---|---|
| **GUI** | The chat product surface — sessions, messages, envelopes, transcript, composer | Clean, unambiguous. React SPA + `internal/api/{sessions,messages,...}`. |
| **Harness** | Turn orchestration: context assembly, tool selection/execution, streaming, dispatch to Worker/Planner | **Tangled with GUI** — both live in `internal/service` (170 files, no package boundary between "run this turn" and "manage this durable agent" and "bridge into launching"). Also used at two altitudes in the codebase: the internal turn-loop, and the separate external `/api/harness/v1` control-plane API — worth deciding which one the name refers to if it becomes a formally named layer. |
| **Launching** | Spawning/managing the actual agent process or API call | **Cleanest pillar today.** `internal/runtime/agent.Boot` is a narrow, well-bounded package, pluggable per provider. Proven separable: a standalone `nanite launch` CLI runs with zero GUI/harness, and sibling apps Tether and Torque already consume the same `agentkit`/`agentlaunch` contract. Not something that needs to be built — it already behaves like a shared, independent service. |
| **Agents** | Persona/capability (system prompt, tools, skills) *and* durable continuity (persistent identity, lifecycle class, wake/resume across sessions) | Conceptually real (`store.AgentProfile`, durable-agent lifecycle classes) but the word "agent" is overloaded across five distinct types/packages in the code — worth a deliberate naming pass if this becomes a formally named pillar. Durable continuity is explicitly **not** about what actions an agent is allowed to take — that's a different axis, which is why it doesn't cover the fifth pillar below. |
| **Workflow** *(internal name)* — branding candidate: **Agent Workflows** | Whether and how rigidly a sequence of actions is enforced, independent of persona and continuity | **New pillar identified this session — does not exist as a concern in Nanite today.** See below. |

## The Workflow pillar

### What it's for

Without it, Nanite's harness always trusts the LLM's own judgment about whether to call a tool, in what order, and whether its self-reported outcome is true. That's fine for open-ended chat, but it breaks down for the "many agents with specific, rigid roles in a pipeline" pattern — the concrete trigger was an agent asked to fetch tasks from Torque instead fabricating them, because nothing in the harness made the fetch mandatory or checked the claim.

Workflow is explicitly **not** a replacement for open-ended chat behavior. Constraints are opt-in per workflow; an unconstrained workflow should degrade to exactly today's behavior. The goal is to make rigidity available, not default.

### Two distinct problems, not one

Investigating prior art (below) surfaced that "add a DAG/workflow layer" actually bundles two separable problems:

1. **Deterministic sequencing** — forcing *which* step happens *when*, regardless of what the LLM would have chosen on its own. This is what LangGraph / Google ADK's Sequential-Parallel-Loop workflow agents are mainly selling, and it's the half most people mean by "agent framework."
2. **Trustworthy completion** — not trusting what an agent *says* it did. Sequencing alone doesn't fix this: an agent can still be sequenced correctly through a step and still lie about the result of that step.

Nanite's dispatch/role model (Chat/Worker/Planner, envelope-mediated handoff — worker output never enters Chat's context raw) is already a partial answer to problem 2. Neither problem currently has a dedicated, general solution in Nanite.

### Architecture approach

Adapter pattern — the same shape Launching already uses successfully (`Dependencies.ProviderAdapter(name) provider.CLIAdapter`, a thin resolver with per-provider implementations behind it). Concretely: a minimal built-in sequencer + verification-hook implementation to start, with the same seam open to drop in a full external framework (LangGraph, Google ADK) when a workflow needs cycles, complex state graphs, or ecosystem tooling the built-in version doesn't cover. This keeps near-term work small, defers the "build vs. adopt" decision on the framework itself, and gives users maximum flexibility without forcing a single dependency.

## Prior art already in the portfolio

Two sibling apps (same `hollis-labs` parent as Nanite) independently explored adjacent territory. Both were reviewed specifically to harvest ideas for Workflow's built-in implementation — not to be adopted wholesale.

### Hadron — DAG-style automation runner (answers the sequencing half)

A local-first blueprint automation daemon (`hadrond`), originally generic dev-automation tooling with AI-agent step kinds bolted on very recently. Active beta, paused mid-development (unrelated to the tool itself), single contributor.

**What's real and worth taking:**
- A genuine DAG with cycle detection and real parallel fan-out/fan-in — but at the **pipeline/stage** level (topological levels of a dependency graph, `sync.WaitGroup`-based), where each stage is one entire blueprint run.
- Within a single blueprint, steps are a flat sequential list — no step-level graph, no parallelism.
- The core idea worth keeping: `http_call`/`mcp_call` steps are **engine-executed, not LLM-executed** — the graph itself makes the call, and the literal result is what downstream logic sees. An LLM inside the workflow can't fake having made that call, because making it was never the LLM's job. This is the sequencing primitive to borrow.

**What's missing / not worth inheriting directly:**
- No step-level DAG (wrong granularity for composing individual agent sub-steps).
- No cycles/loops anywhere, even at the pipeline level (actively rejected) — a real gap versus retry-until-done or supervisor-loop patterns common in agent workflows.
- No structured, typed state passed between steps — data flows via log-line scraping (`::set-output`), the filesystem, or a mailbox.
- No output/schema validation on any step.
- The one step kind that actually runs an LLM subtask (`agent_launch`/`message_wait`) has **zero guardrails** on the agent's self-reported reply — ironically the least-protected part of the system, and the part that would need to be trustworthy for this use case.
- Not crash-durable: a daemon restart orphans in-flight runs, including ones parked at a human-approval gate.

**Verdict:** right philosophy (engine owns the call, not the LLM), wrong grain and too immature to build Workflow's execution engine directly on top of. Remains valuable as what it already is — a separate, coarser-grained local automation/scheduling/audit tool — not as Workflow's foundation.

### Torque — task orchestration runtime (answers the trust half)

Shares the same `agentkit`/`agent.Boot` launch pattern as Nanite (further confirming Launching is already a validated, cross-app shared capability, not something to rebuild). Torque's actual contribution here is orthogonal to Hadron's: it's the strongest existing answer in the portfolio to "how do you stop an agent from just claiming success."

**The real guardrail is three layers, and the first is the standout idea:**

1. **Capability restriction over claim-checking.** The worker agent's own MCP tool surface has no `done`-setting tool at all — it can only call `review` or `blocked`. Self-reporting completion isn't validated after the fact, it's simply not a representable action for the worker. This is a cleaner solve than checking a claim: don't hand out the tool that lets the claim be made.
2. **Engine-owned, non-LLM verification** on top of that — a check (`VerifyWorkerCompletion`) independently inspects git commit count and the session's own tool-use log, and can override a self-reported status if the evidence doesn't support it.
3. **A second, independently-spawned reviewer agent** (fresh process, broader tool surface, the only one actually allowed to set the terminal state) audits the work before it's accepted.

**Known, deliberate gaps** (confirmed, not a surprise to the team): this three-layer rigor currently applies only to the primary `kind=agent` execution path. An older, simpler mechanical-task path (`ModeOneShot`) trusts exit-code-equals-success with no verification — it was a first-pass workaround, and `kind=agent` is explicitly intended to replace it long-term as validation matures. Notably, the reviewer agent's *own* completion currently falls into that weaker unverified path — the auditor isn't audited yet. A `QualityGates` field is fully modeled in the schema but never read anywhere in the scheduler — modeled, not enforced.

Also real but not the trust mechanism the tool names suggest: Torque's checkpoint tools (`torque_task_checkpoint_*`) look like a mandatory gate but are actually optional, agent-discretionary "ask a human" requests — not something that blocks completion by default.

**Verdict:** the strongest prior art for Workflow's trust half. "Restrict the capability rather than validate the claim" is the idea most worth carrying forward, layered with engine-owned verification as an escalating (not mandatory-by-default) option.

## Where this leaves things

- Five pillars adopted as the mental model: **GUI, Harness, Launching, Agents, Workflow.**
- Workflow is the internal engineering name; **Agents** or **Agent Workflows** are candidates for external/product naming, kept deliberately separate from the internal taxonomy.
- Workflow's built-in implementation should borrow Hadron's engine-owned-step idea for sequencing and Torque's capability-restriction-plus-independent-verification idea for trust, behind an adapter seam that allows LangGraph/Google ADK (or others) to be dropped in later for cases needing cycles or richer state-graph semantics.
- API-first LLM access (vs. CLI-subprocess launching as the default) is a live, comparatively low-risk pivot to consider separately — Nanite already runs a parallel direct-API path today (`internal/llm/anthropic`, `internal/llm/openai`), so promoting it to primary is a shift in default, not a green-field build. Not decided this session; flagged as a related but separate question from the Workflow pillar.
- Not addressed this session (open for later): formally untangling Harness from GUI inside `internal/service`, resolving the five-way "agent" naming overload in the code, and any concrete implementation shape for Workflow's adapter seam.

## Follow-up: API-first needs a runtime that doesn't exist yet

Follow-up session, same day. Two threads resolved:

**Persistence, clarified.** "Persistent agent" bundles two different things: *identity persistence* (durable-agent config/history/memory, already owned by the harness independent of launch mechanism — two of the four lifecycle classes already cold-start per wake with no long-lived process at all) and *process persistence* (an OS-level resident process holding in-memory state across turns, which today only a CLI subprocess provides). Dropping CLI launching as the default doesn't threaten identity persistence. It does remove the thing currently providing process residency — which means an API-first default needs its own resident runtime to replace that, not just a switch of which client library makes the model call.

**That runtime doesn't exist in the portfolio.** Went looking for a previously-discussed "own runtime, call APIs directly" library, suspected to live in `libs/`. Checked `go-agent-runtime` (real, compiles, tested — but is CLI-subprocess support tooling: boot-dir writing, Codex JSON-RPC framing, Claude NDJSON framing; its `"api"` runtime-kind is an unimplemented enum value, absorbed into `agentkit` 3 days after its last commit with no change in scope) and the adjacent `go-agent-wrapper` (same CLI-subprocess concern, more mature) and `go-agent-context` (boot-prompt assembly, unrelated). None of them is it.

Clarified this isn't a case of losing/misplacing a direct-API path: `go-providers` did once hand-roll HTTP clients for LLM providers, and that was deliberately removed in v0.10.0 in favor of using official vendor SDKs directly — a library-hygiene decision, not a retreat from API access. The memory of "we were ready to build this" may also be crossed with work done on **Tachyon** (a separate user-facing interactive CLI agent launcher) rather than something committed here.

**What exists to build on:** `go-llm-types` (transport-agnostic request/response/tool/stream-event structures) and `go-llm-contracts` (`Provider` interface, rate-budget primitives) look like the intended seam but have no transport code by design. Nanite's own `internal/llm/anthropic`/`internal/llm/openai` are the best working reference — real, in-app, backing "API Chat" sessions today, just never extracted into a shared lib.

**Initial decision (superseded below):** build it as a new standalone lib, portfolio-shared like `agentkit`. Filed as Torque task `CW-20260812-0001`.

## Follow-up: could Nanite's own CLI just be the runtime? (revises the above)

Same-day follow-up. Nanite is closer to "own runtime, multiple surfaces" than the standalone-library plan assumed — `/api/harness/v1` already exists specifically as a GUI-agnostic control plane ("thin over the same session, message, SSE stream, cancel, approval, and durable-agent services used by the built-in React UI"), `internal/launcher` already proves headless operation for Launching, and the resident API-execution path (`internal/llm/anthropic`/`internal/llm/openai`) already works — it's just only reachable through a browser today. So the real gap was narrower than "build an execution engine from scratch": give Nanite a CLI/TUI client of its own harness, the same way Opencode separates one runtime from its TUI/GUI/CLI surfaces.

**Verified before committing to this:** confirmed via `go.mod` + source audit across all four apps that Hadron, Torque, and Tether have zero cross-app dependencies today — only shared `libs/*` packages, no imports of each other's or Nanite's code, no hardcoded references to each other's services. That invariant (shared libs and patterns, never a live runtime dependency between apps) is real and holds except in one place: Nanite's own `go.mod` has a direct source import of `github.com/hollis-labs/tether/pkg/claudestream` (Tether's own module, not a `libs/` package) via `internal/muxproxy`, left over from an experiment. Filed as its own cleanup, `CW-20260812-0002` (Nanite project): extract the Tether integration into an opt-in plugin, since any sibling-app integration should be optional and pluggable, never compiled into core — matches how `go-tether-client` already works (a client for an optional service, not a requirement).

**Revised decision:** `CW-20260812-0001` updated (retitled "Give Nanite its own API-based agent runtime (CLI-accessible, harness-hosted)") to build this *in* Nanite — a CLI/TUI client of Nanite's existing harness — rather than as a portfolio-wide shared library. Moved to the Nanite project (`PRJ-20260417-0002`) to match. Torque/Hadron/Tether are not expected to depend on it, ever, by default; each keeps using its own independent `agent.Boot`/`agentkit`, and each stays free to pick whatever API runtime fits — Nanite's, Opencode's, a model vendor's own API mode (most already provide one), or something bespoke. If another app ever wants Nanite's runtime specifically, that's an opt-in client it adds itself, the same pattern just re-established for the Tether cleanup — never a required dependency. Both tasks created `manual=true` per Torque's dispatch safety default — parked in `todo` until explicitly promoted.

**Boot prompts prepared for both open tasks** (`CW-20260812-0001` and a newly-filed `CW-20260813-0001`, "Add Agent Workflows" — the Workflow pillar work above, which hadn't been tracked in Torque until this point, filed in the Nanite project) — self-contained prompts for a fresh worker session, each pointing at its task ID as source of truth plus this doc for the reasoning trail, with the non-negotiable constraints repeated inline so a fresh session doesn't drift from them. Work on `CW-20260812-0001` had already started by the next session (branch `feat/api-agent-runtime-cli-CW-20260812-0001`, in progress).

## Follow-up: A2A protocol for agent-to-agent messaging

Follow-up session, next day. Question: should the portfolio's agent-to-agent messaging adopt the open A2A (Agent2Agent, a2a-protocol.org, Linux Foundation, v1.0) standard, with each app's own opinionated behavior layered on top — using Nanite's "wake on message" feature and Tether's messaging as the concrete cases to reason from.

**What A2A is:** deliberately agent-to-agent (not agent-to-tool, that's MCP's job) — stateful, multi-turn, agents negotiate directly rather than being wrapped as tools. Core vocabulary: **Agent Card** (capability/discovery document at `/.well-known/agent-card`), **Task** (real lifecycle: `submitted`/`working`/`completed`/`failed`/`canceled`/`rejected`/`input-required`/`auth-required`), **Message**, **Artifact**. Transport is JSON-RPC over HTTP; `sendMessageStream` for async work via SSE.

**Push notifications, resolved (the one open question from the prior pass):** A2A has a real webhook mechanism, distinct from SSE. `PushNotificationConfig` (`url` required, optional `token`/`authentication`) registers a callback; the server POSTs to it on task state change. Explicitly designed for **fully disconnected clients** — spec names mobile/serverless clients as the motivating case, i.e. it does not require an open connection. Optional, gated by `capabilities.pushNotifications` on the Agent Card. No documented retry/delivery guarantee.

**Precise mapping onto Nanite's wake-on-message gap** — two distinct pieces, not one:
1. **A2A task submission is the wake trigger itself.** An inbound task targeting a durable agent instance is exactly the (currently unwired) `DurableAgentWakeExternalMessage` case — no notification round-trip needed for this half.
2. **Push notifications are how the caller learns the outcome later**, without holding a connection open through however long waking + working takes.

Given no delivery guarantee is documented, push notifications should be treated as a best-effort accelerant over a durable record, never the source of truth — this matches the philosophy Tether's own `/messages/notify` already uses ("storing the message is authoritative; wake errors are returned in-band").

**Current-state findings, portfolio-wide:**
- `go-messaging` (shared by all four apps) is ~1 month old, pre-1.0, already version-skewed (Nanite/Tether on v0.2.1, Hadron/Torque on v0.3.0), pull/polling-first (`Send`→`Inbox`-marks-delivered→`Consume`; `Subscribe` is best-effort, drops messages, no replay), with no Agent Card/capability-discovery concept and no Task/lifecycle concept — just message-level request/response correlation. No documented rationale anywhere for building custom instead of adopting a standard.
- **Hadron already independently built a partial A2A implementation** — a real `AgentCard` at `/.well-known/agent.json` and A2A-shaped task states — polling-only, `PushNotifications` hardcoded `false`, entirely disconnected from `go-messaging` inside the same app. Real signal from the team's own practice, not just outside argument.
- **Nanite's "wake on message" isn't actually built.** `DurableAgentWakeExternalMessage` is a reserved enum value with zero callers; existing mechanisms (a reflex that nudges an *already-running* session, Tether's notify-injects-a-turn-into-a-live-session) can't start a sleeping/stopped agent. Nanite's own backlog already lists A2A as a candidate wake trigger. What's already built and fully decoupled from trigger source (so none of it needs to change): lifecycle-class session-continuity policy, wake idempotency guardrails, recovery-aware resume, full audit trail, reflex governance.
- **Tether's own docs explicitly say "Tether is the runtime, not the orchestrator."** Its messaging is intended as portfolio-wide shared infrastructure (the plumbing exists — `go-tether-client`'s `AsStore()`, Tether's federation `Router`) but nothing exercises it in production; every app runs its own independent local store. Tether's registry (`tether_registry_*`) is the closest thing to an Agent Card in the portfolio today (`Capabilities`/`Skills`/`Links` fields) but is centrally-populated by an importer, not self-published per-agent the way A2A's model works. Tether already implements **ACP** (a different, sibling standard — editor↔agent, not agent↔agent) and is deliberately hosting/stabilizing it there before other apps adopt it — real precedent for "one app stabilizes an external protocol first."
- **A relevant, already-completed body of work exists in Torque**: epic `CW-20260518-0038` "Torque Messaging — federated agent/user communication" (done) — full federation design, cross-host auth, Nanite/Tether adoption, end-to-end test. Two follow-ups are still open: `CW-20260519-0069` (agent address self-discovery — directly solved by adopting Agent Card discovery instead of guessed URN conventions) and `CW-20260519-0070` (federation live bring-up — mTLS, peer registration). Recommended holding `CW-20260519-0070` until the A2A decision is made, to avoid standing up live operational config for a shape that may change shortly after.
- Historical note: Nanite's messaging tables were originally named `a2a_messages` before a deliberate rename to `agent_messages` (`CW-20260417-0320`, done) — this isn't a fresh direction, it's revisiting ground already touched once.

**Decision:** filed as Torque task `CW-20260813-0002` ("Adopt A2A protocol for agent-to-agent messaging across the portfolio"), Hollis Labs Portfolio project, referencing and coordinating with (not duplicating) `CW-20260518-0038`/`CW-20260519-0069`/`CW-20260519-0070`.

## Follow-up: CW-20260812-0001 implemented and merged

Follow-up, same week. `CW-20260812-0001` ("Give Nanite its own API-based agent runtime (CLI-accessible, harness-hosted)") shipped: `nanite chat`, a CLI REPL client of the existing `/api/harness/v1` control plane, merged to `main` via #221. Talks to an already-running `nanite serve` process over HTTP+SSE rather than embedding a second in-process runtime — `internal/store` is single-connection/writer-only, so a second OS process touching the DB directly would have risked contention with the running service; harness-v1 already existed for exactly this purpose (versioned, documented, tested) with zero prior Go clients.

Smoke-testing the new CLI end-to-end against a real running service surfaced three unrelated, pre-existing bugs, fixed in the same PR:
- A race in `streamMessageEvents`'s turn-ownership check (read the async-persisted `Message` row instead of the synchronously-registered `StreamManager` entry — any fast harness-v1 client, including this one, could see a false 404).
- A retired Anthropic model id (`claude-sonnet-4-20250514` — live-current as of the seed-data-staleness audit referenced earlier in this doc, retired by Anthropic sometime after) baked into the seeded default.
- `resolveProvider` never consulted `user_settings.default_provider`, so a session with no explicit provider fell through to a stale, CLI-shaped `provider_fallback_chain` entry — silently routing ordinary API sessions into a CLI boot attempt that crashed on an empty provider string. Fixed the precedence and, per explicit product decision, removed the old unconditional "fall back to anthropic" catch-all: an unconfigured installation now errors instead of silently defaulting.

`CW-20260812-0002` (the Tether-integration cleanup filed alongside this) and `CW-20260813-0001`/`CW-20260813-0002` (Workflow pillar, A2A) remain open, tracked separately.
