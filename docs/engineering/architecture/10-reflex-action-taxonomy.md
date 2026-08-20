# Reflex Action Taxonomy & Precedence

Resolves [Steering](03-steering.md)'s open "Watch item": reflexes absorbed six distinct jobs that used to be five separate systems, and the flat `action_kind` schema (one CHECK-constrained enum, one `priority INTEGER` doing double duty) doesn't scale to six cleanly. This doc is the design produced by a dedicated architecture-review session (`TASKS/phase-4/10-reflex-architecture-review.md`, 2026-08-19) — **design only, no code or schema changed in that session.** Implementation is deferred to follow-up work; this doc exists so the agreed shape isn't lost in the meantime.

## The concrete problems that motivated this

All confirmed against the live code, not hypothetical:

- **`priority` does two different, undeclared things.** In the generic per-turn pass (`internal/agent/reflexes/engine.go:EvaluateState`), it only orders *evaluation*, not *outcome* — every fired reflex still applies. In `dispatch_to_agent`'s own path, it drives strict first-match-wins selection. Same column, two semantics, chosen implicitly by which code path touches it.
- **Priority values are already inconsistent within one action kind.** The three seeded `halt_session` reflexes sit at priority 100, 90, and 10 (`internal/agent/reflexes/seeds.go`) — priority-as-a-number doesn't reliably encode "this is a safety action" today.
- **Contradictory directives can co-fire with no resolution.** Two `force_tool_choice` reflexes naming different tools (or one hard, one soft) can both fire in the same pass and both land in the same concatenated `<system-reminder>` block (`internal/service/chat_reflexes.go:formatReflexReminder`) — the LLM sees two competing imperatives.
- **`halt_session` doesn't preempt anything in the same pass, and doesn't stop the current turn.** `Engine.EvaluateState`'s loop keeps applying lower-priority reflexes after a halt fires. The halt itself (`container.go`'s `HaltHook` → `store.MarkSessionHalted`) only stamps `halted_at`, surfaced later by `frontend_readiness.go` on a *subsequent* request — the turn that triggered the halt completes and reaches the LLM anyway.
- **Three call sites, two of them duplicated by necessity, not oversight.** `internal/service/chat_reflex_dispatch.go:attemptReflexDispatch` and `internal/mcp/self_tools_dispatch.go:matchDispatchToAgentReflex` both hand-implement the same list-candidates → evaluate-in-priority-order → first-fire-wins loop for `dispatch_to_agent`, because `internal/mcp` cannot import `internal/service` (which already imports `internal/mcp`) — documented in both files' own comments as a deliberate, not accidental, duplication.

## The architectural spine: emit → react

Reflexes, and the adjacent mechanism described near the end of this doc, are both instances of one shape: something **emits** a signal, and the harness **reacts** deterministically. This isn't a new idea introduced by this doc — it's already latent in the code (`PluginHooks.EmitReflexFired` / `EmitReflexActionStaged`, `internal/agent/reflexes/engine.go`) under a name that only covers one instantiation. Naming it explicitly gives three otherwise-unrelated-looking mechanisms one shared vocabulary:

1. **State-driven emit** — a reflex's `predicate`/`event`/`interval` trigger evaluates true; the harness's own state-evaluation loop is the "emitter." This is the reflex system proper, and the rest of this doc.
2. **Agent-driven emit** — an agent deliberately calls a small, purpose-built self-tool to declare a fact (e.g. "this task updated"); the harness reacts however it decides to (render a card, hit an internal API, both, neither). See "Harness-reactive self-tools" below — this is a *sibling* mechanism to reflexes, not a reflex action kind, because there's no predicate to evaluate: the agent's own tool call *is* the trigger.
3. **Plugin observability emit** — the existing `EmitReflexFired`/`EmitReflexActionStaged` plugin hooks, which react to (1) firing.

## Facet 1 — Category (`system_message` vs `execute_action`)

A declared, extensible label on each action kind — not a replacement for the specific kinds (see "Why not collapse to two kinds" below).

- **`system_message`** — advisory. Modifies what the LLM sees; the LLM retains discretion over what it does next.
- **`execute_action`** — deterministic. The harness performs the effect directly; no LLM turn or discretion is involved.

Modeled as a lookup table, not a fixed two-value enum, so a third category can be added later without a schema rewrite if a genuinely new shape shows up (this was an explicit ask: flexibility for the unknown, not just today's two).

**Why not collapse the whole taxonomy to these two kinds:** the combining algorithm, the provenance ceiling, and the recurrence default (below) all need to hang off something at real FK granularity. A single `execute_action` row with an internal `kind` param distinguishing halt/schedule/send_message/dispatch would make "plugin tier may not declare *this specific one*" or "*this specific one* uses deny-overrides" inexpressible at the schema layer — the same governance-illegibility problem already ruled out for a generic `callback` kind (see below). Category is a label spanning the specific kinds; it doesn't replace them.

## Facet 2 — Combining algorithm (per action kind)

The closest formal precedent is XACML's policy-combining algorithms (`deny-overrides` / `permit-overrides` / `first-applicable` / `only-one-applicable`) — AWS IAM's own evaluation ("explicit deny always wins, else any allow wins, else default deny") is one instance of `deny-overrides`. Three algorithms cover the current six kinds:

| Algorithm | Meaning |
|---|---|
| `deny_overrides` | If any reflex of this kind fires in a pass, it wins outright and **short-circuits the rest of the pass** — no other action, of any kind, gets applied this tick. |
| `first_applicable` | Among reflexes of this kind that fire, only the highest-priority one is selected (priority as pure tie-break, ties broken by `created_at ASC` as today). |
| `all_applicable` | Every reflex of this kind that fires this pass gets applied — current default behavior, correct for kinds that are additive by nature. |

### The six current kinds, re-classified

| Kind | Category | Combining algorithm | Change from today |
|---|---|---|---|
| `inject_reminder` | `system_message` | `all_applicable` | None — matches current behavior. |
| `force_tool_choice` | `system_message` | `first_applicable` | **Fix.** Today this is effectively `all_applicable` (every fired one lands in the same reminder block) — the source of the contradictory-directive bug above. |
| `send_message` | `execute_action` | `all_applicable` | None. |
| `add_schedule` | `execute_action` | `all_applicable` | None. |
| `halt_session` | `execute_action` | `deny_overrides` | **Fix, two parts.** (a) Same-pass preemption: nothing else this pass applies once a halt fires. (b) Turn-level: the halt must actually stop the *current* turn synchronously, not just stamp `halted_at` for a future readiness check. See "Halt must actually halt" below. |
| `dispatch_to_agent` | `execute_action` | `first_applicable` | None — formalizes what `attemptReflexDispatch`/`matchDispatchToAgentReflex` already do informally today. |

`priority` becomes purely the tie-break *within* `first_applicable`; it has no effect under `deny_overrides` (only firing/not-firing matters) or `all_applicable` (order of application, not selection).

## Facet 3 — Provenance / authority tier

A real FK-backed lookup, not a free-text `created_by` string — used first for a security purpose: which tiers may even *declare* a given action kind. Live tiers on `agent_reflexes`: **`system`** (seeded; may be marked required/opt-out-immune, as three `halt_session` seeds already are), **`operator`** (authored through the CRUD API), **`plugin`** (plugin-contributed). A per-kind allow-list (which tiers may declare which kind) gates the two highest-blast-radius kinds — `halt_session` and `dispatch_to_agent` are the obvious candidates to restrict away from `plugin`-tier, but the specific allow-list values are not locked by this session; the mechanism is.

**`agent_proposed` is not a fourth live tier.** It only exists as a `pending_reflexes` status. Checked against `store.ApprovePendingReflex` (`internal/store/agent_reflexes.go:555`): approval already stamps the resulting live row's `created_by` as `"operator:" + reviewedBy`, discarding origin. So "active in `agent_reflexes` ⇒ operator-approved" is already true today by construction — formalizing provenance as a real FK just needs to preserve that same collapse-on-approval behavior, not invent new behavior. No separate "approved-by" field is needed beyond what `pending_reflexes.reviewed_by` already carries.

## Facet 4 — Recurrence (fire-once vs fire-always vs cooldown)

Cascading, least-to-most-specific — **system default → per-action-kind override → per-reflex override.**

This isn't only a data-modeling nicety — it removes the *behavioral* justification for two of the three call-site duplications described below. Today, `dispatch_to_agent`'s "no cooldown, re-evaluate every turn" is a hardcoded bypass of the generic pass's hardcoded 15-minute debounce, implemented as an entirely separate function. Under this hierarchy it becomes: system default = 15-minute cooldown (naming what's already the de facto default), kind-level override for `dispatch_to_agent` = none. Once that's data instead of code, the *only* remaining reason for `dispatch_to_agent` to have its own invocation points is the import-cycle constraint below — not a recurrence-semantics difference.

**Open, not decided by this session:** whether recurrence needs a third mode beyond "cooldown duration" and "none" — e.g. "fire at most once ever, for the life of the session" for lifecycle kinds like `task_complete_self_terminate`/`task_timeout`. Worth deciding before implementation, not assumed here.

## One shared decision engine, multiple legitimate invocation points

Three call sites evaluate reflexes today:

1. `internal/service/chat_reflexes.go` → `Engine.EvaluateState` (generic pass, all non-`dispatch_to_agent` kinds).
2. `internal/service/chat_reflex_dispatch.go:attemptReflexDispatch` (`dispatch_to_agent`, from the main chat turn loop).
3. `internal/mcp/self_tools_dispatch.go:matchDispatchToAgentReflex` (`dispatch_to_agent`, from the `task_execute` self-tool path).

(2) and (3) are near-verbatim duplicates of the same selection loop, and it's structural, not accidental: `internal/mcp` cannot import `internal/service`, so (3) hand-copies (2)'s logic instead of calling it. Both already import `internal/agent/reflexes` directly (that's where `EvaluateTrigger`/`State` come from in both) — so the shared decision primitive belongs **inside `internal/agent/reflexes`**, the one package both callers can depend on without a cycle. Target shape: something like `reflexes.Resolve(candidates, state) → selected actions`, encoding the per-kind combining algorithm from Facet 2, called by all three sites instead of each reimplementing "list + evaluate + pick."

**What does *not* unify:** how `State` gets built. `attemptReflexDispatch` and `matchDispatchToAgentReflex` both populate `ScopeTier`/`ExecutionPattern` and a synthetic single-entry `UserMessages` window from live, not-yet-persisted, in-flight classification data — `StateCollector` (used by the generic pass) has no way to see a not-yet-committed turn. That's a legitimate reason for distinct invocation points to keep existing; it is not the same problem as the duplicated *decision* logic, and should not be collapsed away.

## Halt must actually halt

Two distinct fixes, both confirmed in scope (operator sign-off: "deny-overrides actually stop the turn"):

1. **Same-pass preemption** (the `deny_overrides` combining algorithm itself, Facet 2) — the shared decision engine must resolve `deny_overrides` kinds before any others, and short-circuit the rest of the pass if one fires. No `inject_reminder`/`force_tool_choice`/etc. gets applied in a pass where a halt also fired.
2. **Turn-level synchronicity** — today's `HaltHook` (`container.go:918`) only calls `store.MarkSessionHalted`, checked later by `frontend_readiness.go` on a *subsequent* request. The turn that triggered the halt still completes and reaches the LLM. The caller (`internal/service/chat_generate.go`, immediately after the `evaluateAndInjectReflexes` call around line 523) needs to check for a halt result from this turn's evaluation and abort before the LLM call, not just log it.

Neither is implemented in this session — captured here as the design; carrying it out is a follow-up.

## Telemetry — current gaps, and what "strong" needs to mean here

Checked while drafting this doc — the operator asked for a deliberate trace/debugging pass on the reflex system specifically, as part of a broader push to harden telemetry across systems. Three concrete gaps found, all stemming from the same root cause as the call-site duplication above: three code paths independently implementing "a reflex fired," not one.

1. **Split sinks for the same conceptual event, by accident of which call site handled it.** `dispatch_to_agent` firings from `attemptReflexDispatch` (the chat-turn-loop path) write to `event_log` (event_type `dispatch_to_agent`, category `reflex`). The *same kind of event* from `matchDispatchToAgentReflex` (the `task_execute` self-tool path) instead writes to `playbook_match_log` via `LogReflexMatch` — a different table, a different schema, no shared shape. Tracing "did any dispatch reflex fire in this session" means checking two unrelated tables depending on entry path, with no way to tell from either alone whether the other path also fired something.
2. **`fired_count`/`last_fired_at` isn't updated by all three paths.** The generic pass (`Engine.EvaluateState`) and `attemptReflexDispatch` both bump it; `matchDispatchToAgentReflex` never does (confirmed — no `BumpAgentReflexFired` call anywhere in `self_tools_dispatch.go`). A `dispatch_to_agent` reflex that fires mainly through the self-tool path can show `fired_count: 0` / `last_fired_at: null` in the operator UI's reflex list while it's actively routing turns — the summary telemetry surface lies about what's actually firing.
3. **Plugin observability hooks only see one of three paths.** `PluginHooks.EmitReflexFired` / `EmitReflexActionStaged` are invoked exclusively from inside `Engine.EvaluateState`'s loop. Both `dispatch_to_agent` call sites bypass `EvaluateState` entirely by design (to skip its debounce) and call `Executor.Apply` directly — so any plugin instrumented against those hooks is structurally blind to every `dispatch_to_agent` firing, not just occasionally missing some.

**Requirement:** every reflex firing, regardless of which of the (target: one) decision-engine entry points produced it, emits one consistent trace record — same shape, same sink, same plugin-hook coverage. This is not separate work from "one shared decision engine" above — it's the same consolidation. Once `reflexes.Resolve(...)` is the single place a firing gets decided, telemetry emission belongs there too, so the three gaps above close as a side effect of that fix rather than needing three separate patches later.

**What the trace record should carry**, beyond what any single path captures today: reflex_id/name/action_kind (existing everywhere already), the resolved **category** and **combining algorithm** applied (new facets from this doc), and **why this one won** when a combining algorithm suppressed competitors. `attemptReflexDispatch` already does the last part well for `dispatch_to_agent` today — its `alternatives_considered` list logs every candidate evaluated and whether it fired, not just the winner — that pattern should become the standard for every kind under `first_applicable`/`deny_overrides`, not stay dispatch_to_agent-specific. Also new, with no current telemetry representation at all because the concepts don't exist yet: **provenance tier** (Facet 3) and **recurrence outcome** (did this reflex's trigger evaluate true but get suppressed by cooldown, and at which level of the cascade — system/kind/reflex — was that cooldown set).

**Not decided here:** whether the unified sink is `event_log` (already used by two of three paths) or a new dedicated reflex-trace table (closer to what `playbook_match_log` already is, just widened beyond `dispatch_to_agent`). That's an implementation call, not an architecture one — captured as a requirement, not a schema.

## Harness-reactive self-tools (adjacent mechanism, deliberately not part of this taxonomy)

Distinguished from reflexes during this session: an agent-initiated "declare a fact, let the harness react" pattern (e.g. `nanite_report_task_updated(id, msg)`, harness decides whether/how to react — a card, an internal API call, both, neither). This has no predicate/event/interval trigger — the agent's own tool call *is* the trigger — so it structurally isn't a reflex action kind and doesn't belong in the `action_kind` table above.

Resolved shape: implement as small, purpose-built `nanite_*` self-tools (the namespace already reserved for first-party tools, already outside the general MCP trust-tier/result-cache apparatus meant for arbitrary external reach). Each self-tool's own definition carries a category/kind field marking it harness-reactive — preferred over a bare boolean flag for the same extensibility reason as Facet 1's category table (room to add more reaction shapes later without a schema change). Naming for this mechanism is not finalized — leading candidate is **"harness-reactive self-tools"**; "report tools" and "declare tools" were also considered and explicitly rejected as final only in that neither is locked yet. Whatever name is chosen, avoid "event" and "notify" — both already carry distinct meaning elsewhere in this vocabulary (`trigger_kind='event'`, `event_log` table; `notify_external`, a candidate deterministic reflex kind below) and reusing them would reintroduce the exact naming confusion this session was trying to eliminate.

This mechanism gets its own design pass when it's actually built — this doc captures the distinction and the naming constraint so the context isn't lost, not a full spec for it.

**Update (2026-08-20):** that design pass happened — see `docs/engineering/architecture/11-harness-reactive-self-tools.md`. Resolved: `internal/selftools` (not `internal/mcp`, not `internal/agent/selftools`), with the reaction engine at `internal/selftools/reactions` (not `action` — that word is already claimed by this doc's own `action_kind`/`AppliedAction`/`Executor.Apply` vocabulary). Implementation remains deferred pending a concrete consumer.

## Deferred extension points (documented, not built)

- **Plugin-registered action kinds.** Two things would be needed together, not one: a registration surface (direct precedent exists — `plugin.yaml`'s `registers.envelopes` + `scripts/generate-plugin-imports.mjs` for envelope types) *and* an Executor hook so `Apply`'s effect dispatch can reach plugin code for kinds it doesn't natively know (today `Apply` is a closed Go `switch`). No concrete plugin need exists today — the one real plugin-shaped precedent in this codebase, the Loom Curator/Weaver pilot reflexes (`internal/agent/reflexes/loom_pilot_seeds.go`), only uses the core `inject_reminder` kind. Documented as a deliberate seam, not built.
- **Candidate future kinds**, to add if/when a real need appears (not built now): `call_self_tool` (deterministic, *reflex*-triggered self-tool invocation — the harness calls a tool automatically when a predicate fires, contrast with the agent-initiated harness-reactive self-tools above), `write_knowledge`, `notify_external`/`route_external`.
- **`callback` is not itself a kind.** Considered and rejected as a generic 7th action kind — an opaque callback target is illegible to the combining-algorithm and provenance-ceiling facets, the same problem ruled out for collapsing to two kinds. If plugin-registered kinds (above) are ever built, each one still gets its own row/identity/policy in the real tables; `callback` is at most the underlying execution mechanism for such a row, never a bare escape hatch of its own.

## What this session did not decide

Listed explicitly so a future reader doesn't mistake omission for a locked decision:

- Exact table/column names and DDL — this doc is architecture-level agreement, not migration-ready schema. (Illustrative shape: `reflex_action_categories`, `reflex_action_kinds` with `category_id`/`combining_algorithm`/`default_recurrence` columns replacing the current CHECK-enum `action_kind` TEXT column, `reflex_provenance_tiers` with a per-kind allow-list join, `agent_reflexes.provenance_tier_id` and `.recurrence_override` FKs/columns.)
- The specific per-tier action-kind allow-list values (which tiers may declare `halt_session`/`dispatch_to_agent`, etc.) — the ceiling mechanism is decided, the values are not.
- Whether recurrence needs a third "fire once ever" mode beyond duration/none.
- ~~Final naming for the harness-reactive self-tool mechanism and its category value.~~ Resolved 2026-08-20 — see `docs/engineering/architecture/11-harness-reactive-self-tools.md`.
- Whether/when to build the plugin-registered-action-kind seam.
- The unified telemetry sink (`event_log` vs. a new dedicated reflex-trace table) — the requirement (one consistent trace record, one sink, full plugin-hook coverage) is decided; the storage shape is not.

## Status

Design complete and operator-signed-off as of 2026-08-19. No code or schema changed. Implementation — the shared decision engine, the new tables, the halt-synchronicity fix, the unified telemetry emission, and the harness-reactive self-tool mechanism — is deferred to follow-up task(s), not yet filed.

Operator's explicit call on the open items above (allow-list values, once-ever recurrence mode, self-tool naming, telemetry sink shape, plugin-kind seam timing): acceptable to leave open for now and let a couple of weeks of real usage inform the answer, rather than force a decision ahead of that signal.
