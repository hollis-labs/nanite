# Nanite Agent System — Target Architecture

**Date:** 2026-08-18
**Status:** Locked target design for the agent-critical subsystems, following a full-day architecture alignment review. Supersedes the relevant sections of `docs/release-vision.md` and the three `docs/alignment-review-*.md` passes where they conflict with decisions made here — those documents remain useful as the evidence trail and reasoning history, not as the current target.
**Companion:** `docs/architecture-agents-tasks-2026-08-18.md` — the concrete, sequenced task breakdown that implements this document. This file describes *what* the system should look like; that one describes *how to get there and in what order*.
**Full decision history:** `docs/architecture-decision-log-2026-08-17.md` — every decision below traces back to a dated entry there with its rationale and verification detail.

## 1. Scope

This document covers the four subsystems that make up "an agent" end to end, in the order they were reviewed:

1. **Construction** — how an agent gets defined.
2. **Launching** — how a defined agent becomes a running process or in-process turn.
3. **Steering** — how the system decides what an agent does moment to moment.
4. **Harness** — the shared turn-loop mechanics every launched agent runs through.

Everything here is scoped to what's genuinely load-bearing for agents specifically. The audit's other subsystem docs — session-lifecycle-recovery (beyond the two recovery items carried into §5), envelope-system, plugin-system, the storage layer generally, inter-agent-messaging (beyond the subagent-reply fix already shipped) — are explicitly **not** covered here. Picking those up is next session's work, not because they're unimportant, but because they weren't part of this pass.

## 2. Guiding principles

These aren't independent — they're the same handful of judgment calls applied consistently across all four subsystems, and they're why the target design below looks the way it does.

- **One runtime, several doors.** GUI, CLI, API, MCP, and A2A are all consumers of the same underlying execution — not separate execution paths with their own rules. This was already mostly true structurally (everything converges on `agent.Boot` or `provider.StreamChat`); the work here is making the remaining seams (routing, provider selection) explicit instead of implicit.
- **The database is the source of truth.** Files exist only where they earn their keep: builtin/seed content (compiled into the binary, used once to seed) and nothing else. No system re-ingests from disk on every boot. Every "we hand-edited a file and it silently didn't take effect, or got silently reverted" failure mode traced back to this pattern being inverted.
- **Composition over flat definition.** An agent is an assembly of independently-defined facets (role, scope, tools, skills, permissions), not a single monolithic record. This is the direct fix for the `.nanite/agents/*.md` sprawl — most of that sprawl was the same role, redefined per project, because there was no cheaper way to express "same persona, different context."
- **Real relational references, not free-text strings.** Tool names, skill names, and model IDs become foreign keys against live catalogs instead of pattern-matched strings in JSON blobs. Every recurring bug this review dug into (tool-name typos, stale model pins, `roleTools`/`tools` drift) has this exact shape, and a relational reference makes the whole bug class structurally impossible instead of something to catch after the fact.
- **Hints, not control.** Steering nudges the agent; it doesn't gate what the agent can do with a deterministic pre-decision layer. This isn't just a style preference — hard-gating experiments in this codebase produced dead-end conversations where nothing moved forward, which lines up with Anthropic's own published guidance on agent design.
- **Kill dead code aggressively.** Standing policy, captured to durable memory (`user/chrispian/memory/decisions/aggressive_dead_code_removal_policy`): when something has zero callers or zero rows, the default action is removal, not "flag and revisit." Nanite has exactly one operator today; loud breakage from a cut is easy to diagnose, silent zombie code is not.
- **MCP is the one external door; A2A is a separate door for a different job; GUI/CLI stay first-party.** GUI and CLI-based chat are first-party clients of the existing REST+SSE API — not moving to MCP, because that API already does the job well and MCP's discrete-tool-call shape is a poor fit for a live turn-by-turn conversation. MCP becomes the single external door for Nanite-aware consumers (Loom today), chosen for standardization (one auth model, one discovery contract, riding the protocol the industry converged on) rather than to avoid engineering cost — that cost was already low. A2A stays distinct, for genuinely external callers with zero Nanite-specific knowledge (real cross-framework/cross-vendor interop) — a different problem than MCP solves, not a duplicate of it.

## 3. Agent Construction

### 3.1 The composition model

An agent is built from three layers, resolved with closest-wins override — the same shape as RBAC/IAM's Role+Binding+cascading-scope pattern (AWS IAM, Kubernetes RBAC/RoleBinding, GCP IAM), deliberately chosen over the flatter "fully-specified Agent object" most agent frameworks use, because the flat pattern doesn't solve reuse-across-context cleanly.

```
roles                        -- reusable persona/behavior template
  id, name, system_prompt, default tool/skill/permission hints

agents                       -- the composition/assembly record (NOT renamed —
                                 keeps every existing agent_id FK's current meaning)
  id, slug, role_id (FK -> roles), consumer_id (FK -> consumers, nullable),
  model_id (FK -> models), instance_mode, class, enabled

<scope references>           -- NOT a new facet catalog like role — references to
                                 entities that already exist for other reasons
  agent_projects (agent_id, project_id)        -- already real, gets its FK fixed
  <future scope dims follow the same pattern>  -- data sources, datasets, etc.
```

Cascading override order, closest wins (same resolution order as `.htaccess`/Kubernetes RBAC): **role → agent/composition → task/invocation.** A role supplies the broadest defaults; the agent composition can override them per scope; a specific task/dispatch can override again at runtime. This isn't special-cased per field — `class` (advisor/process/template/harness), tool/skill/permission grants, and model selection all follow the identical three-tier cascade.

**Scope is not a facet catalog.** Unlike role, there's no new "scope" table to define — scope is a reference to something that already exists independently (a project, eventually a data source or dataset). A label like "SME" is identity, not structure — it belongs in a role's `name`/`system_prompt`, not in a scope mechanism. Concretely: "SME" isn't a role type, it's a generic advisory role scoped to a specific project or dataset.

### 3.2 Relational references replace free-text strings

```
known_tools                  -- NEW: doesn't exist today. Live-synced catalog.
  id, name, server, status (available|unavailable)   -- unavailable, not deleted,
                                                          when a server disconnects

agent_tools                  -- join, replaces tools:/toolPermissions:/roleTools: entirely
  agent_id FK -> agents, tool_id FK -> known_tools

agent_dispatch_allowlist     -- NEW, separate concept from agent_tools: which tools
                                 this agent may authorize a subagent it dispatches to use
                                 (today: agent_profiles.parent_dispatch_allowlist, a bare
                                 JSON array with no FK)
  agent_id FK -> agents, tool_id FK -> known_tools

agent_skills                 -- same FK pattern as agent_tools, against the real skills
                                 catalog (skills table already exists and is real)
  agent_id FK -> agents, skill_id FK -> skills

models                       -- already exists with a real schema (provider_id FK,
                                 context_window, pricing, is_enabled) — needs its
                                 models.dev refresh target fixed (currently feeds an
                                 in-memory overlay, not this table) before agents.model_id
                                 can FK against it meaningfully
```

A default-tools baseline (so a newly-created agent can't accidentally ship without the tool-discovery escape hatch — `request_tools`/`tool_list`/`tool_describe`) is a flag on `known_tools` marking certain tools as always-included regardless of an agent's own `agent_tools` rows, not a per-creation-flow default that can be silently dropped.

### 3.3 Ownership and instancing

```
consumers                    -- NEW, minimal. First row: 'loom'.
  id, name
```

`agents.consumer_id`, nullable = operator-owned. Reuses the "consumer" concept from the platform/MCP decision (§2) rather than introducing a second tenancy concept.

`agents.instance_mode` (e.g. singleton / fresh-per-wake / concurrent) makes explicit what's implicit and hardcoded today (`wakeSkipReason`'s per-`lifecycle_class` branching) — whether an agent composition can run as more than one live instance at a time.

### 3.4 Reflexes at construction time

`internal/agent/reflexes`'s `agent_reflexes` table already supports what's needed structurally: `agent_id IS NULL` = class-bound/global reflex, a specific `agent_id` = per-agent reflex. The one gap: no field distinguishing "required, cannot opt out" from "default-on, an agent may opt out" — add it.

### 3.5 What's cut

- **External-format agent import** (the adapter-discovery tier reading `.claude/agents/`, codex/gemini/opencode conventions) — no replacement. Nanite agents are defined in Nanite's own schema; importing another tool's native config format never worked anyway.
- **Files as agent storage**, except builtin/seed content. No file import, no export/round-trip. Builtin/seed profiles become one-time seed data — inserted once, never re-overwritten from the compiled file on every boot. This closes a real, currently-live bug: a GUI customization to a builtin agent is silently reverted on the next restart today.
- **`agent_known_skills`/`roleSkills:`-as-currently-used** — a dead end structurally identical to `roleTools:`, seeds a display-only table nothing reads at runtime. Superseded by the FK-based `agent_skills` join.

Plugin-provided agents stay as a source, but must conform to this schema once it lands — no legacy grandfathering.

## 4. Agent Launching

### 4.1 CLI-based subprocess launching

Nanite has never run agents through a real pseudo-terminal — that design was abandoned for the current `StreamingStdio` approach (a long-lived subprocess exchanging NDJSON over plain stdin/stdout pipes for Claude; fresh-subprocess-per-turn for Codex/OpenCode). "PTY" is a naming fossil surviving in provider-name strings and a few function names; it does not describe a live mechanism anywhere in the current runtime.

**The boot-directory strategy is correct and stays exactly as-is.** Every CLI launch spawns with `cwd` set to a Nanite-owned boot directory, not the project directory — project access happens separately via `--add-dir`. This is deliberate, hard-won infrastructure: only the `CLAUDE.md`/`AGENTS.md` file loaded from the process's own boot-dir `cwd` survives Claude Code's context compaction. Planting into Nanite's own directory (rather than the project's) is *why* Nanite's own system/workflow instructions reliably survive compaction. This is already shared infrastructure across every CLI launch, not tied to the boot-profile catalog being retired below — nothing here changes.

**Post-compaction re-read of the project's real `CLAUDE.md`/`AGENTS.md` is mandatory-by-default, code-driven** — not a per-agent opt-in flag. (A flag can be added later if a real need for one shows up — YAGNI until then.)

**The boot-profile catalog (`boot_profile_catalog_path`, the `bootprofile.Profile`/`Launch` YAML system shared cross-app with Tether's `bootgen` format) is retired as a standalone system.** It never competed with agent construction — it only ever overrode prompt *content* for CLI launches, with a real `agents` row still resolved underneath regardless. Its original purpose (per-project generated agent files) is now solved more cleanly by the composition model in §3. Three pieces carry forward:

- The `cmd`/`http` dynamic-resolver capability (fetch live data — memory/handoff recall, etc. — at launch time and fold it into assembled context) becomes a first-class, DB-configurable part of launching-time context assembly, available to every agent — not YAML-file-based, not gated behind a separate catalog system.
- The mandatory compaction re-read (above).
- Nothing else. "Lineage" (built for Tesseract's version-diffing use case, delivered no real value) is dropped entirely. Cross-app portability (Tether/Torque interop via a shared boot-profile file format) is deliberately opt-in, not a structural default — cross-app agent access goes through MCP; if bootgen-format interop is ever wanted again, it's an explicit, on-demand export/import bridge, not Nanite's live source of truth.

### 4.2 API-based launching

**Provider registration (code) and provider/model metadata (DB) are two distinct concerns — keep them named and documented as such.** Registering a new HTTP provider (an SDK wrapper implementing `llmcontracts.Provider`) is inherently a code change. The `providers`/`models` DB tables are catalog/display metadata layered on top of already-registered providers; they can never make a new provider exist. The `models` table's job is narrower than it might look: it informs *which model string* an agent composition references among already-registered providers.

**Ollama gets fixed for real, not cleaned up as dead code.** It's a real, currently-run local provider — `chat.InferProvider` already special-cases Ollama-shaped model names but routes to a provider that was never (re-)registered. Needs a proper `internal/llm/ollama` adapter and registration.

**Provider reliability parity is accepted as best-effort, not a gap to close.** Rate-limiting, circuit-breaking, and prompt caching are Anthropic-specific today because that's what Anthropic's SDK exposes — OpenAI doesn't have equivalents to wire up. Nanite supports what each provider actually offers, on every provider it uses. (Two more providers — OpenRouter, OpenCode Zen — are likely additions later; no action needed now, noted so this stays extensible.)

**`resolveProvider`'s bespoke fallback chain collapses into the construction-model cascade.** Once an `agents` row has a real, cascade-resolved `model_id` (§3.1), provider/model resolution becomes "read the already-resolved value" — not a second, independent walk (session → agent-profile default → user-settings default → fallback chain → model-inferred) reimplementing the same kind of decision the composition model already makes.

### 4.3 CLI-vs-API routing becomes an explicit typed field

Replace the fragile four-site string-prefix convention (`chat.IsCLIProvider`/`NormalizeCLIProvider`, `shouldUsePTY`, `agent_deps.go`'s `stripRegistryPrefix`, plus the boot-profile-catalog's own layered `pty-` prefix convention) with `agents.runtime_kind` (cli | api) — a real, typed field checked once, not a string pattern matched in four places that have to stay in sync by convention. This is the exact mechanism class behind the historical CLI/API misrouting bugs found in this review (the stale `["pty"]` fallback chain, the inverse "c195" bug). Part of this work: fully scrub the remaining "PTY" naming (`shouldUsePTY`, `IsPTYProvider`, the `pty-*` provider-string convention) — there is no PTY anymore, only a CLI-based subprocess, and the naming has already caused real confusion, including during this review.

## 5. Steering

### 5.1 Reflexes become the single steering primitive

`internal/agent/reflexes` — the DB-backed predicate/event/interval engine — absorbs the real, valuable jobs of three systems being retired below:

- **The agent broker's real intent** ("know which agent to use based on context"; nudge on mail/detected intent) — folds in as a new reflex action kind (e.g. `dispatch_to_agent`) alongside the existing five (`inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule`).
- **`promptrouter`'s phrase-matching** — the reflex predicate engine already supports regex-match triggers over message text, a superset of promptrouter's flat phrase-list matching. Promptrouter's remaining job becomes a reflex trigger shape, not a separate system.
- **Skill-broker-style relevance surfacing** — reflexes nudge an agent toward using an assigned skill on a specific, testable condition (the same pattern already used to point agents at named procedures today), rather than a generic per-turn relevance ranking. Explicitly analogous to Claude CLI's hooks.

The concrete design (exact new action-kind shape, how promptrouter's existing catalog entries migrate) is not yet finalized — this is a direction, not a spec.

### 5.2 What's cut

- **Modes, in full**: Session Mode (+ `modes` table + `/mode`/`/chat`/`/plan`/`/work` slash commands), Legacy Agent Mode, the mode↔agent junction table, and the per-turn `classify.ClassifyMode` signal. Original intent (mirror Anthropic-style modes, detect intent to route to the right agent) is superseded — real usage was near-zero, and any permission/procedure effects modes provided are better done via reflexes. **Side effect to handle deliberately**: the context/slot system's `INV4` ("mode-aware content swap") invariant becomes vacuous once Session Mode is gone — needs an explicit small update, not silent rot in a subsystem otherwise left alone.
- **The strategy planner, in full.** Both of its outputs are gone: `Approach` was already decorative (logged, never gated behavior), and its `MaxTurns` output is superseded by harness-level termination bounds (§6.1). Original intent was "hint/suggest to save context," not hard control; hard-gating experiments here produced dead-end conversations, consistent with published guidance on agent design. If something like this is needed later, real-time wake/message delivery is available as a general steering mechanism to rebuild narrower against.
- **The skill broker and tool broker, as formal abstractions.** Both broker patterns served a real purpose early, before the current construction model and reflex engine existed — neither is earning its complexity now. What's cut: the rule-matching wrapper (`go-toolbroker`/`NaniteDefaultRules`, `internal/skillbroker`). What stays: the underlying catalogs and the selection/filter logic that does real work — permissions, allowlist, chat-surface exclusion, progressive discovery.
- **`agent_known_tools.BumpActivation`'s usage-based "learning."** Meant to infer per-agent tool/skill preference from live usage; results were unreliable and it broke prompt-cache stability (a real, documented incident), which is why it's already a no-op. Not being revived in this form — preference, if wanted, comes from explicit assignment through the construction model, not implicit usage inference.

### 5.3 What's kept, actively being evaluated (not yet a final call)

- **Grounding** (`internal/grounding` — memory-recall-informed strategy signal) — fully built, real, substantial; disabled by default, zero rows ever. Worth testing in real chat sessions as a possible complement to reflexes before deciding to integrate, re-architect, or cut.
- **`pending_reflexes`** (agent proposes its own reflex, operator approves) — complete review/approve backend, missing only the self-tool that would let an agent call it. Build the missing piece and actually test the flow before deciding.

### 5.4 Two correctness gaps carried into implementation

Not architecture questions, but real and explicitly not forgotten:

- **Tool concurrency-safety classification** is currently pure name-heuristic (suffix/substring matching), not derived from any declared tool metadata — a misleadingly-named destructive tool could be misclassified as safe to parallelize.
- **Truncation, broadly** (not just the tool-catalog's 80-character description truncation) has a real, documented history of causing bugs here — a truncated error message once caused an agent to hallucinate a tool didn't exist. Whatever touches truncation next needs deliberate care, not a repeat of "add a limit and move on."

## 6. Harness (the shared turn loop)

### 6.1 Termination bounds, simplified

Two redundant "soft" turn-budget mechanisms are cut: the strategy planner's `MaxTurns` (§5.2) and, separately, `AgentConstraints.MaxTurns` (default 75) — both purely telemetry, neither one actually stops anything. The real, hard stoppers stay exactly as they are: `RunawayFailCap` (10, consecutive tool failures), `HardCeiling` (200, absolute iteration backstop — already the generous, non-strategic ceiling a redesign would otherwise need to invent), `IdleTimeoutSeconds`, and natural `end_turn`.

**Idle-timeout and reaper aggressiveness is a real, still-open reliability question, not resolved by the recent activity-reset fix.** The operator's own experience: more false reaps than genuine idle/runaway catches, by a wide margin. The shipped reaper fix (activity-reset idle timer + non-resetting hard ceiling) should help but needs verification against real behavior — treat as "landed, not yet proven," the same posture as the subagent-reply-delivery fix from earlier in this review.

### 6.2 Run-another-agent surfaces, unified

REST delegation, LLM-triggered subagent dispatch, and durable-agent wake all converge on the same `generateResponse` execution today, but each has its own request/result type, its own draining logic, its own completion-signaling mechanism. Collapse to one shared shape.

### 6.3 Tool lazy-loading

`NANITE_TOOLS_LAZY_LOAD` (essential/lazy tool partitioning, cuts context by loading full tool schemas on demand) is fully built and tested but off by default. Turn it on; tune based on real behavior.

## 7. Reliability floor (carried from the start of this review, still real)

- **Model pinning**: 6 of 8 `.nanite/durable-agents/*.yaml` still pin a retired model ID. Trivial, independent fix.
- **Durable-agent wake mistagging**: a wake's delivery path stamps `CallerChat` instead of `CallerBackground` — a durable-agent wake is currently indistinguishable from a human GUI message at the exact layer any caller-aware steering narrowing would need to branch on. Real prerequisite bug; fix before relying on caller-type-based behavior anywhere.
- **HTTP-provider crash recovery**: the immediate noise (misleading permanent-failure breadcrumbs) is already fixed by gating the Recovery Broker off for HTTP-provider sessions. `DispatchRetry` itself is still CLI-only by construction. Open decision: is falling back to Orphan Sweep/cold-boot sufficient for HTTP-provider sessions, or is a real HTTP-provider retry path wanted?
- **`RequestStart`**: fix to call straight through to the real `Start()`, matching `RequestResume`'s behavior — no known reason for the current two-step dead end.
- **`IsFirstPartyBuiltinServerName`**: hardcoded four-name switch, same bug shape as the incident it was built to fix. Needs structural hardening (self-registering protection) before "platform" means more MCP servers connecting over time.
- **A2A method-name conformance**: close the gap between Nanite's invented JSON-RPC method names (`a2a.task.submit`) and the real spec's (`SendMessage`/`GetTask`/`CancelTask`) — already self-flagged in the code as a known gap.

## 8. Genuinely unresolved — do not treat as decided

- **Whether durable agents should eventually run CLI-wrapped instead of API-based.** The two-substrate split (CLI-primary for interactive, API-harness narrowed to durable agents) is the working default, but this was never actually tested — no real Curator wake has been routed through a CLI-wrapped path to see what's gained or lost. The `runtime_kind` typed field (§4.3) is designed to make this experiment cheap to run later, not to pre-decide the answer.
- Exact design of the reflex `dispatch_to_agent` action kind and how promptrouter's existing catalog migrates (§5.1) — direction is set, specifics aren't.
- GUI's four start-surface types (Chat/Harness/Durable/Recipe) against the new construction model — not yet traced against §3; "Recipe" likely collapses into "create with prefilled fields" but this hasn't been designed.

## 9. Explicitly out of scope for this document

Deferred to a follow-up session, not forgotten: session-lifecycle-recovery's remaining territory (compaction event wiring, the other three of four independent crash-recovery mechanisms), the envelope system, the plugin system, the storage layer beyond what's touched above, inter-agent messaging beyond the subagent-reply-delivery fix already shipped, and the frontend's own architecture (never audited in the first place, flagged as a gap worth a dedicated pass).
