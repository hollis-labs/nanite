# Engineering Task Breakdown

Concrete, sequenced work implementing `architecture/*.md`. Full reasoning/verification trail for every item: `docs/architecture-decision-log-2026-08-17.md`.

**Status:** Ready to schedule. Phases are ordered by dependency; items inside a phase can be worked in any order unless noted.

**Sequencing principle for Phase 0**: anything that's pure subtraction or renaming, and doesn't functionally depend on the new construction/launching schema (Phase 1/2) to exist first, belongs here — even if it's conceptually "steering work" or "Cards work." Staging all of it early reduces the surface area everything downstream has to reason about. A few items below were originally scoped into later phases by association with their subsystem; they're listed here instead because their *replacement mechanism already exists*, or there's no replacement needed at all.

**No agents run in production during this work** — there is no live-traffic constraint on any of this. Sequencing that remains (agent broker → reflexes, the boot-profile catalog's resolvers, `resolveProvider`'s cascade fold-in, etc.) is a genuine build-then-cut dependency — deleting the old mechanism before its replacement exists would leave a real capability gap once agents run again — not caution about breaking something live. Where a cut requires touching another still-referencing piece of code in the same change (e.g. modes + the agent broker's mode-dependent rules), that's an ordinary "the code won't compile otherwise" constraint, not a safety one.

---

## Phase 0 — Independent fixes, cuts, and renames (no schema dependency)

### Fixes

1. Fix model pinning — 6 of 8 `.nanite/durable-agents/*.yaml` still pin a retired model ID.
2. Fix `RequestStart` to call straight through to the real `Start()`, matching `RequestResume`.
3. Fix durable-agent wake `CallerType` mistagging (`CallerChat` → `CallerBackground`). **Blocks** any future caller-type-aware steering narrowing.
4. Build a real HTTP-provider recovery retry path, with flagging and backoff.
5. Build `internal/llm/ollama` — a real, currently-used local provider that's currently broken, not dead code.
6. Turn on `NANITE_TOOLS_LAZY_LOAD` by default; tune against real sessions.
7. Harden `IsFirstPartyBuiltinServerName` — same bug shape as a prior incident, currently a hand-maintained 4-name switch.
8. A2A conformance: real method names (`SendMessage`/`GetTask`/`CancelTask`, not `a2a.task.*`), a real `TaskManager.CancelTask` (currently no execution path exists), and a decision on `A2APushNotifier.ProcessPendingDeliveries` (real ticker, or explicit v1 scope-out).
9. Adopt `pressly/goose` for migrations, replacing the no-ledger every-boot-rerun model.
10. Convert builtin/seed agent profiles to one-time seed data (stop `AutoIngestAgents` re-overwriting `source='internal'` rows every boot) — closes a real, currently-live bug where a GUI customization to a builtin agent is silently reverted on restart. Bounded to that one behavior; doesn't need the full `roles`/`agents` schema split.

### Cuts — replacement already exists, or none needed

11. **Strategy planner, full removal.** Its replacement (the reaper's activity-based termination) is already built and shipped — no reason to wait for the steering redesign.
12. **`AgentConstraints.MaxTurns`**, and the `chat-loop-budget-soft-warning` signal that depends on it. Same reasoning as #11.
13. **`question-form`** (Cards) — special-cased in one specific place in the turn loop; bounded deletion, no dependency on the Cards primitive-composition work.
14. **The four backend-only messaging card types** (`message-request`/`reply`/`notification`/`handoff`) — no frontend component ever existed for them.
15. **Giphy/oembed/support-ticket plugins and their orphan card types** (`kb-result`, `giphy-modal`, `resolution-capture`, `ticket-form`, `ticket-confirmation`) — confirmed demos. Bundle the CLAUDE.md/doc reference cleanup into the same change.
16. **The external-format agent-import tier** (`.claude/agents/`, codex/gemini/opencode config import in `Discover()`) — no dependency on the new `roles`/`agents` schema.
17. **`roleSkills:`/`agent_known_skills`-as-currently-used** — confirmed zero functional effect on prompt assembly today, so removing the frontmatter parsing and seeding code creates no functional gap; doesn't need to wait for the FK-based `agent_skills` replacement.
18. **`internal/messaging/gomsg`**, `agent_cycles`, `tool_enrichments` write path, the unscheduled known-tools/skills TTL reaper, `agent_boot_plans`, `providers.base_url`/`api_key` columns, dead `internal/config.Config` fields, `workflows` table, `session_stats`, `agent_mailbox_view`, `trigger_rules`, `custom_actions`, `session_agent_overrides` — verify each against current code before cutting, not just this list. **Do not cut `session_handoffs`** (confirmed live, twice, by two independent checks).
19. **`agent_messages_legacy_089`/`todos_legacy_d1`** — after #9 (goose) lands.
20. Retire the in-app `workspaces` table and the filesystem-level `NANITE_WORKSPACE` multi-instance mechanism (keep `NANITE_DB_PATH`).
21. **Modes, in full** — mechanical, *with one coupled step*: the agent broker's mode-dependent rules (2–4, the `mode=work`/`mode=plan` thresholds) must be stubbed out in the same change, or the broker breaks referencing a deleted signal. One PR, not two efforts landing separately.
22. **Skill broker / tool broker formal-abstraction removal** — delete the rule-matching wrapper (`go-toolbroker`/`NaniteDefaultRules`, `internal/skillbroker`), call the existing filter/selection logic directly. Mechanical, but touches a currently-live, actively-exercised path — tag for real testing, unlike the pure-dead-code cuts above.
23. **`broker_decisions`/`agent_broker_decisions`/`strategy_decisions`** — export the data, then drop. Follows naturally once #11, #21, and #22 land (that's what removes their writers).
24. Housekeeping: review/delete `.nanite/agents/agridd-project-manager.md`/`proxima.md`; keep `torque-task-writer.md`.
25. **`sessions.status`'s unused CHECK enum values** (`sleeping`/`halted`/`terminated`, never written by any code path) — drop from the constraint. The real, actively-used `halted_at`/`halted_reason` column pair stays.
26. **`sessions.compaction_summary`/`compacted_at` + `UpdateSessionCompaction`** — zero call sites today, no need to wait for `compaction_events` wiring.
27. **P7** (the scratchpad→`handoff_stashes` compaction-time snapshot) — cut the extraction step only; the scratchpad tool and Glass-4 both stay untouched.
28. **`ClassifySessionIntent`/`ScoreIntent`/`sessions.intent`**, alongside making Glass-4 universal (remove the `IsLongRunning` gate so every session gets it, then delete the now-unused classifier).
29. **`prompt_templates`/`agent_prompt_templates`** — redundant with the new Role-based `system_prompt` and barely adopted in practice (one real assignment ever, added early in development before agents were properly set up). Relocate the compaction-disclosure content first: **make it a single, universal, hardcoded piece of content in the compaction pipeline itself** — every chat agent gets it unconditionally when relevant, not a per-agent-assignable template. The 4 existing variants were keyed by *mode* (general/code/plan/research); since modes are being cut, this collapses to one unified disclosure message rather than 4 mode-branched ones — genuinely simpler, not just relocated. Then cut the rest of the mechanism.
30. **`templates`** (the separate, unrelated output-formatting-snippet table) — has a real CRUD/REST surface but no confirmed real consumer found, and other ways to achieve the same thing exist today. Cut entirely. If revisited later, use a clearer name than the bare `templates` — `output_templates` or an `output/templates` grouping, not a name vague enough to need re-explaining two months later.

### Renames

25. **PTY naming scrub** (`shouldUsePTY`, `IsPTYProvider`, the `pty-*` provider-string convention) — the *name* can be fixed now, independent of `runtime_kind`'s full migration (still real Phase 2 work). Rename now; redesign the mechanism later.
26. **`internal/recovery/*` namespace regrouping** (Recovery Broker, Orphan Sweep, Recovery Pack, interrupted-turn detection under one shared prefix) — mechanical package move, but touches live code that fires regularly. Real testing after the move, not a zero-risk rename.
27. **"Volon" eradicated** from the codebase entirely, including the `volon-envelope` fence tag; **`fragments-envelope` dropped**. Backend and frontend both recognize `nanite-envelope` only.

**Deliberately not included here**: renaming `promptrouter`'s "reflex" vocabulary (`BuiltinReflexes()`/`Reflex`) — since `promptrouter` itself is being absorbed into reflexes in Phase 3, renaming its internals now and migrating them shortly after is likely wasted motion. Do this renaming as part of the actual migration, not as standalone cleanup first.

**Open question, not yet decided**: `prompt_templates` vs. `templates` — flagged as a confusing naming collision but never explicitly decided as a rename. Add to this sweep, or leave deferred?

---

## Phase 1 — Agent Construction (foundational)

See `architecture/01-agent-construction.md` for the target schema. Migrations: `roles`, `agents` composition columns (`role_id`, `consumer_id`, `model_id`, `instance_mode`, `runtime_kind`), `consumers`, `known_tools`, `agent_tools`, `agent_dispatch_allowlist`, fix `agent_skills`'/`agent_projects`' missing FKs, fix the `models` table's `models.dev` sync target, add the reflex opt-out field. Kill the file-reingest-on-boot pattern generally. Build the assignment UI/API. Data-migrate every current `.nanite/agents/*.md` onto `roles`/`agents`.

## Phase 2 — Agent Launching (depends on Phase 1's `runtime_kind` column)

Wire `runtime_kind` as the single CLI/API routing *mechanism* (naming already fixed in Phase 0 #25), replacing four string-matching call sites. Retire the boot-profile catalog as a standalone system; port forward the `cmd`/`http` dynamic-resolver capability and the mandatory post-compaction re-read as first-class launching-time mechanisms. Collapse `resolveProvider` into the construction cascade.

## Phase 3 — Steering (depends on Phase 1's reflex opt-out field)

Design and add the `dispatch_to_agent` reflex action kind; migrate the agent broker's remaining rules and promptrouter's phrase catalog into reflex predicates (fixing the two phantom-agent reflex entries, and doing the `promptrouter` naming cleanup as part of this migration). Build the missing `agent_reflex_propose` self-tool and test `pending_reflexes` in real sessions. Test grounding in real sessions. Add `FilterToolSelection`.

## Phase 4 — Harness

Verify the reaper's real-world behavior before further idle-timeout tuning. Unify the three run-another-agent surfaces into one shared request/result type.

## Phase 5 — Session Lifecycle, Messaging, Cards, Plugins

- **Session lifecycle**: extend `event_log` postmortem logging to all four recovery mechanisms. Wire `compaction_events` (relocate the compaction-disclosure text off `prompt_templates` as part of this, per Phase 0 #29). Add TTL pruning to the scratchpad tool.
- **Cards**: rebuild `todo-list`/`plan-review`/`subagent-spawn-approval` as compositions of the primitive set. Build the interactive-table-with-row-actions primitive (schema-validated actions, reusing the `approval-card` response-routing pattern). Build the context-replay exclusion (Card data excluded from replayed conversation history) — the highest-leverage single item in this phase. Fix CLI-agent boot content to source the type list dynamically instead of a hardcoded stale list.
- **Plugins**: wire `registers.agent_profiles[]`. Build the real installed/enabled state model (WordPress-style, DB-backed, builtin+subprocess uniform). Close the CLI-install-vs-hot-reload asymmetry. Develop `registers.panels[]` and `registers.crud[]` (not urgent). Make the HTTP middleware chain plugin-extensible.

## Phase 6 — The one open experiment, and documentation follow-through

1. **Run the CLI-vs-API experiment for durable agents** (made cheap by Phase 2's `runtime_kind` field) — route one real Curator wake through a CLI-based subprocess, measure whether anything in Nanite's retry/escalation policy is lost and whether session dormancy behaves sanely. This is the one genuinely unresolved architecture question from the whole review.
2. **Old-docs archival pass** — `docs/architecture/`, `docs/decisions/`, `docs/audits/` (the April tree, distinct from `docs/system-audit/2026-08-17/`), and most top-level `docs/*.md` predate `docs/engineering/` and are now superseded where they overlap. Resolve the two colliding `ADR-001` files specifically. Proposed policy (needs confirmation before executing): move genuinely superseded, no-remaining-value docs to a clearly-marked archive location or delete them per the standing dead-code policy; anything with real historical value that isn't duplicated here gets an explicit "superseded by `docs/engineering/...`" banner rather than being silently left to look current.
3. **Frontend architecture pass** — deliberately deferred until enough of the above lands, since it will resolve or inform a meaningful amount of frontend work. Planned to be driven by a browser-based agent session once ready.
4. **GUI start-surface reconciliation** (Chat/Harness/Durable/Recipe tabs against the new construction model) — folds into the frontend pass above.
