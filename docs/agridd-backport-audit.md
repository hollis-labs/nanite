# Agridd → Nanite Backport Reconciliation Audit

**Date:** 2026-05-25
**Auditor scope:** READ-ONLY. No source modified in either repo.
**Source (ahead):** `/Users/chrispian/dev/hollis-labs/apps/agridd`
**Target (backport-to):** `/Users/chrispian/dev/hollis-labs/apps/nanite`
**Authority rule:** Nanite mainline is authoritative. This audit does not propose
reverting nanite-only work (subagent checkpoint/resume, file-SOT profile cleanup,
managed-agent editability Phase 1/2, `meta_harnesses`, `internal/reflex` M3 catalog).
**Plan of record:** `agridd/docs/nanite-backport-plan.md`.

---

## Summary

The durable-agent platform backport is **substantially complete**. The store
substrate, migrations, control-plane API (`/api/harness/v1`, durable-agent
admin/recipes/wake/boot-plans/capabilities/agent-builder/frontend-readiness), and
the frontend are all present in nanite. The migration renumbering matches the
plan's offset table exactly (065→085, zero gaps).

The gaps that remain fall into two buckets:

1. **One real product/runtime capability never came over: the FU-30 reflex
   *engine*** (`internal/agent/reflexes/`). Nanite has the full store substrate
   for it (`store/agent_reflexes.go` is byte-identical, migration `074_agent_reflexes.sql`
   is present) but the runtime engine that *reads* those rows and injects
   reminders/halts per turn was not backported. This is the headline miss.

2. **Everything else agridd-only is either an exploration artifact the plan
   explicitly excludes** (Torque monitor-loop driver, probes, the go-agent-runtime
   second launch stack, agentdb spike, relay/composer FU-spikes) **or test-only
   files whose implementations already exist in nanite.**

### Category counts (agridd-only `.go` files + whole packages)

| Category | Count (files) | Notable packages |
|---|---|---|
| **MISSED — SHOULD BACKPORT** | 9 non-test + 5 tests | `agent/reflexes` engine, `service/chat_reflexes.go`, plugin reflex hooks, `mcp/args_normalize.go` |
| **DELIBERATELY EXCLUDED** | ~24 non-test | `monitor`, `probes`, `composer`, `relay`, `agentdb`, `agent/skills`, `agent/urn`, runtime-bind stack, `api/{session_halt,compose,relay,halt_escalator,role_seed_priority}` |
| **ALREADY PRESENT (impl exists; tests/equivalents)** | ~6 (mostly test files) | `store/agent_reflexes_test.go`, `store/agent_runtime_test.go`, `service/agent_cycles_test.go`, `context_known_tools_test.go` |
| **UNCLEAR — NEEDS OPERATOR DECISION** | 3 capability clusters | reflex engine scope, `args_normalize`, compaction/preflight reapers |

Whole agridd-only packages: `internal/agent/reflexes`, `internal/agent/skills`,
`internal/agentdb`, `internal/composer`, `internal/monitor`, `internal/probes`,
`internal/relay`. (`internal/agent/urn` package exists but has zero non-test
importers in agridd.)

Nanite-only packages confirming independent forward motion:
`internal/reflex` (M3 input-pattern→role dispatch catalog — **different concept**,
do not conflate with the reflex engine), `internal/subagent` (retry/audit/resume).

---

## Reflex Engine (dependency closure + backport order)

**This is the one true product-capability miss.** Agridd's
`internal/agent/reflexes/` is the **FU-30 reflex runtime engine**: predicate
evaluation over a windowed session snapshot, action dispatch (inject_reminder /
halt_session / force_tool_choice / add_schedule / send_message), and base-reflex
seeding. It is invoked **per chat turn** (not by a background monitor loop).

> ⚠️ Naming collision: nanite's `internal/reflex/` is an **unrelated** M3
> "playbook" catalog that maps user-input phrases to dispatch roles. It is NOT
> the engine. The engine belongs under `internal/agent/reflexes/`.

### Files to backport (the engine itself)

| File | Role |
|---|---|
| `internal/agent/reflexes/types.go` | `State`, `MessageSignal`, `EventSignal`, `AppliedAction(s)` |
| `internal/agent/reflexes/state.go` | `StateCollector` — builds State from store |
| `internal/agent/reflexes/evaluator.go` | `EvaluateTrigger` — JSON trigger-spec AST interpreter (conjunction contract) |
| `internal/agent/reflexes/executor.go` | `Executor` + Halt/Schedule/SendMessage hooks |
| `internal/agent/reflexes/engine.go` | `Engine`, `NewEngine`, `SetPluginHooks`, `Evaluate`/`EvaluateState` |
| `internal/agent/reflexes/seeds.go` | `SeedBaseReflexes`, `BaseSeeds` (per-class base reflexes) |
| `internal/agent/reflexes/*_test.go` | evaluator/state/engine-plugin tests (encodes the no-false-positive contract — backport with the code) |
| `internal/service/chat_reflexes.go` | `evaluateAndInjectReflexes`, `formatReflexReminder`, `appendUserContext` — the per-turn integration |

### Dependency closure — what the engine needs, and its status in nanite

| Dependency | Status in nanite | Notes |
|---|---|---|
| `store.AgentReflex` type + action-kind constants | ✅ **PRESENT** | `store/agent_reflexes.go` is **byte-identical** to agridd |
| `store.ListAgentReflexesForAgent` / `BumpAgentReflexFired` | ✅ PRESENT | in `store/agent_reflexes.go` |
| `store.LastNAssistantMessages` / `LastNTokenUsage` / `ToolCallsForMessage` | ✅ PRESENT | StateCollector deps satisfied |
| migration `074_agent_reflexes.sql` | ✅ PRESENT | renumbered per plan |
| `store.MarkSessionHalted` (+ `session_halt.go`, migration `071_session_halt.sql`) | ✅ PRESENT | `store/session_halt.go` byte-identical; halt hook target exists |
| `SlotAssemblyResult` / `ctxpkg.SlotUserContext` / `rebuildLegacySystemPrompt` | ✅ PRESENT | chat_reflexes injection seam already exists in nanite service |
| **Plugin reflex filter constants** `FilterReflexState`, `FilterReflexAction` | ❌ **MISSING** | add to `internal/plugin/filter.go` (2 const lines) |
| **Plugin reflex emit methods** `EmitReflexFired`, `EmitReflexActionStaged` (and Created/Updated/Deleted/Validated) | ❌ **MISSING** | add to `internal/plugin/events.go` (6 thin methods) |
| `cfg.Plugins.ApplyFilter` (the `PluginHooks` interface) | ✅ PRESENT | `plugin/host.go:523` already has `ApplyFilter`; engine `PluginHooks` is **nil-safe/optional** |
| Container wiring (`reflexEngine := reflexes.NewEngine`, `Executor.Halt` hook, `SeedBaseReflexes`, `ReflexEngine` on chat config) | ❌ **MISSING** | ~20 lines in `service/container.go` (mirror agridd lines 856-880, 981) |
| chat service field `reflexEngine` + call site | ❌ **MISSING** | `service/chat.go` field + `chat_generate.go` call (`evaluateAndInjectReflexes` at the post-slot-assembly point, agridd `chat_generate.go:567`) |

**No agentdb, no monitor, no relay, no go-agent-runtime needed.** The engine's
closure is fully satisfiable with the 8 engine files + the small plugin
hook additions + container/chat wiring. Everything store-side is already in place.

### Recommended backport order for the engine

1. Add plugin reflex filter constants (`plugin/filter.go`) + emit methods
   (`plugin/events.go`). Smallest, no dependents-break risk.
2. Copy `internal/agent/reflexes/` (all 6 impl files + 4 tests) verbatim from
   agridd into nanite's `internal/agent/driftguard/` — imports already
   resolve (`internal/store`, `internal/plugin`). (Path updated 2026-08-16:
   this backport landed and was subsequently renamed from
   `internal/agent/reflexes` to `internal/agent/driftguard` on the nanite
   side, CW-20260816-0062, to resolve a collision with `internal/reflex`.)
3. Copy `internal/service/chat_reflexes.go`.
4. Wire in `service/container.go`: build engine, set `Executor.Halt` →
   `store.MarkSessionHalted` + event log, `SetPluginHooks(cfg.Plugins)`,
   `SeedBaseReflexes`, thread `ReflexEngine` onto the chat-service config.
5. Add `reflexEngine` field to chat service + the per-turn call site in
   `chat_generate.go`.
6. Run `go test ./internal/agent/driftguard ./internal/service ./internal/store`.

**Size: M.** Mechanical (substrate exists), but touches three hot files
(container.go, chat.go, chat_generate.go) so it needs careful placement +
focused tests.

---

## Missed — Should Backport

| File / pkg | What it does (1 line) | Deps (missing) | Size |
|---|---|---|---|
| `internal/agent/reflexes/*` (6 impl + 4 tests) | FU-30 runtime reflex engine: per-turn predicate eval + action dispatch | plugin reflex hooks (below); all store deps present | M |
| `internal/service/chat_reflexes.go` | Per-turn glue: evaluate reflexes, inject `<system-reminder>` into user-context slot | reflexes pkg | S |
| `internal/plugin/filter.go` constants `FilterReflexState`/`FilterReflexAction` | Plugin filter taps for reflex state/action | none | S |
| `internal/plugin/events.go` `EmitReflex*` methods (6) | Plugin event emission for reflex lifecycle/firing | none | S |
| `internal/service/container.go` reflex wiring | Build + seed + halt-hook + thread engine into chat svc | reflexes pkg | S |
| `internal/service/chat.go` `reflexEngine` field + `chat_generate.go` call site | Activates engine per turn | reflexes pkg | S |
| **agridd-only tests whose impl already lives in nanite** (backport for coverage) | `store/agent_reflexes_test.go`, `store/agent_runtime_test.go`, `service/agent_cycles_test.go` | impls present | S |

**Note on `mcp/args_normalize.go`** — see Unclear section; it is a small
defensive helper (`normalizeAndWarnArgs`, wired into agridd `mcp/manager.go`) that
canonicalizes/warns on tool-call arg shape. Low risk to bring, but not named in
the plan. Leaning "should backport (S)" but flagged for operator confirmation.

---

## Deliberately Excluded (matches plan intent / Known Intentional Limits)

These are exploration artifacts or surfaces the plan's "Known Intentional Limits
To Preserve" explicitly fences off. Do **not** backport without an explicit
operator decision to change scope.

| pkg / file | Why excluded | Plan citation |
|---|---|---|
| `internal/monitor/` (`circuit.go`) | Torque FU-21 monitor-loop circuit breaker; self-documented as *superseded by the reflex engine* ("once the reflex engine proves out… this package should be removed") | "No second runtime launch stack"; the engine is the intended replacement |
| `internal/probes/` (`probes.go`, `specs.go`) | FU-20 pre-tick SQL probe runner hardcoded to a Torque DB path; spike artifact | "not every exploration artifact" |
| `internal/composer/` (`composer.go`, `source_mail.go`) | FU-27 per-tick procedure-body composer for the monitor-loop driver; stubs probes/mail/focus | tied to excluded monitor loop |
| `internal/relay/` (`service.go`, `types.go`) | Agent-routing conveniences over messaging substrate (relay-mode agents) | Ready-notice/mailbox-target limits; "Ready notices stay preview-only until mailbox target resolution is explicit" |
| `internal/agentdb/` (8 files) | Per-agent SQLite spike (narrative_write, agent_db_read/exec, cross-agent grants) | exploration artifact; not in "What Comes Over" |
| `internal/agent/skills/` (`loader.go`) | FU-33 file-SOT skills curation loader (skill_get/skills_view_more/skill_search self-tools) | Not in plan scope; nanite has its own `internal/skill/` + `skillbroker` |
| `internal/agent/urn.go` | `GenerateAgentURN` mint for agent-mux messaging; **zero non-test importers in agridd** | dead/spike code |
| `internal/runtime/agent/{runtime_kind,runtime_binding,turn,checkpoint,boot_context}.go` | Second runtime launch stack built on `go-agent-runtime` `runtimebind`/`sessionkit`/`turn`/`checkpoint`; nanite has its own bootdir-based launch + subagent checkpoint/resume | **"No second runtime launch stack"** (explicit). nanite `agent.go`/`kickoff.go` do NOT use runtimebind/sessionkit |
| `internal/api/session_halt.go` (+ `_test.go`) | HTTP `/api/sessions/{id}/halt` + health-probe endpoints for the monitor-loop driver | driver excluded; store-side `MarkSessionHalted` retained for the reflex halt path |
| `internal/api/halt_escalator.go` | FU-21 production HaltEscalator → agent-mux daemon `/messages` | "Ready notices stay preview-only"; daemon escalation out of scope |
| `internal/api/relay.go` (`/api/relay/*` routes) | HTTP surface for `internal/relay` | relay excluded |
| `internal/api/compose.go` (`/api/sessions/{id}/compose-boot`) | HTTP surface for `internal/composer` | composer excluded |
| `internal/api/role_seed_priority.go` | FU-14 hardcoded role-seed activation values (cache-stability) | not in plan; nanite may handle cache stability differently |
| `internal/mcp/self_tools_agent_db*.go`, `self_tools_narrative.go` | Self-tool handlers for the agentdb spike | agentdb excluded |
| `internal/api/agents_fu28.go` (impl absent → `agents_fu28_test.go` agridd-only) | FU-28 URN-alias agent surface | tied to `agent/urn` spike |
| `internal/bootprofile/tesseract_recall.go` (impl absent → test agridd-only) | Tesseract recall in boot profiles | spike; not in plan |

### Service-layer "reaper" files — excluded as Torque-spike, see Unclear

`internal/service/compaction_reaper.go` (FU-55), `proactive_compact.go` (FU-15),
`tool_unknown_preflight.go` (FU-19), `agent_known_tools_reaper.go` are
Torque-Supervisor-hardening spikes (CW-20260520-* series). None are in the plan's
"What Comes Over." They are wired in agridd's container but address Torque-specific
runaway/cache pathologies. Default verdict: **excluded**, but they harden the
durable-agent runtime generally — flagged under Unclear for an explicit call.

---

## Already Present (in nanite under same/different path)

| agridd item | Where in nanite |
|---|---|
| reflex **store substrate** (`store/agent_reflexes.go`) | identical file present |
| `store/session_halt.go` + `MarkSessionHalted` | identical file present |
| `store/agent_runtime.go` `RuntimeKind` (migration `079`) | present (`agent_runtime.go:28`) |
| durable-agent stores/services/API | `store/durable_agents.go`, `service/durable_agents.go`, `api/durable_agents.go` (nanite versions LARGER) |
| `/api/harness/v1` control plane | `api/api.go:189-198+` fully registered |
| wake / recipes / agent-builder / capabilities / boot-plans / frontend-readiness | all present in `internal/api/` |
| known-tools slot assembly (agridd `context_known_tools_test` target) | nanite `service/context.go` + extensive `context_*_test.go` suite |
| `service/messaging_sink.go` | present (differs, nanite-side evolved) |
| `service/agent_cycles.go` (agridd-only test → impl) | present |

The agridd-only **test** files `store/agent_reflexes_test.go`,
`store/agent_runtime_test.go`, `service/agent_cycles_test.go` test code that
already exists in nanite — backport them as added coverage (S), not as new features.

---

## Unclear — Needs Operator Decision

1. **Reflex engine scope vs nanite's `internal/reflex` M3 catalog.** Are these
   meant to coexist (engine = runtime durable-agent reflexes; M3 = input→role
   dispatch), or did nanite intend `internal/reflex` to *replace* the engine?
   The plan lists "per-agent capability CRUD… reflexes" under What Comes Over,
   which implies the engine *is* in scope — but nanite shipped a same-named,
   different-purpose package. **Recommend: backport the engine as
   `internal/agent/reflexes/` (distinct path), leave `internal/reflex/` alone.**

2. **`internal/mcp/args_normalize.go`** — defensive tool-arg canonicalization,
   wired into agridd `mcp/manager.go`. Not in plan. Useful hardening, low risk.
   Bring it or not?

3. **Torque-hardening reapers** (`compaction_reaper`, `proactive_compact`,
   `tool_unknown_preflight`, `agent_known_tools_reaper`). Generally useful
   runtime guards, but born as Torque-Supervisor spikes and absent from the plan.
   Backport as general hardening, or leave excluded?

---

## Frontend Assessment

**Frontend looks complete. No deeper diff required for capability gaps.**

- File parity: agridd `ui/src` = 313 ts/tsx files; nanite = **314** (nanite ahead).
- Only **one** agridd-only `ui/src` file: `__tests__/phase-15-composer-fork-boundary.test.tsx`
  — a test exercising `ComposerToolbar` (which exists in both). nanite has no
  dedicated "composer fork boundary" feature code, but the component under test is
  present. **Low value; optional to port the test.**
- Nanite-only files (`MetaHarnessManager.tsx`, `use-chat-session-switch.test.tsx`)
  confirm nanite moved forward.
- Spot-checked shared durable-agent FE files — nanite versions are **strictly
  larger** (`DurableAgentAdminPanel.tsx` 1423 vs 1367; `lib/types.ts` 2773 vs 2726;
  `lib/api.ts` 3745 vs 3639), consistent with the operator's "we fixed frontend
  and backend stuff." Divergence reflects nanite *additions*, not missing capability.

**Verdict: frontend is complete; the durable-agent admin/start/recipe surfaces all
landed and then evolved further in nanite.**

---

## Migration Reconciliation (by topic)

**Complete. Zero topic gaps.** Every agridd durable-agent migration has a
renumbered nanite equivalent exactly per the plan's offset table:

| Agridd | Nanite | Topic | Status |
|---|---|---|---|
| 065_per_agent_state | 068_per_agent_state | per-agent state | ✅ |
| 066_agent_profiles_role_tools | 069_agent_profiles_role_tools | role tools | ✅ |
| 067_agent_schedules | 070_agent_schedules | schedules | ✅ |
| 068_session_halt | 071_session_halt | session halt | ✅ |
| 069_agent_profiles_durable | 072_agent_profiles_durable | durable profiles | ✅ |
| 070_agent_profiles_multi_agent | 073_agent_profiles_multi_agent | multi-agent | ✅ |
| 072_agent_reflexes | 074_agent_reflexes | reflex rows (engine substrate) | ✅ |
| 074_agent_profiles_role_skills | 075_agent_profiles_role_skills | role skills | ✅ |
| 075_agent_mailbox_view | 076_agent_mailbox_view | mailbox view | ✅ |
| 076_tool_broker_session_cache_and_known_order | 077_… | tool broker cache | ✅ |
| 077_agent_context_policy_and_cycles | 078_… | context policy/cycles | ✅ |
| 078_agent_runtime_kind | 079_agent_runtime_kind | runtime kind | ✅ |
| 079_durable_agent_instances | 080_… | instances | ✅ |
| 080_durable_agent_launch_policy | 081_… | launch policy | ✅ |
| 081_durable_agent_stopped_status | 082_… | stopped status | ✅ |
| 082_durable_agent_events | 083_… | events | ✅ |
| 083_seed_legacy_durable_agent_instances | 084_… | seed legacy | ✅ |
| 084_agent_boot_plans | 085_agent_boot_plans | boot plans | ✅ |

(Agridd skipped numbers 071/073 in its own sequence; not a gap.) Nanite's
067 slot is its own `067_subagent_run_retry_lifecycle.sql` (nanite-only work),
which is why the offset begins at +3.

---

## Recommended Dependency-Ordered Backport Sequence

The only product gap is the reflex engine; everything else excluded matches plan
intent. Suggested order:

1. **(operator decision)** Confirm reflex-engine scope vs `internal/reflex`
   (Unclear #1). Assuming yes:
2. **Plugin hooks** — add `FilterReflexState`/`FilterReflexAction` to
   `plugin/filter.go`; add `EmitReflex*` to `plugin/events.go`. *(S, no risk)*
3. **Reflex engine package** — copy `internal/agent/reflexes/` (impl + tests)
   into nanite's `internal/agent/driftguard/` (path updated 2026-08-16 per
   CW-20260816-0062 — see the note in the "Recommended backport order for
   the engine" section above). Imports resolve against existing nanite
   `store`/`plugin`. *(M)*
4. **Per-turn glue** — copy `internal/service/chat_reflexes.go`. *(S)*
5. **Container + chat wiring** — engine construction, `Executor.Halt` →
   `MarkSessionHalted`+event, `SetPluginHooks`, `SeedBaseReflexes`, thread
   `ReflexEngine` onto chat config; add `reflexEngine` field + call site in
   `chat_generate.go`. *(S, but hot files — test carefully)*
6. **Coverage tests** — backport `store/agent_reflexes_test.go`,
   `store/agent_runtime_test.go`, `service/agent_cycles_test.go`. *(S)*
7. **(operator decision)** Optionally bring `mcp/args_normalize.go` (Unclear #2)
   and/or the Torque-hardening reapers (Unclear #3).
8. **Verify:** `go test -count=1 ./internal/agent/driftguard ./internal/service
   ./internal/store ./internal/plugin`.

**Do NOT backport:** `monitor`, `probes`, `composer`, `relay`, `agentdb`,
`agent/skills`, `agent/urn`, the `runtime/agent` go-agent-runtime bind stack, and
the `api/{session_halt,compose,relay,halt_escalator,role_seed_priority}` surfaces —
all match the plan's exclusions / Known Intentional Limits.
