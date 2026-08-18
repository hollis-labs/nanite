# Nanite Agent System — Task Breakdown

**Date:** 2026-08-18
**Companion:** `docs/architecture-agents-2026-08-18.md` (the target design these tasks implement) and `docs/architecture-decision-log-2026-08-17.md` (the full reasoning/verification trail behind each decision).
**Status:** Ready to schedule. Phases are ordered by dependency, not by priority within a phase — items inside a phase can be worked in any order unless a note says otherwise.

---

## Phase 0 — Independent fixes (no schema dependency, start immediately)

These don't wait on anything below and don't block anything below either, except where noted.

1. **Fix model pinning.** 6 of 8 `.nanite/durable-agents/*.yaml` (`atlas-curator`, `atlas-librarian`, `content-strategist`, `content-writer`, `ideation-partner`, `loom-weaver`) still pin `model: claude-sonnet-4-20250514` (retired, 404s live). Clear the field so it defers to the working default-model resolver, matching `loom-curator.yaml`/`orchestrator.yaml`. Six one-line edits.

2. **Fix `RequestStart`** (`internal/service/durable_agents.go:496`). Currently only flips `durable_agent_instances.status` to `start_requested` and returns — nothing transitions it further. Align with `RequestResume` (`:589`), which calls straight through to the real `Resume`/`Start`.

3. **Fix durable-agent wake `CallerType` mistagging.** The wake delivery path (`durable_agents.go:485` `deliverWakePrompt` → `chatDurableAgentRuntimeController.SendMessage` → `ChatService.HandleMessage`) stamps `CallerChat` instead of `CallerBackground`. `internal/dispatcher` already defines `CallerBackground` and stamps it correctly elsewhere — this is one call site to fix. **Blocks**: any future caller-type-aware steering narrowing depends on this being correct first.

4. **Decide and implement the HTTP-provider recovery scope.** `DispatchRetry` (`internal/runtime/agent/recovery/broker.go:327`) is unconditionally CLI-only by construction; the call site is already gated off for HTTP-provider sessions (`chat_http_broker_notify.go`'s `HasBootdirLayout` check, PR #246). Decide: is falling back to Orphan Sweep/cold-boot sufficient for HTTP-provider sessions long-term, or build a real HTTP-provider retry path (retry the API call directly, no bootdir involved)? Implement whichever is chosen.

5. **Fix Ollama registration.** `chat.InferProvider` already routes Ollama-shaped model names (`llama*`, `gemma*`, `model:tag`) to a provider named `"ollama"` that was never (re-)registered as a real `llmcontracts.Provider`. Build `internal/llm/ollama` (mirroring the `internal/llm/anthropic`/`internal/llm/openai` shape) and register it in `cmd/nanite/main.go:initProviders`.

6. **Turn on `NANITE_TOOLS_LAZY_LOAD`** by default; tune the essential/lazy partitioning threshold against real sessions if needed.

7. **`IsFirstPartyBuiltinServerName` structural hardening** (`internal/mcp/naming.go:132`). Currently a hardcoded 4-name switch (`self`/`dev`/`code`/`general`) — the same bug shape as the incident it was built to fix (commit `5144590`), just currently patched. Make new first-party builtin registrations self-protecting by construction rather than requiring a manual switch-statement edit.

8. **A2A method-name conformance.** `internal/api/a2a_jsonrpc.go` uses invented method names (`a2a.task.submit`, etc.) instead of the real spec's (`SendMessage`/`GetTask`/`CancelTask`) — already self-flagged in the code. Fix the wire-level method names; `TaskManager`'s routing logic underneath is already correct and doesn't change.

9. **Dead-code cut pass** (per the standing aggressive-removal policy — cut, don't flag):
   - `internal/messaging/gomsg` + `envelope_bridge.go`'s bridge functions — zero callers outside tests.
   - `agent_cycles` (table + `internal/service/agent_cycles.go`) — zero production callers, doc comment admits the value is never consumed.
   - `tool_enrichments` write path (`UpsertToolEnrichment`) — zero callers outside tests. Read path stays wired but is dead in practice without a writer; cut both together unless a real writer is planned.
   - Known-tools/skills TTL reaper (`ReapExpiredAgentKnownTools`/`ReapExpiredAgentKnownSkills`) — implemented, tested, never scheduled. Either wire up a ticker or cut.
   - `agent_boot_plans` (table + `internal/store/agent_boot_plans.go` + `internal/api/agent_boot_plans.go`) — confirmed an earlier, abandoned attempt at the problem the boot-profile catalog (Phase 2) already solves differently. Cut.
   - `providers.base_url`/`api_key` DB columns — full CRUD surface, zero effect on request construction. Either wire up for real or cut the columns and the CRUD surface with them.
   - `internal/config.Config`'s unread fields (`Role`, `BootProfiles`, `Defaults.BootProfiles`, `WritePaths`, `ProtectedPaths`, `HooksDir`) — parsed and merged, never read anywhere else.
   - **Do not cut** `session_handoffs` (confirmed live: real store methods, real callers via CLI/MCP self-tool/REST) — an earlier audit pass claimed this was dead; it isn't.

10. **Housekeeping**: review then delete `.nanite/agents/agridd-project-manager.md` and `proxima.md` (may be recreated under new names against the new schema, or dropped — no rush). Keep `torque-task-writer.md` as-is.

---

## Phase 1 — Agent Construction (foundational; most later work depends on this)

1. **Schema migration: `roles` table.** New table — `id`, `name`, `system_prompt`, plus optional default tool/skill/permission hints a new `agents` composition can start from.

2. **Schema migration: `agents` composition columns.** Extend the existing `agent_profiles` table (or its successor — naming stays `agents`/`agent_profiles`, no rename) with: `role_id` (FK → `roles`), `consumer_id` (FK → new `consumers` table, nullable), `model_id` (FK → `models`), `instance_mode`, `runtime_kind` (needed by Phase 2, add here so Phase 2 doesn't need its own migration). `class` stays a column here, resolved via the cascade (§3.1 of the architecture doc), not moved to `roles`.

3. **Schema migration: `consumers` table.** Minimal — `id`, `name`. Seed with `loom` as the first row.

4. **Schema migration: `known_tools` catalog.** New table — doesn't exist today. `id`, `name`, `server`, `status` (`available`/`unavailable`). Live-synced from the tool broker's catalog on connect/disconnect; `unavailable` on disconnect, not deleted (avoids an agent silently losing a declared tool because a server was briefly offline).

5. **Schema migration: `agent_tools` join table.** `agent_id` FK → `agents`, `tool_id` FK → `known_tools`. Replaces `tools:`/`toolPermissions:`/`roleTools:` entirely. Add a flag on `known_tools` (or a join attribute) marking certain tools (`request_tools`, `tool_list`, `tool_describe`) as always-included regardless of an agent's own `agent_tools` rows — the default-tools-baseline mechanism.

6. **Schema migration: `agent_dispatch_allowlist` join table.** Separate from `agent_tools` — which tools this agent may authorize a subagent it dispatches to use. Replaces `agent_profiles.parent_dispatch_allowlist` (currently a bare, un-FK'd JSON array).

7. **Schema migration: `agent_skills` join table fix.** Add the FK to `agents.id` (currently `agent_id TEXT NOT NULL` with no FK, per the schema's own "agents may be file-based" comment — no longer true once files stop being the source of truth). Wire real assignment through the same construction flow as `agent_tools`.

8. **Fix `agent_projects`'s missing FK** (same "agents may be file-based" comment, same fix as above) — this is the existing precedent for the scope-reference pattern; make it structurally correct before building more scope dimensions on top of it.

9. **Fix the `models` table's sync target.** `models.dev`'s live refresher (`syncCatalogToRegistry`) currently feeds an in-memory overlay (`pkg/models`) for pricing/context-window lookups only — it needs to also populate/update the DB `models` table's availability/status so `agents.model_id` can FK against something that reflects reality (a retired model becomes visibly retired, not a silent 404 at dispatch time).

10. **Add the reflex opt-out field.** `agent_reflexes` already supports class-bound (`agent_id IS NULL`) vs. per-agent rows — add a field distinguishing "required, cannot opt out" from "default-on, agent may opt out."

11. **Cut the external-format agent-import tier** from `internal/agent.Discover()` (the adapter-discovery priority-5+ path reading `.claude/agents/`, codex/gemini/opencode formats).

12. **Convert builtin/seed profiles to one-time seed data.** Stop `AutoIngestAgents` from re-overwriting `source='internal'` rows from the compiled file on every boot — insert once if missing, leave DB-side customizations alone afterward. This closes the live bug where a GUI edit to a builtin agent is silently reverted on restart.

13. **Kill the file re-ingest-on-boot pattern generally** (`agent.Discover()` → `AutoIngestAgents` for anything other than the one-time seed step above). GUI/API/CLI become the only write paths for non-seed agents.

14. **Build the assignment UI/API** for the new model: role selection, scope (project) assignment, tool/skill grants via `agent_tools`/`agent_skills`, all through the cascading override — this is what actually lets an operator create an agent under the new schema instead of hand-editing a file.

15. **Data migration**: map every currently-discoverable `.nanite/agents/*.md` definition onto the new `roles`/`agents` split. This is the point where the `.nanite/agents/*.md` sprawl actually gets cleaned up, not just re-architected around.

---

## Phase 2 — Agent Launching (depends on Phase 1's `runtime_kind` column existing)

1. **Wire `agents.runtime_kind` (cli|api) as the single routing decision**, replacing the four-site string-prefix convention: `chat.IsCLIProvider`/`NormalizeCLIProvider` (`internal/chat/engine.go:254-303`), `shouldUsePTY` (`internal/runtime/agent/factory.go:59`), `agent_deps.go`'s `stripRegistryPrefix`, and the boot-profile-catalog's own `pty-` prefix layering (retired in this same phase, see below).

2. **Scrub "PTY" naming.** Rename `shouldUsePTY` → something reflecting reality (e.g. `usesLongLivedStreamingStdio` or similar — exact name is an implementer call), `IsPTYProvider` → drop or rename, retire the `pty-*` provider-string convention in favor of the typed field from item 1. No functional PTY exists anywhere in the runtime; the naming should stop implying one does.

3. **Retire the boot-profile catalog as a standalone system.** Remove `bootprofile.Registry`, the `boot_profile_catalog_path` config surface, the `MetaHarnessManager.tsx` GUI, and the `bootprofile:<id>` provider-string convention.

4. **Port forward, as first-class launching-time mechanisms (not YAML-catalog-gated):**
   - The `cmd`/`http` dynamic-resolver capability — DB-configurable, available to every agent, not opt-in to a separate catalog.
   - Mandatory, code-driven post-compaction re-read of the project's real `CLAUDE.md`/`AGENTS.md` (no per-agent flag).
   - Nothing else from the catalog carries forward — "lineage" is dropped, cross-app portability is deliberately not rebuilt as a default (see architecture doc §4.1 for the explicit opt-in path if ever needed).

5. **Build the `internal/llm/ollama` adapter** (see Phase 0, item 5 — listed here too since it's genuinely launching-layer work, can be done in either phase).

6. **Collapse `resolveProvider`'s fallback chain** into "read the already-cascade-resolved `model_id`" from the Phase 1 composition model — remove the independent session → agent-profile-default → user-settings-default → fallback-chain → model-inferred walk.

7. **Document (in code comments and the architecture doc, already partially done) the provider-registration-vs-metadata split** so it doesn't drift back into confusion — this is a documentation/clarity task, not a code change, but worth tracking so it doesn't get lost.

---

## Phase 3 — Steering (depends on Phase 1's reflex opt-out field; benefits from Phase 2's `runtime_kind`)

1. **Design and add the `dispatch_to_agent` reflex action kind** (alongside the existing `inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule`) — the mechanism that absorbs the agent broker's real job.

2. **Migrate the agent broker's rule logic into reflex predicates.** The 6-rule deterministic broker (`agentkit/broker`, `DeterministicBroker.Decide`) — reflex-match priority, mode/scope-tier thresholds — becomes reflex trigger specs. Retire `internal/service/chat_broker_dispatch.go`'s `attemptBrokerDispatch` call site once migrated.

3. **Migrate `promptrouter`'s phrase catalog into reflex predicates** (regex-match triggers, which the reflex engine already supports). Fix the two phantom entries in the process (`documentor-mention`/`strategist-mention`, which match real phrases but resolve to no actual agent profile). Retire `internal/promptrouter` once migrated.

4. **Cut modes, in full**: `modes` table, `sessions.current_mode_id` + `/mode`/`/chat`/`/plan`/`/work` slash commands, `agent_modes` (legacy), `agent_mode_assignments` (junction), `classify.ClassifyMode`. **Companion task, don't skip**: update the context/slot system's `INV4` invariant (`internal/context/INVARIANTS.md`, `slot_invariants_test.go`) — it becomes vacuous once Session Mode is gone and needs a deliberate small edit, not silent staleness in a subsystem otherwise left alone.

5. **Cut the strategy planner in full**: `internal/strategy` package, `chat_strategy.go`'s `planStrategyForTurn` call site, `strategy_decisions` table (or leave the table as historical telemetry, operator's call).

6. **Retire the skill broker and tool broker as formal abstractions.** Remove `internal/skillbroker`'s ranking layer and `go-toolbroker`/`NaniteDefaultRules`'s rule-matching layer. Keep: the `skills`/`known_tools` catalogs, and the real filter/selection logic (permissions, allowlist, chat-surface exclusion, progressive discovery) — these move to being called directly rather than wrapped in a broker interface.

7. **Build the missing `agent_reflex_propose` self-tool** so `pending_reflexes` can actually be tested (currently has a complete review/approve backend and REST API but no way for an agent to call it).

8. **Test `pending_reflexes` in real sessions** — decide keep/re-architect/cut based on results, not static review.

9. **Test grounding (`internal/grounding`) in real sessions** as a possible complement to reflexes — decide integrate/re-architect/cut based on results.

10. **Implementation-time correctness fixes** (deferred from architecture, not forgotten):
    - Tool concurrency-safety classification: replace name-heuristic (`GetToolMeta`) with a declared-metadata field, or at minimum audit the heuristic against the full live tool catalog for misclassifications.
    - Truncation review, broadly: audit every truncation path (`tool_result_cache`, the file-based `internal/truncate` fallback, the 80-character progressive-discovery catalog description) for the specific failure mode already seen once (truncated content causing a hallucinated capability gap) before extending truncation anywhere else.

---

## Phase 4 — Harness (mostly independent; shares `runtime_kind` work with Phase 2)

1. **Cut `AgentConstraints.MaxTurns`** (default 75, soft/telemetry-only, redundant with the already-cut strategy-planner budget). Retire the `chat-loop-budget-soft-warning` SSE event with it — it becomes dead once nothing produces the underlying signal.

2. **Verify the reaper's real-world behavior before further tuning.** The `d92d8cf` activity-reset fix should reduce false reaps but hasn't been validated against the operator's stated experience ("more false reaps than idle or runaway agents by far"). Instrument or sample real reap events over a real usage window; confirm agents flagged idle are actually idle before making any further changes to `IdleTimeoutSeconds`/reaper thresholds.

3. **Unify the three "run another agent" surfaces** (REST delegation — `delegation.go`; LLM-triggered subagent — `subagent_runner.go`; durable-agent wake — `durable_agents.go`) into one shared request/result type and one completion-signaling mechanism, while keeping their genuinely different entry semantics (synchronous-drain for delegation, fabrication/zero-output detection for subagents, lifecycle state machine for durable). The goal is one shared shape underneath three call conventions, not forcing all three into an identical caller experience.

---

## Phase 5 — Verification and the one open experiment

1. **Run the CLI-vs-API experiment for durable agents.** Route one real Curator wake through a CLI-wrapped subprocess instead of the API-harness path (made cheap by Phase 2's `runtime_kind` field). Measure: does the wake complete the task correctly; is anything in Nanite's retry/escalation policy silently lost by ceding the tool-calling loop to the CLI tool's own harness; does session lifecycle (dormancy, resume) behave sanely for a CLI-wrapped subprocess held between wakes. This is the one genuinely unresolved architecture question from the whole review — resolve it empirically, not with more document analysis.

2. **GUI start-surface reconciliation.** Trace `StartSurfaceDialog.tsx`'s four tabs (Chat/Harness/Durable/Recipe) against the Phase 1 construction model and redesign as needed — likely "Recipe" collapses into "create with prefilled fields," but this hasn't been designed yet.

3. **GUI session visibility.** Gate background/system-agent (durable) session visibility in the GUI session list behind the `developer_mode` flag, matching the existing gate on `dev_bash` and the deferred interactive-terminal UI.

---

## Not included here — next session

Session-lifecycle-recovery's remaining territory, the envelope system, the plugin system, the storage layer beyond what Phase 1-4 touch, inter-agent messaging beyond the already-shipped subagent-reply-delivery fix, and a dedicated frontend architecture pass. These weren't part of this review and shouldn't be inferred from it.
