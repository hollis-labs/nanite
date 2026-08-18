# Nanite Architecture Alignment — Decision Log

**Date:** 2026-08-17
**Status:** Living log — decisions below were made directly in conversation with the operator, not inferred or pre-decided by any review pass or subagent. Updated incrementally as topics get resolved; the full architecture doc + task breakdown gets written once enough of this is settled.
**Inputs:** `docs/release-vision.md`, the three alignment-review passes, `docs/system-audit/2026-08-17/`, plus direct code verification for every claim a decision below depends on. See `docs/alignment-review-synthesis.md`'s "Code-verification addendum" for the underlying verification detail — this file only records what was actually decided.

---

## Decided

### 1. Transports — keep all three, distinct roles, no consolidation

- **GUI + CLI (`nanite chat`) are first-party clients.** Stay on the existing REST+SSE harness-v1 API. Not moving to MCP — the frontend already has a mature, working streaming/envelope architecture (`useChat.ts`, structured cards, reconnect/session-takeover semantics) built for a live turn-by-turn conversation, which is a different interaction shape than MCP's discrete-tool-call model. Forcing it onto MCP would mean rebuilding working machinery for no functional gain.
- **MCP is the single external door for Nanite-aware external consumers** (Loom today, future portfolio apps). The decision to standardize on MCP was never about avoiding per-consumer engineering cost — that cost is already low in this codebase (confirmed: `internal/api/loom_curator_wake.go` is a ~30-line payload adapter in front of the same service call the generic endpoint uses, not duplicated logic). The real reason: riding the protocol the agent industry has converged on, one auth model instead of N ad hoc ones, one discovery contract (`tools/list`) instead of tribal-knowledge URLs and payload shapes, one thing to version. Bespoke REST webhooks (`loom_curator_wake.go`-style) migrate to MCP tool calls once the control-plane surface exists — they don't stay as a second parallel external door.
- **A2A is distinct and stays** — not retired, not merged into MCP. Reason, from the operator directly: MCP requires the caller to already know Nanite-specific tool names/schemas; A2A (the real, spec-conformant protocol) is for callers with zero Nanite-specific knowledge — genuine cross-framework/cross-vendor agent interop. That's a real, separate goal, not a duplicate of what MCP covers. Today's only real user is the operator, but the stated long-term goal is an app other people can use, and cross-standard interop is explicitly wanted for that, not just tolerated.
- **MCP's request/response shape is sufficient for wake-and-track** — settled, not an open question. `wake_agent` acks and returns (same shape as Nanite's existing HTTP API: 202 + session ID), `get_agent_status` polls separately. No subscription/resource mechanism needed.
- **Auth**: not yet built (today's Loom webhook has none — acceptable for "works for just me," not acceptable long-term). When the MCP control-plane surface gets built, adopt MCP's standard OAuth-based authorization framework rather than inventing something bespoke. This is a real task to include when that work happens, not something to defer indefinitely.
- **Working principle going forward**: if a problem surfaces later in this transport model, treat it as a mechanical/implementation bug to diagnose and fix — not as evidence the boundary architecture itself (first-party API / external MCP / cross-framework A2A) was the wrong call.

### 2. A2A — locked, with a concrete follow-up task

A2A stays, distinct from MCP, and the goal is genuine spec conformance (not just internal reuse of its routing logic). Concrete task, not yet started: close the method-name conformance gap already self-flagged in the code (`internal/api/a2a_jsonrpc.go` uses Nanite-invented method names like `a2a.task.submit` instead of the real spec's `SendMessage`/`GetTask`/`CancelTask`), and clean up any remaining naming ambiguity against the old, already-renamed-away internal messaging system that used to share the "a2a" name (now `internal/messaging`/`agent_messages`). `TaskManager`'s routing logic (classify-target → route to `WorkflowLauncher.Launch` or `DurableWake.Wake`) stays shared infrastructure behind both A2A and the future MCP control-plane tools — that reuse was correct in all three review passes, independent of the A2A-vs-MCP question.

Separately noted, not in scope for Nanite right now: Tether has its own earlier, pre-standardization A2A-host experiment in its messaging system. Possible future direction (adopt the pattern more broadly across the portfolio), explicitly parked — this pass stays Nanite-scoped.

### 3. Agent-profile housekeeping (captured, not urgent)

- `.nanite/agents/agridd-project-manager.md` and `proxima.md` — flagged for review then deletion. May be recreated under new names, or dropped entirely, once the new agent spec exists. No action needed now.
- `.nanite/agents/torque-task-writer.md` — confirmed keep, solid as-is.
- GUI session visibility: durable/system-agent sessions currently show alongside interactive sessions in the GUI session list. Useful during active build-out, but the target state is to gate background/system-agent session visibility behind the `developer_mode` flag (same flag already gating `dev_bash` and the deferred interactive-terminal UI). Not urgent, captured for the eventual agent-spec/GUI work.

### 4. Agent-definition spec — early requirements (superseded by §6's resolved schema)

**Note added end-of-day: everything below was locked by §6's role/scope/agent composition model.** This section is kept as the requirements list that fed that resolution, not as a separate open item — read §6 for the actual, current schema.

- External-format agent import (adapter-discovery tier: `.claude/agents/`, codex/gemini/opencode) — cut, no replacement needed.
- Plugin-provided agents — stay, must conform to the new schema once it lands, no legacy grandfathering.
- Files as agent storage — dropped except for builtin/seed profiles (compiled-in, used once to seed the DB). DB is canonical for everything else; no file import, no export/round-trip planned (no current use case for one).
- Builtin/seed profiles become one-time seed data, not re-overwritten from the compiled file on every boot — closes a real live bug where a GUI customization to a builtin agent is silently reverted on next restart.
- Durable-agent config becomes an optional, class-conditional extension on one core `agents` schema (not a second parallel definition) — and this pattern should be built general enough to support future agent-type variations beyond durable, not hardcoded to just that one case.
- Tool/skill/model references become real foreign keys against live catalogs instead of free-text strings — eliminates the tool-name-typo and stale-model-pin bug classes structurally. `known_tools` catalog table doesn't exist yet (today's `agent_known_tools.tool_name` is a bare string with no real catalog to reference) — needs to be built. `models` table already exists with a real schema and a live `models.dev` refresher, but the refresher currently feeds an in-memory overlay (`pkg/models`), not the DB table — needs reconciling before `agents.model_id` can FK against it meaningfully.
- Ownership/tenancy tagging: `agents.consumer_id` (FK to a new minimal `consumers` table, nullable = internal/operator-owned) — reuses the "consumer" concept from the earlier platform-scoping decision rather than introducing a separate one. Loom is the first real row.
- Instance concurrency: no explicit field exists today (current behavior is implicit, hardcoded per `lifecycle_class` in `wakeSkipReason`) — new schema needs an explicit `instance_mode` (e.g. singleton / fresh-per-wake / concurrent) property.
- Per-agent reflexes (`internal/agent/reflexes`, NOT `internal/promptrouter` — see below): already substantially supported — `agent_reflexes` table already has `agent_id IS NULL` (class-bound/global) vs. a specific `agent_id` (per-agent). Gap: no explicit "required, cannot opt out" vs. "default-on, agent may opt out" distinction yet — needs a field added.
- GUI's 4 start-surface types (Chat/Harness/Durable/Recipe, `StartSurfaceDialog.tsx`) — not yet evaluated against the new schema; likely "Recipe" collapses into "create with prefilled fields" once one schema exists, but not traced in detail yet.

### 5. Naming/terminology cleanup — captured, explicitly not urgent

- `internal/promptrouter`'s `BuiltinReflexes()`/`Reflex` type still carry "reflex" naming even though the package itself was already renamed away from `internal/reflex` this past weekend specifically to stop that conflation. Needs a full scrub of "reflex" terminology from that system — "reflexes" is now a reserved term specifically for `internal/agent/reflexes` (agent-steering). Not the same system, was never related to the per-agent-reflex requirement above.
- `internal/promptrouter`'s catalog (`BuiltinReflexes()`) should move from a hardcoded Go-literal list to a real, config-based source with proper duplicate-detection and validation — same root problem (hardcoded/unvalidated) as the agent-definition issues, applied to the routing/steering layer. Belongs to the "steering" pass, not the agent-construction pass — revisit when we get there.
- Conceptual framing for future reference: `promptrouter` is a general match/route-on-signals substrate (could eventually run more than phrase-matching — sentiment, keyword detection, etc.), not inherently reflex-shaped. `agent/reflexes` is specifically reserved for agent-steering behavior. Operator's namespace instinct (`agent/*` for multiple agent-related subsystems, `prompt/*` for multiple prompt-related subsystems) is idiomatic Go and worth following organically as new subsystems get added — no rename forced now.

### 6. Agent composition model — role/scope/agent split (supersedes the flat `agents` schema sketched earlier in §4)

The construction discussion converged on a composition model rather than a flat definition table — closer to RBAC/IAM's Role+Binding+cascading-scope pattern (AWS IAM, Kubernetes RBAC/RoleBinding, GCP IAM) than the flat, fully-specified "Agent object" most agent frameworks (LangChain, CrewAI, Google ADK, AutoGen, OpenAI Assistants) use. Chosen deliberately: the flatter pattern doesn't solve "same persona, reused across different contexts" cleanly, which is the direct cause of today's `.nanite/agents/*.md` sprawl (multiple near-duplicate profiles that are really the same role at a different scope).

- **`agents` is not renamed.** It stays the composition/assembly record — the persisted form of `new Agent(role, scope, skills, permissions, context, settings)`. Every existing `agent_id` FK across the schema (`agent_tools`, `agent_skills`, `agent_projects`, `agent_reflexes`, `durable_agent_instances.profile_id`) keeps its current meaning — no rename ripple.
- **`roles`** — new table, the one genuinely new facet. Holds persona/identity (name, system_prompt) and optionally sensible defaults for tools/skills/permissions that a new composition can start from. Reusable across many `agents` compositions bound at different scopes.
- **Scope is not a new facet catalog** the way role is. It's a reference from the composition record to entities that already exist independently for other reasons — a project, a data source, a specific dataset. `agent_projects` (already real, already FK'd once fixed) is the existing precedent; other scope dimensions follow the same association pattern as they come up. No abstract "domain of expertise" facet — a label like "SME" belongs in the role's `name`/`system_prompt` for identity purposes, not in structure.
- **Tools/skills/permissions bind at the composition (`agents`) level**, not fixed by role. Role may carry defaults a new composition starts from; the actual grant is adjustable per composition/scope. Chosen explicitly for flexibility without added complexity.
- **Cascading override resolution, closest wins, confirmed direction**: Role (broadest defaults) → Agent/composition (scope-specific settings) → Task/invocation (narrowest, most specific — overrides everything above it at runtime). Same resolution order as `.htaccess`/Kubernetes RBAC.
- **`class` (advisor/process/template/harness) follows the same 3-tier cascade as everything else** — a default at `roles`, override-able at the `agents` composition level, and override-able again at runtime/task level. Not a special case: the same role can legitimately run different lifecycle shapes depending on scope or even a specific dispatch.

**Construction is settled.** External-format import cut, plugins stay (must conform), files dropped except builtin/seed, DB-authoritative with no re-ingest-on-boot, role/scope/agents composition model with cascading override (role → agent → task, closest wins), FK-based tool/skill/model references replacing free-text strings, `consumer_id` ownership tagging, explicit `instance_mode`, per-agent reflexes (already real, opt-out field still needed), naming resolved with no rename ripple. Next: agent launching.

### 7. Boot-profile catalog — retire as standalone, port three specific pieces

The catalog (`boot_profile_catalog_path`, `bootprofile.Profile`/`Launch` YAML, shared cross-app with Tether's `bootgen` format, also used by Torque) never actually competed with agent construction — it only ever overrides prompt *content* for CLI-shaped launches; a real `agent_profiles` row is still resolved underneath regardless. Its original purpose (per-project generated agent files, before the role/scope/agent composition model existed — e.g. `nanite-engineer-nanite`) is now solved more cleanly by that model. Verified two of its mechanics against the shared boot code before deciding what to keep:

- **The actual hard-won discovery: only the CLAUDE.md/AGENTS.md loaded from the agent's own boot-dir cwd survives Claude Code's compaction — not any other CLAUDE.md the agent might also have access to.** This is a structural fact about the CLI tool's behavior, and it's *why* planting in Nanite's own boot dir (not the project dir) matters — it's what makes Nanite's own system/workflow instructions durable across compaction. Confirmed this planting strategy is already shared `agent.Boot`/`Layout` infrastructure, not catalog-specific — already safe today, nothing to port, just something to not accidentally break when launching gets redesigned.
- **Separately, and NOT the hard part**: whether an agent is instructed to re-read the project's own real CLAUDE.md/AGENTS.md after compaction is an optional, cheap design choice layered on top (a deliberate choice, not a technical necessity) — can be made mandatory/code-driven now if wanted, low stakes either way.
- **`cmd`/`http` dynamic-resolver slots** (fetch live data — memory/handoff recall, etc. — at launch time and fold it into assembled context) — genuinely valuable, real runtime flexibility ("the callback idea... meant it was flexible at runtime too"). Decision: carry this forward as a first-class, DB-configurable part of launching-time context assembly, available to every agent — not YAML-file-based, not gated behind opting into a separate catalog system.
- **"Lineage" (`LineageAlias`/`LineageID`) — drop entirely, no value delivered.** Built specifically for Tesseract to observe how an agent definition changed across versions over time — not a continuity/hand-off mechanism as originally guessed. Confirmed: "we got no value out of it." Nothing to port, nothing to reconcile against the new `agents.id` model.
- **Post-compaction re-read of the project's real CLAUDE.md/AGENTS.md is mandatory-by-default**, code-driven, not a per-agent opt-in flag. YAGNI on a flag until a real need for one shows up.
- **Cross-app portability (Tether/Torque interop) — deliberately opt-in, not a structural default.** Cross-app agent access goes through MCP (already locked), not shared launch-file portability — consistent with "Curator belongs to Loom" and the general platform/consumer model. If bootgen-format interop is ever wanted again, build it as an explicit, on-demand export/import bridge (DB → bootgen YAML and back), not Nanite's live internal source of truth.

`agent_boot_plans` (previously flagged as dead/unwired code, a judgment call) is now understood in this light too — it looks like an earlier, abandoned attempt at the same "plant items into a boot dir" problem this catalog already solves differently. Reinforces treating it as safe to cut rather than revive.

### 8. Standing policy: aggressive dead-code removal (captured to Vanta — `user/chrispian/memory/decisions/aggressive_dead_code_removal_policy`)

Not scoped to this session — a general operating preference going forward. When dead/unused code is found (zero callers, zero rows, unwired feature), default to recommending outright removal, not "flag as low-risk, revisit later." Single-operator system, no external dependents; loud breakage from removal is easy to diagnose, silent zombie code is not. This **retroactively strengthens** every "cut opportunistically" item already in this log (§4's `gomsg`/`agent_cycles`/`tool_enrichments`/unscheduled reaper list, §7's dead `internal/config.Config` fields) — read those as "cut," not "cut when convenient."

### 9a. API-based launch (`provider.StreamChat` path) — locked understandings

- **Provider registration (code) vs. provider/model metadata (DB) are two distinct concerns, not two overlapping ones — keep them clearly separated going forward, in naming and in docs.** Registering a new provider (an SDK wrapper implementing `llmcontracts.Provider`, registered in `initProviders`) is inherently a code change. The `providers`/`models` DB tables are metadata/catalog only (display names, pricing, context windows, which models are enabled) layered on top of already-code-registered providers — they can never make a new provider exist. Our `models` table (and its sync-target gap, already flagged) inform *which model string* an agent composition references among registered providers; that's the correct, narrower scope for it.
- **`providers.base_url`/`api_key` DB columns are dead (full CRUD surface, zero runtime effect on request construction)** — real candidate for the dead-code policy; either wire them up for real or cut them.
- **Ollama is a real, wanted local-model provider that's currently broken, not dead code to remove.** It's actually run locally today. `chat.InferProvider` special-cases Ollama-shaped model names but routes to a provider that was never (re-)registered as a real `llmcontracts.Provider` — needs a proper `internal/llm/ollama` adapter + registration, not a cleanup of the stale routing.
- **Provider reliability parity (rate-limiting/circuit-breaking/caching) across providers is accepted as best-effort, not a gap to close.** Nothing to fix — some providers (OpenAI today) don't support what Anthropic's SDK exposes; Nanite supports what each provider actually offers. Two more API providers likely coming later (OpenRouter, OpenCode Zen) — no action now, noted so future provider-abstraction decisions stay extensible to more than two.
- **`resolveProvider`'s bespoke fallback-chain (session → agent-profile default → user_settings.default_provider → fallback chain → model-inferred) should collapse into the role→agent→task cascade already locked for construction** — once an `agents` composition row has a real, cascade-resolved `model_id`, this becomes "read the already-resolved value," not a separate independent resolution mechanism.

### 9. `RequestStart` — fix to match reality, not document the gap

Confirmed no known reason for the two-step (`RequestStart` flips `status` to `start_requested` and stops; nothing transitions it further). Align it with `RequestResume`'s behavior — call straight through to the real `Start()`.

### 10. Steering — agent broker, strategy planner, modes, promptrouter

- **Hardcoded `MaxTurns` budgets (10/20/40) — cut.** Explicitly naive (both the operator's own judgment and Anthropic's published guidance against rigid step/turn caps as a steering mechanism). Replaced by the reaper's already-shipped activity-reset idle timeout + non-resetting hard ceiling (`d92d8cf`, confirmed real and matches the intended shape). Open sub-question: does the tool-use loop still want a generous, non-strategic iteration backstop (same pattern as the reaper's hard ceiling, just at iteration-count instead of wall-clock) — not yet resolved.
- **Modes — cut in full**: Session Mode (+ `modes` table + slash commands), Legacy Agent Mode, the mode↔agent junction table, and the per-turn `classify.ClassifyMode` signal. Original intent (mirror Anthropic-style modes, detect intent to pick the right agent) is superseded — low real usage, and any permission/procedure effects modes provided elsewhere are better done via reflexes. Note: this makes the context/slot system's `INV4` ("mode-aware content swap") invariant vacuous — needs a deliberate small update when this lands, not silent rot in a subsystem otherwise agreed to leave alone.
- **Strategy planner — full cut, not partial.** Both outputs are gone: `Approach` was already decorative (logged, never gated behavior), `MaxTurns` is being replaced per above. Original intent was "hint/suggest to save context," not hard control — hard-gating experiments produced dead-end conversations, consistent with Anthropic's own published guidance (hints over control). Real-time wake/message delivery is available as the general steering mechanism if something like this is needed later; can always rebuild narrower if a real need appears.
- **Agent broker + `promptrouter` — proposed to consolidate into `internal/agent/reflexes` as the single steering primitive, not stay as separate systems.** Agent broker's real intent (know which agent to use based on context; nudge on mail/detected intent) and promptrouter's phrase-matching (already a capability the reflex predicate engine has via regex-match triggers) both look like they collapse into reflexes — likely as a new reflex action kind (e.g. `dispatch_to_agent`) alongside the existing five. Agreed in principle; concrete design still to be worked out.
- **Grounding and `pending_reflexes` — conditionally keep, not yet a final decision.** Both are worth actually trying in real chat sessions (not just static review) before deciding whether to re-architect, integrate as a complementary system to reflexes, or kill. Grounding: the operator wasn't aware this had been built — genuinely fresh evaluation, not a known-and-neglected feature. `pending_reflexes`: needs the missing propose self-tool built before it can be tested at all.

### 11. Steering — skill broker and tool broker retired as abstractions; catalogs stay

- **Skills: clean slate, not a revival.** Never actually exercised in practice — system-stability work took priority over everything else, `agent_skills` has zero rows workspace-wide. Follows the same model as tools: FK-based assignment through the role/scope/agent composition model, no separate ranking abstraction. Reflexes act as the nudge/reminder mechanism toward actually using an assigned skill — explicitly analogous to Claude CLI's hooks (fire on a condition, inject a reminder/behavior).
- **Both the skill broker and the tool broker (the formal "broker" abstraction layers) are retired.** The broker pattern served a real purpose early — before the current agent-construction model and reflex engine existed — but isn't earning its complexity anymore. What stays: the underlying catalogs (skills table, tool catalog) and the actual selection/filter logic that does real work (permissions, allowlist, chat-surface exclusion, progressive discovery). What goes: the formal rule-matching "broker" wrapper around them (`go-toolbroker`/`NaniteDefaultRules`, `internal/skillbroker`).
- **`agent_known_tools.BumpActivation`'s usage-based "learning" approach is not being revived in its original form.** It was meant to let the system infer per-agent tool/skill preference from live usage — results were "chaotic at best" and it broke prompt-cache stability, which is why it was already turned into a no-op. If preference/pinning is wanted going forward, it comes from the new explicit config model (deliberate assignment through role/scope/agent), not implicit usage-count inference.
- **Two correctness gaps deferred to implementation, not forgotten**: the tool concurrency-safety classification (currently pure name-heuristic, no declared metadata — real risk, explicitly flagged as "for sure" needing a fix) and truncation more broadly, not just the 80-character tool-catalog case. Truncation has a real, documented history of causing bugs here (a truncated error message once caused an agent to hallucinate a tool didn't exist; naive reordering broke prompt caching) — whatever touches truncation next needs to be done deliberately, not as a repeat of "add a truncation limit and move on."
- **Net effect on steering**: reflexes (absorbing agent broker, promptrouter, and skill-broker-style nudging) is the one real steering primitive left, plus grounding and `pending_reflexes` under active evaluation, plus the tool/skill catalogs as plain infrastructure reflexes and agents reference directly — no formal broker abstraction wrapping any of it.

### 12. Harness — turn loop, CLI/API routing, tool lazy-load, run-another-agent unification

- **`AgentConstraints.MaxTurns` (default 75, soft/telemetry-only) — cut.** A second, separate "soft budget" from the already-cut strategy-planner one, same anti-pattern (fires a warning, stops nothing). Real stoppers stay as-is: `RunawayFailCap` (10, hard), `HardCeiling` (200, hard — this is already the generous non-strategic iteration backstop asked about earlier, no new mechanism needed), `IdleTimeoutSeconds`, natural `end_turn`.
- **Idle-timeout/reaper aggressiveness is a real, still-open reliability concern, not settled by this weekend's activity-reset fix.** Operator's own words: "we've had more false reaps than idle or runaway agents by far." The `d92d8cf` reaper fix (activity-reset timer + hard ceiling) should help, but needs real verification against actual behavior before being trusted as resolved — same "landed but unproven" treatment as the subagent-reply-delivery fix from earlier today. Needs a review pass to confirm agents flagged idle are actually idle before tuning further.
- **CLI-vs-API routing becomes an explicit, typed field** (`runtime_kind` on the agent composition model, extending what was already planned for the durable extension to every agent) **replacing the fragile four-site string-prefix convention** (`chat.IsCLIProvider`/`NormalizeCLIProvider`, `shouldUsePTY`, `agent_deps.go`'s `stripRegistryPrefix`, plus the boot-profile-catalog's own layered `pty-` prefix convention on top). This is the exact mechanism behind the historical CLI/API misrouting bugs found earlier today. Part of this work: rename the remaining "PTY" naming in this area (`shouldUsePTY`, `IsPTYProvider`, `pty-*` provider-string convention) — there is no PTY anymore, only a CLI-based subprocess, and the naming has already caused real confusion (including in this very conversation).
- **`NANITE_TOOLS_LAZY_LOAD` (essential/lazy tool partitioning) — turn on, tune as needed.** Fully built, tested, was believed already enabled but is off by default. Firmer decision than grounding's "evaluate then decide" — just enable it and tune based on real behavior.
- **Unify the three "run another agent" surfaces** (REST delegation, LLM-triggered subagent, durable-agent wake) — currently converge on the same `generateResponse` execution but each has its own request/result type, draining logic, and completion-signaling mechanism. Collapse to one shared shape.
- **Docs entropy (stale internal chat-system docs, and today's decisions only widening the gap) — acknowledged, not a separate action item.** Handled by the eventual final architecture doc this whole review is building toward, not interim doc patches.

---

## Not yet decided / still open

**Note added end-of-day: this section was written early (after §1-2) and is stale — most of what it originally listed (pre-loop steering narrowing, dead-code cuts) got resolved later in this same log (§10-12) and in `docs/architecture-agents-2026-08-18.md`/`docs/architecture-agents-tasks-2026-08-18.md`. Genuinely still open as of end-of-day, per the architecture doc's §8:**

- Whether durable agents should eventually run CLI-wrapped instead of API-based — the two-substrate split is the working default, never actually tested. Resolving this is Phase 5 of the task breakdown.
- The exact design of the reflex `dispatch_to_agent` action kind and how promptrouter's catalog migrates into it (§10) — direction is set, specifics aren't.
- GUI's four start-surface types (Chat/Harness/Durable/Recipe) against the new construction model — not yet traced.
- HTTP-provider recovery scope (§7 of the task breakdown, Phase 0 item 4) — Orphan-Sweep-only vs. a real retry path.

Anything not listed under "Decided" above or in the two `architecture-agents-*` documents should still be treated as unresolved.

---

## 2026-08-18 — Storage & Migrations

### 13. Migration mechanism — adopt goose, don't build custom

No `schema_migrations` ledger exists today — all 93 migration files re-run in full on every boot, idempotency achieved by swallowing specific SQL errors. This already caused one production crash-loop (`e2273f8`) and the fix (`migrate:skip-if-column-exists`) was applied only to the three migrations involved in that incident — the identical structural pattern in `043`/`089` remains unguarded, so the same failure class is still live. **Decision: adopt `pressly/goose`** (real version tracking, supports embedded SQL files via `embed.FS`, SQLite support, up/down rollback) rather than building a bespoke migration framework. Researched and confirmed both goose and golang-migrate are mature, actively maintained, real options — this is a well-solved problem in the Go ecosystem, not a gap worth filling with custom infrastructure, consistent with the day's broader theme of not reinventing what a mature tool already does better. If there's still appetite to publish something OSS, a thin wrapper tuned to patterns actually hit here (the CHECK-widening rename-recreate-copy dance) is a more realistic target than a full migration engine.

### 14. `_decisions` tables — export and drop; `event_log` is the right home for future decision/reasoning capture

`broker_decisions`, `agent_broker_decisions`, and `strategy_decisions` all lose their writers once yesterday's steering decisions land (agent broker and strategy planner retired; tool broker's rule layer retired). Export the historical data, then drop all three. **For future decision/reasoning capture** (the operator wants to keep capturing "why did the agent do X vs Y" — at minimum in dev mode — to inform steering/tooling improvements, explicitly not low-value noise): `event_log` already has the right shape (`event_type`/`category` discriminator + flexible `metadata` JSON blob) — confirmed via its schema and the existing `reflex_action` write pattern. No new polymorphic table needed. The actual task is write-site discipline going forward: whatever logs a steering decision (the new reflex `dispatch_to_agent` action, etc.) needs to populate `metadata` with real reasoning (confidence, alternatives considered, why this and not that), not just a bare event name.

### 15. Two "workspace" concepts — both retired, for different reasons

- **In-app `workspaces` table** (UI session-grouping construct, 1 row ever) — retired. Built in anticipation of a user-organization need that was never actually used. `projects` (nested under it) goes with it.
- **Filesystem-level `NANITE_WORKSPACE`** (one full separate `main.db`/XDG data directory per named "workspace," selected once at process start) — retired, also YAGNI, same as above: only `"default"` was ever materialized, and grep against the test suite found zero automated usage. The operator's recollection of "agents use this for testing" turned out to be `NANITE_DB_PATH` (a simpler, direct DB-path override) — that mechanism stays; the workspace/multi-instance layer on top of it does not.
- **Naming, for the record if a real need for DB-instance separation ever returns**: "instance" is the preferred term over "workspace" — XDG conventions govern *where* files go, not what a "workspace" means, and what this mechanism actually provided (a fully separate, isolated database and state directory) is much closer to "instance" or "profile" than the lighter-weight, switchable, in-session concept "workspace" usually implies. The naming collision between this and the in-app concept was a real, independent source of confusion, not just a coincidence.
- **Explicit boundary, not to be re-blurred later**: a separate-DB-per-instance mechanism and `consumer_id` tagging (the platform/tenancy decision from yesterday) solve different problems and are not steps on the same path. `consumer_id` — one shared DB, lightweight tagging — was chosen specifically to avoid the N-separate-config-surfaces cost of an embedded-instance-per-consumer model. If a future consumer genuinely needs full isolation from another consumer (not just tagging), that's a real, bigger tenancy decision to make explicitly later — not something a dev/test DB-isolation tool provides for free.

### 16. Confirmed dead, cut

- **`workflows` table** — schema exists, zero rows, zero code anywhere (outside its own migration) reads or writes it. `workflow_runs`/`workflow_run_steps` are the tables actually used by the real workflow-execution feature.
- **`session_stats`** — zero rows, no live call site (confirmed independently during the harness review), and its schema isn't even in the migration ledger (the owning plugin calls its own `CREATE TABLE IF NOT EXISTS` at init time). Cut the table and the out-of-band schema registration together.
- **`agent_messages_legacy_089`/`todos_legacy_d1`** — safely droppable once goose (§13) replaces the swallowed-error idempotency mechanism that's been keeping them around as a side effect.

### 17. Remaining zero-row tables — resolve per functional area, not as a separate storage sweep

`documents`/`session_objects`/`compaction_events` → session-lifecycle-recovery (next). `plugin_settings`/`trigger_rules`/`custom_actions` → plugin-system. `agent_mailbox_view`/`session_agent_overrides` → inter-agent-messaging. `a2a_push_deliveries` → follow-up to the A2A conformance task already on the list. Each gets judged with the context of the system it actually belongs to.

---

## 2026-08-18 (cont'd) — Session Lifecycle & Recovery

### 18. `session_handoffs` — correction, keep and evaluate like grounding

The audit doc's claim ("zero store methods anywhere in the codebase") is wrong — verified directly: `internal/messaging/handoff.go` has real `INSERT`/`SELECT`/`UPDATE` methods, reachable via the CLI (`message_cmd.go`), an MCP self-tool, and REST (`internal/api/messaging.go`). Zero rows means unused in practice, not unimplemented — same class of finding as `session_handoffs` being flagged live yesterday. Never fully tested, same stability-issue reason as everything else in this bucket. **Decision: keep, evaluate in real usage** (same posture as grounding) rather than cut or commit further. Design direction, not yet built: bake handoff requests into reflexes and/or procedures/workflows as a triggerable action, rather than building it as a standalone fourth steering mechanism.

### 19. HTTP-provider recovery — build a real retry path

Confirmed with much stronger evidence than yesterday's initial flag: 46 of 50 (92%) of all permanent-outcome recovery breadcrumbs are sessions that were correctly classified transient by the Recovery Broker, then escalated to a user-visible permanent error purely because `DispatchRetry` structurally can't succeed for HTTP-streamed sessions (it always calls the CLI bootdir-setup path). **Decision: build a real HTTP-provider retry path** (retry the API call directly, no bootdir involved), with explicit flagging/telemetry and a backoff strategy — not naive immediate retry, matching the existing `MaxBrokerRetries`/remediation-timeout discipline already used for the CLI path.

### 20. `compaction_events` — wire it up

Both read and write sides are fully built, tested, and migrated; the only gap is a one-line writer assignment missing at both production `CompactionPipeline{}` construction sites. Wiring this also revives the CompactionContract disclosure feature (telling the model what was preserved after a compaction event fires), which is otherwise fully built and silently dead for the same one-line reason. Low cost, real value — do it.

### 21. Scratchpad — corrected understanding, keep with TTL pruning

**Correction to the initial framing**: the scratchpad is a working-notes/thinking-space tool (the common "let the agent externalize its reasoning" pattern), not a continuity mechanism — its value is the act of an agent using it, not whatever content happens to be in it at any moment. Being usually-empty is expected, not a defect. **Decision: keep the scratchpad tool**, add TTL auto-pruning (in-memory storage is worth considering over DB-persisted, given it's genuinely ephemeral and doesn't need to survive a restart), and revisit whether a reflex should nudge usage at the right moments. Never fully tested (same stability-issue reason as `session_handoffs`) — keep-and-evaluate, not keep-and-done.

### 22. P7 (compaction-time scratchpad snapshot into `handoff_stashes`) — cut

Distinct from the scratchpad tool itself (kept, §21). P7 is the mechanism that extracts a snapshot of the scratchpad into `handoff_stashes` at compaction time for continuity purposes — but it's write-only and never read back for anything except Glass-4/long-running sessions, and the scratchpad was never meant to reliably hold the structured continuity fields P7 extracts in the first place. Dead weight regardless of the scratchpad's own value. Cut the extraction step; the scratchpad tool stays.

### 23. Glass-4 handoff — made universal; the intent classifier is cut entirely

Glass-4 (the agent-self-authored continuity handoff, previously gated to `sessions.intent == "long-running"`) becomes universal — every session gets it, not just ones that scored above a threshold. Motivation: not an observed context-size problem (none seen in practice), but general, low-cost value regardless of session type.

**The entire session-intent classification mechanism is cut alongside this** (`ClassifySessionIntent`/`ScoreIntent`, `internal/service/session_intent.go`, the `sessions.intent` column, `IsLongRunning`). Verified it has exactly one real consumer — gating Glass-4 — which is now moot. Reinforcing evidence this was the right call, not just a simplification: two of the classifier's weighted signals (`HasSessionMode`, `HasWorkspace`) already depend on mechanisms cut earlier in this same review (modes; the in-app `workspaces` concept), so the classifier would have silently degraded regardless.

### 24. Cleanup targets, session lifecycle

- `sessions.status`'s unused CHECK enum values (`sleeping`/`halted`/`terminated`, never written by any code path — apparently leaked vocabulary from `durable_agent_instances.status`) — drop from the constraint. The actively-used `halted_at`/`halted_reason` column pair (a distinct, real mechanism that happens to also mean "halted") stays untouched.
- `sessions.compaction_summary`/`compacted_at` + `UpdateSessionCompaction` — no call site, fully redundant once `compaction_events` (§20) is wired. Cut.
- `session_agent_overrides` — full CRUD, zero rows ever, no clear intended consumer found. Cut unless a real use case surfaces.
- `session_objects` — kept as-is; this is legitimate ephemeral infrastructure (card/tool-result payloads, evicted on archive), currently empty because nothing active needs it right now, not because it's unused.

### 25. The four recovery mechanisms — distinct, not redundant; restructure into a shared package namespace

Recovery Broker, Orphan/Runtime Reaper, Recovery Pack, and interrupted-turn detection each answer a genuinely different question about "what went wrong" — this is not the redundant-decision-layers problem yesterday's steering pass found; no consolidation of the mechanisms themselves is needed. What *is* needed: they're currently scattered across unrelated packages (`internal/runtime/agent/recovery`, `internal/runtime/agent/orphan_sweep.go`, `internal/service/recovery_pack.go`, and `detectInterruptedTurn` living inside `internal/api/sessions.go`) with nothing in the code layout reflecting that they're one four-part system. Restructure under a shared namespace (e.g. `internal/recovery/*`), matching the `agent/*`/`prompt/*` idiom already settled yesterday.

### 26. Observability — extend `event_log` postmortem logging to all four recovery mechanisms

Only the Recovery Broker currently writes a queryable trail (`nanite_recovery_breadcrumbs`). Orphan Sweep reconciliations, Recovery Pack replays, and manual `/recover`/`/reboot` calls are only visible in logs. Using the `event_log`-for-reasoning-capture pattern already decided (§14): log all four mechanisms' actions there, not just the broker's.

---

## 2026-08-18 (cont'd) — Inter-Agent Messaging

### 27. The "why three messaging substrates" question resolves itself from existing decisions

`agent_messages` (real, active, stays), A2A (stays, spec-conformance target), `gomsg`/`messaging_envelopes` (already cut, per the very first review pass). Not a fourth open question — just not previously written down as resolved.

### 28. Two concrete additions to the already-locked A2A conformance task

- **`a2a.task.cancel` needs a real implementation, not just a wire-level fix.** There is no `TaskManager.CancelTask` anywhere — the JSON-RPC method returns a hardcoded "not yet implemented" with no execution path at all, regardless of transport.
- **`A2APushNotifier.ProcessPendingDeliveries` needs a real caller or an explicit scope decision.** Nothing drains queued push-notification deliveries today — its own doc comment claims "called by the background worker ticker," but no such ticker exists anywhere in the repo. Either wire a real ticker, or explicitly decide push notifications aren't part of v1 conformance and document that rather than leaving it silently half-wired.

### 29. `MessageWakePolicy` resolution — fold into the construction cascade

Same shape as `resolveProvider`'s already-flagged bespoke fallback chain: `sessions.metadata` → `agent_profiles.constraints` → hardcoded global default, a fourth independent manual resolution walk in the codebase. Collapses into the role→agent→task cascade already locked for construction rather than staying its own separate mechanism.

### 30. `agent_mailbox_view` — cut

Zero call sites anywhere in the codebase (not even in `gomsg`), and its own migration comment attributes it to `internal/composer/source_mail.go`, a file that doesn't exist in the current tree. Clean cut per the standing dead-code policy.

---

## 2026-08-18 (cont'd) — Envelope System

### 31. CLI-launched agent envelope docs — fix as part of the launching work

`.sandbox/envelope-schema.md` (planted into every CLI boot directory) lists only 7 of 26+ real envelope types and claims those are "the only types the frontend can render"; its companion boot content separately points agents at `config/envelopes.yaml` and `internal/envelope/schemas/*.schema.json` for the current list — neither path exists in this repo. Fold into the CLI boot-directory content work already planned in launching: don't hardcode a static type list into planted content at all, source it from the same manifest the codegen check already validates against.

### 32. Giphy/support-ticket plugins and their envelope types — cut entirely

Confirmed demos, not real features. Cut the plugin usage and the five orphan envelope types registered through the `nanite-legacy` workaround (`kb-result`, `giphy-modal`, `resolution-capture`, `ticket-form`, `ticket-confirmation`) rather than migrating them into the real manifest.

### 33. "Volon" eradicated; `fragments-envelope` dropped; only `nanite-envelope` recognized

The word "volon" (an intermediate brand-history name) is to be removed from the codebase entirely, including the `volon-envelope` fence tag. `fragments-envelope` is also dropped — no need to preserve it. Backend and frontend both recognize `nanite-envelope` only, closing the current three-fence-tag inconsistency between the two.

### 34. `question-form` — cut

Confirmed as a pre-envelope-system legacy attempt, not a real primitive — it already gets special-cased persistence handling in the core turn loop unlike every other envelope type (only `question-form` triggers a `CreateEnvelopeInstance` call from that path), which is itself evidence it doesn't fit the uniform model. Cut alongside the rebuild of what it was trying to do as a proper composed primitive if a real need for structured multi-field user input resurfaces.

### 35. The four messaging envelope types — cut

`message-request`/`message-reply`/`message-notification`/`message-handoff` — backend-only, "no frontend component yet" (unfinished scaffolding, not a deliberate omission like `chat-loop-budget-soft-warning`'s), and redundant with `agent_messages.kind`'s own CHECK constraint. Cut.

### 36. Composition preferred wherever possible; rebuild concrete types as compositions of primitives

Standing principle for this system going forward: build concrete, feature-specific cards by composing the primitive set (`info-card`, `list-card`, `metric-card`, `progress-card`, `confirmation-card`, `table-card`, `timeline-card`, `diff-card`, plus `document-viewer`/`report-card`/`error-report`/`approval-card`/`proposal-card`) rather than minting a new top-level manifest entry per feature. Concretely: `todo-list` → `list-card` + status field; `plan-review` → `list-card` + `confirmation-card`; `subagent-spawn-approval` → `approval-card` + subagent-specific data via the existing `props` discriminator mechanism (already used for `approval-card`/`proposal-card`, just not applied consistently). Rebuild these as compositions rather than keeping them as separate types.

### 37. Interactive table with row-level actions — build as a first-class, provider-agnostic primitive

Confirmed previously tested and working as a one-off plugin implementation — proven feasible. Decision: make it a first-class primitive (extending `table-card` with an optional, schema-validated per-row/per-column `actions` capability, the same pattern `approval-card` already uses for routing responses), fully developed so any plugin can build on the pattern — not a one-off per-integration card. Design fresh rather than porting the old plugin-specific implementation; follow established patterns (the existing `props` discriminator mechanism) and industry best practice (Adaptive Cards' `Action.*` element model is a reasonable reference point) rather than reusing whatever the prior plugin-specific version did.

### 38. Envelope data excluded from replayed conversation context — real, concrete task

Verified directly: tool-auto-emitted envelope data is already stripped from the *current* turn's tool-result content the agent sees, but the full envelope JSON gets appended into the assistant's own final response text, which *is* replayed into every subsequent turn's context via normal message history — none of the four compaction stages target this specifically (they operate on tool_result blocks, not envelope-fenced content inside assistant messages). This is the concrete mechanism that makes the Torque-style "harness injects rich data, agent's context stays light" vision actually hold at session scale, not just for one turn. Real, scoped engineering task, not a redesign.

### 38a. Plugin system

- **Wire up `registers.agent_profiles[]`** against the new role/scope/agent construction model — currently accepted by the manifest schema but never acted on. Real, immediate beneficiary: Loom's agents (Curator/Weaver) would move onto proper plugin-registered agent definitions as soon as this lands.
- **`trigger_rules` and `custom_actions` — cut**, with historical context that matters: `trigger_rules` was an early version of what became reflexes; `custom_actions` was meant to be slash-command-triggered UI actions. Both zero-caller, zero-row. Whatever real need either was reaching for should be served by reflexes (for triggered behavior) and the existing, real plugin `commands[]` registration (for slash-command actions) going forward — not revived in their original form.
- **`registers.panels[]` and `registers.crud[]`** — both real, worth developing, neither urgent. Build when there's a first real consumer or genuine downtime, not before.
- **Builtin plugin enable/disable — needs a real installed/enabled state model, not the current file-rename mechanism.** Explicitly modeled on WordPress's plugin state (code present = installed, a separate flag = active/enabled), working uniformly for both builtin and subprocess plugins via GUI/CLI/API — not the current subprocess-only, disk-manifest-rename approach that doesn't even apply to builtins. Consistent with "DB is source of truth": this state belongs in the DB, not encoded as a file's presence/absence.
- **Hot-reload confirmed real** (`POST /api/plugins/reload`, dev-mode `window.__nanite_reloadPlugin`) — genuinely avoids a restart, not a false memory. One real asymmetry to close: CLI-based plugin *install* still requires a manual restart while the API-driven install path and the reload endpoint both hot-load live — have the CLI call the reload endpoint automatically after a local install instead of leaving this inconsistent. Documented as a reusable pattern: hot-reload is worth deliberately considering for anything that changes with any frequency or user-facing impact; it's fine to skip for things that rarely change or don't matter if stale — not a blanket requirement everywhere.
- **Hooks/filters coverage is genuinely rich already — corrects an earlier undersell.** Verified directly: a real six-point filter chain spans the whole turn loop (`FilterUserMessage` → `FilterSystemPrompt` → `FilterContextWindow` → `FilterToolResult` → `FilterAssistantResponse` → `FilterEnvelopeData`), plus reflexes have their own `FilterReflexState`/`FilterReflexAction` and `EmitReflexFired`/`EmitReflexActionStaged`, plus a real spread of lifecycle events (session archived, agent switched, artifact created, bookmark changed, config changed). This is not a gap needing a redesign — it's already close to comprehensive. **One real, concrete gap found**: there's a filter for tool *results* (post-execution) but none for tool *selection* — a plugin can't currently add/remove/reshape which tools get offered to the model for a turn, only react after the fact. Add a `FilterToolSelection` hook alongside the tool-selection filter stack kept from yesterday's tool-broker retirement.
- **Middleware exists today, but only at the HTTP layer, and it's not plugin-extensible.** `internal/server/server.go` has a real, standard `net/http` middleware chain (recover → logging → CORS → auth → caller-identity → body-limit) — genuinely Laravel-shaped, just scoped to raw HTTP requests. **Decision: don't build a second, general "middleware" concept.** The turn-loop's `Filter*`/`Emit*` system already functionally serves that role for the chat-turn domain — a second name for the same pattern would recreate the exact naming-collision problem this whole review has spent two days curing elsewhere. Instead: **make the existing HTTP middleware chain plugin-extensible**, the one place true middleware exists today but has zero plugin reach (a plugin can register new routes, not inject into the chain wrapping every request). Complementary to the filter/hook system, not a replacement or a hybrid — same underlying pattern, applied to the one layer that's currently missing it.

### 39. Envelope system naming — "Cards," not "Blocks" — locked

Confirmed. Clean layering, stated by the operator: **Envelope** = wire protocol only. **Card** = the rendered UI system (this is largely a UI system — Cards is the right register). A Card may internally be *composed of* smaller elements — data, interactivity, sub-regions — which is a legitimate place composition-oriented vocabulary (like "block") could still apply *inside* a card's internal structure later, without colliding with the system-level name or with `ContentBlock`'s existing meaning. Not designing that internal composition layer now — just noting the distinction so it doesn't get lost when §36/§37's composition work happens.

Considered "Blocks" (a ChatGPT-sourced suggestion, with real precedent in Slack's Block Kit and Notion's content-block vocabulary) against "Cards." **Decision: "Cards."** The deciding factor, found by checking the actual codebase rather than reasoning about industry terminology alone: this codebase already has a dense, established, unrelated meaning for "block" at exactly the layer a UI-card system would sit next to — `llmtypes.ContentBlock`, `ToolUseBlock`, `SlotBlocks` (82+ call sites across the provider/context/chat-loop layer), all meaning "a chunk of raw LLM message content," not a rendered UI element. Naming the new system "Blocks" would recreate, in real time, the exact naming-collision pattern this entire review has spent two days finding and fixing elsewhere in this codebase. "Cards" has no such collision, and it's already the organic, bottom-up naming choice for most of the 26 (soon fewer) envelope types (`approval-card`, `table-card`, `diff-card`, etc.) — naming the system "Cards" documents a convention that already mostly exists rather than inventing a new one. "Envelope" stays scoped to the transport/wire-shape layer, as intended either way.
