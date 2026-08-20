# Harness-Reactive Self-Tools

Resolves `docs/engineering/architecture/10-reflex-action-taxonomy.md`'s stub — "Harness-reactive self-tools (adjacent mechanism, deliberately not part of this taxonomy)" — which deliberately punted the real design: *"This mechanism gets its own design pass when it's actually built — this doc captures the distinction and the naming constraint so the context isn't lost, not a full spec for it."* This doc is that design pass, produced by a dedicated operator design session (`TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md`, 2026-08-20) — **design only, no code or schema changed in this session**, matching how `10-reflex-action-taxonomy.md` itself was produced. Implementation is deliberately deferred (see "Status" below).

## The mechanism, recapped

An agent-initiated "declare a fact, let the harness react" pattern: a self-tool call *is* the trigger — there's no predicate/event/interval to evaluate, so this structurally can't be a reflex `action_kind` row (reflexes' emit side is a harness-side state-evaluation loop watching for a trigger; here, the trigger already happened by the time the tool call lands). The harness decides whether/how to react — render a card, call an internal API, call an external API, invoke a callback, some combination, or nothing — based on how that specific tool is configured, not on what the model asked for.

## Where it lives: `internal/selftools`

Self-tool definitions and dispatch move out of `internal/mcp` (where they live today, as a large static Go slice + a hardcoded dispatch `switch` in `SelfToolsTransport.CallTool`) into a new top-level package, `internal/selftools`. Considered and rejected:

- **`internal/mcp`** (status quo) — self-tools aren't an MCP concept; they only ride the MCP transport. Leaving them there conflates "what protocol carries this" with "what this is."
- **`internal/agent/selftools`** — rejected on a direct `GLOSSARY.md` check. `internal/agent/` already has two distinct, established meanings in this codebase (`internal/agent/` proper: Agent *composition* — role/scope/grants/permissions, plus `reflexes/` for steering; `internal/runtime/agent/`: the boot/process *runtime* — bootdirs, sandboxing, workspace). Self-tools are neither — they're a harness-wide calling surface every runtime reaches identically regardless of which Agent composition is active. Nesting under `agent/` would make "agent" mean a third thing depending on subdirectory, exactly the collision class `GLOSSARY.md` exists to prevent. The distinction the name was reaching for ("belongs to the agent calling it, not the system") is already carried by **self** — "self-tools" already means "tools the harness gives an agent to act on itself/the harness," as opposed to an MCP server's externally-reached tools.

Go's `internal/` visibility rule doesn't reward nesting depth here either — the enforced boundary is set by the first `internal/` segment from repo root; `internal/selftools` and a nested variant are equally visible module-wide. The choice is pure semantic organization, and this repo's own top-level `internal/` is overwhelmingly flat siblings (`mcp`, `plugin`, `messaging`, `envelope`, `chat`, …) for exactly that reason.

**The reaction engine** lives at `internal/selftools/reactions` — a sub-package, not `internal/selftools/react` or `internal/selftools/action`:

- Not `react`/`react`-rooted names: a bare verb is a minor idiom wart against this repo's own sibling convention (`reflexes`, `messaging`, `envelope` — nouns/gerunds, not verbs), and would read as call-site stutter (`react.Fire(...)`).
- Not `action`: **`action` is already claimed, precisely and heavily**, one level over — `internal/agent/reflexes` uses `action_kind`, `execute_action` (one of Facet 1's two categories), `AppliedAction`/`CandidateOutcome.ActionKind`, `Executor.Apply`. Reusing it here risks the exact same collision class just ruled out for `agent`. "Reaction" also fits the mechanism more precisely than "action" — the doc's own framing ("a card, an internal API call, both, neither") describes a contextual, possibly-multiple, possibly-absent *response*, not reflexes' singular deterministic *effect* selected by a combining algorithm.

Checked against `GLOSSARY.md`: no existing "react"/"reaction" entry to collide with. Per the parent doc's naming constraint, also confirmed neither "event" nor "notify" appear anywhere in this design.

## Definition & reaction shape

**Scope decision:** DB-backed, but only the *reactive layer* — not a migration of the whole self-tool catalog. The ~70 existing tool definitions (name/description/input schema) and their dispatch stay exactly where the `internal/selftools` move puts them: static Go, matching how `agent_reflexes`/`agent_context_resolvers` already layer DB-backed config on top of otherwise-Go-executed behavior without migrating the thing being configured into the DB. Only self-tools that are actually harness-reactive get a row here — the rest of the catalog is untouched.

**Category/kind is a lookup table, not a hardcoded enum** — same extensibility reasoning as the taxonomy doc's Facet 1 (room to add reaction shapes later without a schema rewrite). Illustrative shape (architecture-level agreement, not migration-ready DDL, matching this doc's own parent's precision level):

- **`selftool_reaction_kinds`** (lookup): `id`, `slug` (`render_card`, `internal_api_call`, `external_api_call`, `callback`), `category` (`render` vs `execute` — mirrors Facet 1's `system_message`/`execute_action` split; anticipates a future provenance-ceiling question — "which tier may register an `external_api_call` reaction" — the same shape as "which tier may declare `halt_session`"), `implemented` (bool — lets `external_api_call`/`callback` exist as real, queryable, documented rows before they're executable, without a later migration when they are built), `description`.
- **`selftool_reactions`** (config, many rows per tool): `id`, `tool_name` (plain string matching the Go-defined tool's `Name` — no FK to a tool-catalog table, since definitions stay in Go), `reaction_kind_id` FK, `config` (JSON, kind-specific: envelope type/template for `render_card`; endpoint + body template for `internal_api_call`/`external_api_call`; target identifier for `callback`), `enabled`, `created_at`. A `provenance`/`created_by` column is a documented, not-built extension point — relevant once a plugin can register its own self-tool + reaction; not needed while only system/operator register these, same posture the taxonomy doc took on plugin-registered action kinds generally.

**No combining-algorithm column, deliberately.** Reflexes need one because multiple *reflexes of the same kind* can independently fire on the same state and something has to pick a winner. Self-tool reactions don't have that problem: one tool call looks up *its own* configured reactions, and firing multiple different kinds together is the normal case (the doc's own "a card, an internal API call, both" framing), not a conflict to resolve. Omitting Facet 2's machinery here isn't a gap relative to reflexes — it's a different problem shape.

**Why `internal_api_call` and `external_api_call` stay separate kinds** rather than collapsing into one generic `http_call` kind with a trust flag: internal targets are implicitly trusted (parallel to the MCP `builtin` trust tier); external targets need the allowlist/auth gating `external_api_call` will eventually require (parallel to `third_party_http`). Keeping them distinct kinds keeps that gate legible at the schema layer without a later rewrite — the same reasoning the taxonomy doc used to explicitly reject a generic `callback` action kind ("an opaque callback target is illegible to the… provenance-ceiling facet… every one still gets its own row/identity/policy").

## The import-cycle constraint, and what it means for each reaction kind

Confirmed directly against the current import graph, not assumed: `internal/mcp` (and, by the same boundary, `internal/selftools` after the move) **cannot** import `internal/chat` or `internal/service` — both already import `internal/mcp`, so either direction is a cycle. `internal/agent/reflexes/executor.go` hits the identical wall today and resolves it by only ever importing `internal/store` — no exception, even for `halt_session`. That's not incidental: it's *why* `halt_session` historically only stamped a DB flag instead of pushing a live signal (see "Halt precedent" below).

This splits the four reaction kinds into two structurally different execution shapes:

- **`internal_api_call`, `external_api_call`, `callback`, and reaction telemetry** — none of these need same-turn live UI delivery. They execute fully synchronously inside `internal/selftools/reactions` (an HTTP client call for API/webhook/callback kinds, an `internal/store` write for telemetry) with no cycle problem at all.
- **`render_card`** — this one genuinely needs to reach the SSE/streaming layer, which sits above `internal/selftools`. There is no way to execute it directly from inside the engine. The resolver's job for this kind is to build the envelope payload and hand it back to the calling self-tool handler, which embeds it in the tool's own `ToolResult` text as a `<!--ENVELOPE_DATA:...:ENVELOPE_DATA-->` marker — the same signal-out mechanism `card_show`/`todo_list` already use today, for the same structural reason (there is no other way out of `internal/selftools` for a live UI effect).

### Halt precedent — checked, not assumed

Before finalizing this split, independently verified whether reflexes' own "halt must actually halt" fix (`docs/engineering/architecture/10-reflex-action-taxonomy.md`) was real and still holds, since several agents had claimed it was done. It is:

- `internal/agent/reflexes/resolve.go`'s `Resolve()` applies `deny_overrides` same-pass preemption for `halt_session` (Facet 2, built by `TASKS/reflex-taxonomy/03-shared-decision-engine.md`).
- `internal/service/chat_generate.go:538-557` loops over the returned reflex actions immediately after `evaluateAndInjectReflexes`, checks for `store.ReflexActionHaltSession`, and `return`s before the LLM provider call later in the same function (`TASKS/reflex-taxonomy/04-halt-turn-synchronicity.md`). `HaltHook`/`MarkSessionHalted` still runs synchronously earlier, unchanged — this closes the other half: stopping the *current* turn, not just flagging the session for a future request.
- Ran `TestGenerateResponse_HaltSessionReflex_AbortsTurnBeforeLLMCall` and `TestGenerateResponse_NonHaltReflex_DoesNotAbortTurn` directly against this checkout: both pass. The halt case asserts zero LLM provider calls *and* the session row shows `halted_at` set; the control case confirms a non-halt reflex still reaches the LLM normally.

**The pattern that fixed it, and why it transfers:** the constrained engine (`internal/store`-only) does the DB write; the actual "stop/affect the live thing" side effect happens one layer up, in a caller that already has an unconstrained view (`chat_generate.go` sits in `internal/service`, which can see everything). The engine never reaches past `internal/store` — the caller reaches instead. That is exactly the shape `render_card` needs: `internal/selftools/reactions` resolves *what* should render; a caller above it (the self-tool handler surfacing the marker, and ultimately whichever transport layer — in-process chat loop or CLI-agent proxy — is watching for it) does the actual reaching.

One real difference from halt: halt has exactly one synchronous caller (`chat_generate.go`), so "one engine call, one consumer" was sufficient. Self-tool reactions have to reach card rendering from at least two structurally distinct doors that don't share a call stack — the in-process chat-turn loop and the CLI-launched-agent proxy (`internal/api/tools_call.go`) — because CLI agents genuinely call in through a different path. Multiple doors are not a problem to eliminate; **what has to be shared is what happens past each door**: today, three independent consumers (`internal/service/chat_tool_executor.go`+`chat_generate.go:captureEnvelopeData`, `internal/api/tools_call.go:extractEnvelopeMarker`, `internal/mcpserver/handlers.go:convertEnvelopeMarkers`) each hand-scan for the same marker — the exact "duplicated by necessity, not oversight" shape the taxonomy doc already diagnosed for `dispatch_to_agent`'s three call sites. This design's fix is the same shape as `03`'s: collapse the three independent *interpretations* into one shared function, driven by the DB-backed category/kind lookup instead of hand-authored per-tool marker construction, called from each door that structurally has to exist. The marker signal itself stays — it's the only viable channel out of `internal/selftools` — only its consumption is unified.

## Telemetry

**Full coverage required** — every fired reaction gets a trace record, regardless of kind, mirroring `internal/agent/reflexes/telemetry.go`'s `EmitFirings`.

**Sink: `event_log`, reused as-is — no schema change needed.** Checked directly against the reflexes precedent (`TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md`, already implemented and reviewed) rather than assuming: that task hit this exact question and recorded *"No new dedicated table was added — `event_log`'s existing (session_id, event_type, category, detail, metadata) shape is sufficient once `event_type` carries the fired action_kind and `metadata` carries this file's traceRecord."* Confirmed the schema directly (`internal/store/migrations/001_schema.sql:473`): `session_id`, `event_type` TEXT, `category` TEXT, `detail` TEXT, `metadata` TEXT (JSON, default `'{}'`) — already fully polymorphic, no scope triggers here.

Self-tool reactions follow the identical pattern as a **new, parallel** thin wrapper in `internal/selftools/reactions` (not an extension of `internal/agent/reflexes/telemetry.go` — the taxonomy doc's own mechanisms stay untouched, per this session's scope fence): one `event_log` row per fired reaction —

- `event_type` = the reaction kind slug (`render_card`, `internal_api_call`, …)
- `category` = `"selftool_reaction"` — a new value, distinct from reflexes' existing `category = "reflex"`, so the two telemetry streams stay separately queryable via the existing `ListEvents(category, limit)` path
- `detail` = the tool name
- `metadata` = a trace record: tool name, reaction kind, the config used, the triggering `tool_call_id` (for correlating back to the exact `tool_use` block that caused it), success/error

## Worked example: `task_update_report(id, msg)`

Kept illustrative rather than bound to a real consumer (Nanite's own todo/plan store, or otherwise), per this session's decision on immediate-vs-deferred implementation below — the point is proving the shape is buildable, not shipping a specific integration. Renamed from the taxonomy doc's original `nanite_report_task_updated` to match the `<concept>_<verb>` convention every other self-tool already follows (`docs/tool-naming-convention.md`) — none of the ~70 registered self-tools carry a `nanite_` prefix except `nanite_code_execute`; the `nanite_` reservation is about *keeping the namespace clear of MCP-origin collisions*, not a naming requirement for every self-tool. (Operator note: the convention doc itself is flagged for a follow-up revisit, tracked separately, not in this session's scope.)

**Definition:**
- `id` (string, required) — opaque identifier; the harness never interprets it, only threads it through to reaction configs.
- `msg` (string, required) — human-readable update text.
- Description follows the existing When-to-use / When-NOT-to-use / Required-context / Output-shape convention every current self-tool description uses.

**Handler's own work is deliberately thin** — the concrete thing distinguishing it from a CRUD tool like `todo_update`: no independent persisted state of its own. It validates `id`/`msg`, calls `reactions.Fire(ctx, "task_update_report", payload)`, returns a short confirmation to the LLM (plus the `render_card` marker, if that reaction fired). Every side effect worth having happens through the reaction layer, not the handler — that's the actual point of the worked example: proving a self-tool can be *purely* declarative.

**Two reaction rows, exercising both execution shapes identified above:**

1. `render_card` — `config: {envelope_type: "info-card", template: {title: "Task update", body: "{{msg}}"}}`. Exercises the "engine resolves, caller surfaces" path.
2. `internal_api_call` — `config: {endpoint: "/api/example/task-updates", method: "POST", body_template: {id: "{{id}}", msg: "{{msg}}"}}`. Exercises the "engine executes directly" path.

Both fire independently off one `Fire()` call, proving the "both" case the mechanism's own definition promises, not just a single kind. Each fired reaction — both of these — emits one `event_log` row, demonstrating full telemetry coverage by construction rather than per-kind retrofitting.

## What this session did not decide

Listed explicitly, matching the parent doc's own convention, so a future reader doesn't mistake omission for a locked decision:

- Exact table/column names and DDL for `selftool_reaction_kinds`/`selftool_reactions` — illustrative shape only.
- The `internal_api_call`/`external_api_call`/`callback` reaction kinds' real execution semantics (auth, retries, idempotency, which internal endpoints are safe to expose this way) beyond the worked example's illustrative config shape.
- Provenance/authority tiering for reaction registration (which tier may register which reaction kind on which tool) — anticipated as a future extension (see `category` on `selftool_reaction_kinds` above) but not designed, since no plugin-registered self-tool exists today to need it.
- The exact shared-consumer refactor of the three existing `ENVELOPE_DATA` extraction sites (`chat_tool_executor.go`/`chat_generate.go`, `internal/api/tools_call.go`, `internal/mcpserver/handlers.go`) — the *direction* (collapse to one shared interpretation function per door) is decided; the concrete function signature/location is not.
- `docs/tool-naming-convention.md`'s own `nanite_*`-reservation wording, flagged during the worked example as worth revisiting — tracked separately by the operator, out of scope here.

## Status

Design complete and operator-signed-off as of 2026-08-20. No code or schema changed. **Implementation is deferred deliberately — document now, build once a concrete consumer exists** (the operator's explicit call, not a default-to-inaction). No follow-up implementation task file is filed by this session; `TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md`'s own scope stays design-only, matching how `TASKS/phase-4/10-reflex-architecture-review.md` closed out.

**Update, 2026-08-20 (later session):** implementation tasks filed — see `TASKS/harness-reactive-self-tools/README.md`. The "wait for a concrete consumer" call above still stands as the accurate record of this design session's own reasoning at the time; a later decision moved forward with filing the implementation work, to be executed once an operator books a dedicated Orchestrator session.
