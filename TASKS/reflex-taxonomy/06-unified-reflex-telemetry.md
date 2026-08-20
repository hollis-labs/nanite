# Unified reflex telemetry — one trace record, one sink, full plugin-hook coverage

**Phase:** 2 — Provenance & Telemetry (`TASKS/reflex-taxonomy`)
**Status:** not-started
**Depends on:** `03-shared-decision-engine.md` (needs `Resolve()` as the single decision point to hook emission into), `04-halt-turn-synchronicity.md` (both touch `chat_generate.go`/`chat_reflexes.go`'s reflex call site — land after `04` to avoid re-churning the same lines twice)
**Touches:** `internal/agent/reflexes/` (`Resolve()` or the executor path, wherever emission gets centralized), `internal/service/chat_reflexes.go` (`evaluateAndInjectReflexes`'s `event_log` write), `internal/service/chat_reflex_dispatch.go` (`attemptReflexDispatch`'s existing `event_log`/`alternatives_considered` write), `internal/mcp/self_tools_dispatch.go` (`matchDispatchToAgentReflex`'s `playbook_match_log` write via `ReflexLogger`/`LogReflexMatch`), `internal/api/reflexes.go` or wherever reflex telemetry is read back for the operator UI (`GET /api/agents/{id}/reflexes`'s `fired_count`/`last_fired_at`).

## Context

`docs/engineering/architecture/10-reflex-action-taxonomy.md`, "Telemetry" section — three confirmed gaps, all from the same root cause as the call-site duplication `03` already fixed for *selection* logic: three code paths independently implementing "a reflex fired," never unified for *telemetry* either.

1. **Split sinks.** `dispatch_to_agent` firings from `attemptReflexDispatch` (chat-turn-loop path) write to `event_log` (`internal/service/chat_reflex_dispatch.go`, event_type `dispatch_to_agent`, category `reflex`). The same kind of event from `matchDispatchToAgentReflex` (the `task_execute` self-tool path, `internal/mcp/self_tools_dispatch.go:405`) instead writes to `playbook_match_log` via `ReflexLogger.LogReflexMatch` (`store.ReflexMatchLogEntry`, `internal/store/migrations/032_playbook_match_log.sql`) — a different table, different schema, no shared shape.
2. **`fired_count`/`last_fired_at` blind spot.** `Engine.EvaluateState` and `attemptReflexDispatch` both call `Store.BumpAgentReflexFired`; `matchDispatchToAgentReflex` never does. A `dispatch_to_agent` reflex firing mainly through the self-tool path shows `fired_count: 0`/`last_fired_at: null` in the operator UI's reflex list while actively routing turns.
3. **Plugin hooks blind to two of three paths.** `PluginHooks.EmitReflexFired`/`EmitReflexActionStaged` (`internal/agent/reflexes/engine.go:15-16`) are invoked exclusively from inside `Engine.EvaluateState`'s loop. Both `dispatch_to_agent` call sites bypass `EvaluateState` by design and call `Executor.Apply` directly — any plugin instrumented against those hooks is structurally blind to every `dispatch_to_agent` firing.

Design doc's resolution: *"this isn't separate work from the shared-decision-engine fix — once `reflexes.Resolve(...)` is the single place a firing gets decided, telemetry emission belongs there too, so the three gaps above close as a side effect of that fix rather than needing three separate patches later."*

**What the trace record should carry**, beyond what any single path captures today: `reflex_id`/`name`/`action_kind` (existing everywhere), the resolved **category** and **combining algorithm** applied (new — from `01`'s lookup), **why this one won** when a combining algorithm suppressed competitors (generalize `attemptReflexDispatch`'s existing `alternatives_considered` list — every candidate evaluated that turn, with whether it fired — to every kind under `first_applicable`/`deny_overrides`, not just `dispatch_to_agent`), **provenance tier** (new — from `05`), and **recurrence outcome** (did the trigger fire true but get suppressed by cooldown, and at which cascade level — system/kind/reflex — per `02`).

**Sink choice is explicitly an implementation call, not an architecture one** (design doc's own words) — pick `event_log` (already the majority path, general-purpose table) or promote `playbook_match_log` (currently narrowly named/shaped for `dispatch_to_agent` only). Recommendation: `event_log`, since it's already general-purpose and already the majority path — but document whichever you choose and why; this isn't a blocking decision to escalate.

## What to do

1. **Decide and document the unified sink** (event_log vs. widening playbook_match_log vs. a new dedicated table) — pick one, don't build two.

2. **Centralize emission.** Move the event_log/trace write, the `fired_count`/`last_fired_at` bump, and the `EmitReflexFired`/`EmitReflexActionStaged` plugin hook calls into `Resolve()` itself (or a thin wrapper every caller of `Resolve()` goes through immediately) — one implementation, not three call sites each independently patched to add whatever they're currently missing. Check what each of the three currently does before centralizing, so nothing regresses:
   - `Engine.EvaluateState` today: bumps `fired_count` + fires plugin hooks, but the actual `event_log` write happens one layer up in `chat_reflexes.go:evaluateAndInjectReflexes` (line 35), not inside the engine itself.
   - `attemptReflexDispatch` today: writes `event_log` (with `alternatives_considered`) + bumps `fired_count`, but never fires plugin hooks.
   - `matchDispatchToAgentReflex` today: writes only to `playbook_match_log`, does none of the other two.

   Land on one consistent set of side effects that happens exactly once per real firing, regardless of which of the three call sites produced it.

3. **Retire `matchDispatchToAgentReflex`'s dedicated `playbook_match_log` write** in favor of the unified sink — but first grep for any real reader of `playbook_match_log`/`ReflexMatchLogEntry`/`LogReflexMatch` (an operator UI, a CLI report, an admin export) before removing the write path. If a real reader exists, either keep writing both during a transition and note the follow-up to migrate the reader, or migrate the reader in this same task if it's small — don't silently starve a real consumer.

4. **Generalize `alternatives_considered`.** `attemptReflexDispatch`'s existing pattern (every `dispatch_to_agent` candidate evaluated that turn, fired or not) should become the standard shape emitted for every kind under `first_applicable`/`deny_overrides` (where "why did this one win over that one" is a meaningful question — `all_applicable` doesn't need it, everything that fired was applied). `03`'s `Resolve()` should already be returning enough per-candidate detail for this — use it, don't re-evaluate candidates a second time just to build the telemetry record.

5. **Add the new fields** — category, combining_algorithm, provenance_tier, recurrence outcome (fired-but-cooldown-suppressed + which cascade level set the effective cooldown) — to the emitted record.

## Done means

- One trigger of each of `inject_reminder`, `force_tool_choice`, `halt_session`, and `dispatch_to_agent` (via **both** the chat-turn-loop path and the `task_execute` self-tool path) produces one consistent-shape trace record in the one chosen sink, with `fired_count`/`last_fired_at` bumped and plugin hooks fired in every case — verified by a test exercising all four kinds across both dispatch-to-agent entry points, not just the paths that already worked before this task.
- **Verified fix for gap 2, live**: a `dispatch_to_agent` reflex fired exclusively via the `task_execute` self-tool path shows a non-zero `fired_count`/non-null `last_fired_at` via `GET /api/agents/{id}/reflexes` — reproduce the exact previously-broken scenario and confirm it's fixed, not just unit-tested.
- `matchDispatchToAgentReflex`'s dedicated `playbook_match_log` write is either removed (with confirmation no real reader was orphaned) or explicitly kept with documented reasoning — either outcome recorded in this file's Work Log.
- A test confirms `alternatives_considered`-style data is now present for a `force_tool_choice`/`halt_session` firing, not just `dispatch_to_agent`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

<!-- Worker fills in as it goes. -->

## Review notes

<!-- Reviewer fills in. -->
