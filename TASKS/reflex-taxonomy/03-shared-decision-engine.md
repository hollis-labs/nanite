# Shared decision engine — combining algorithms, one `Resolve()` primitive for all three call sites

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`)
**Status:** implemented
**Depends on:** `01-taxonomy-schema-foundation.md`, `02-recurrence-cascade.md`
**Touches:** `internal/agent/reflexes/` (new `resolve.go` or similar + `engine.go` rewrite), `internal/service/chat_reflex_dispatch.go` (`attemptReflexDispatch`'s selection loop), `internal/mcp/self_tools_dispatch.go` (`matchDispatchToAgentReflex`'s selection loop), `internal/service/chat_reflexes.go` (`formatReflexReminder` — verify only, likely no change needed).

## Context

This is the core deliverable `docs/engineering/architecture/10-reflex-action-taxonomy.md` calls "one shared decision engine, multiple legitimate invocation points." Three call sites today each hand-roll "list candidates → evaluate trigger → pick" independently:

1. `internal/agent/reflexes/engine.go:101-175` — `Engine.EvaluateState`'s inline loop (all non-`dispatch_to_agent` kinds, `dispatch_to_agent` explicitly skipped per Phase 4 #09, see the comment at lines 102-125 — **do not regress this skip**).
2. `internal/service/chat_reflex_dispatch.go` — `attemptReflexDispatch`'s own loop (`dispatch_to_agent` only), calling `reflexes.EvaluateTrigger` and `Executor.Apply` directly, bypassing `EvaluateState` (see `02`'s task file for why, now data-driven rather than hardcoded).
3. `internal/mcp/self_tools_dispatch.go:296-410` — `matchDispatchToAgentReflex`, a near-verbatim duplicate of (2), existing only because `internal/mcp` cannot import `internal/service` (which imports `internal/mcp` — a real cycle, not an oversight; both files' own comments say so explicitly).

The design doc's target shape: `reflexes.Resolve(candidates, state) → selected actions`, living in `internal/agent/reflexes` — the one package all three callers already import without a cycle — encoding the per-kind combining algorithm from Facet 2 (`deny_overrides` / `first_applicable` / `all_applicable`, modeled on XACML's policy-combining algorithms). **What does NOT unify:** how `State` gets built. (2) and (3) both populate `ScopeTier`/`ExecutionPattern`/a synthetic single-entry `UserMessages` window from live, not-yet-persisted, in-flight classification data — `StateCollector` (used by (1)) has no way to see a not-yet-committed turn. That's a legitimate, permanent reason for (2)/(3) to stay separate call sites; only their *selection* logic collapses into `Resolve()`.

This task also fixes the concrete `force_tool_choice` bug directly: two `force_tool_choice` reflexes naming different tools can currently both fire and both land in the same `<system-reminder>` block (`internal/service/chat_reflexes.go:formatReflexReminder`, lines 46-82) — the LLM sees two competing imperatives. Once `force_tool_choice` is classified `first_applicable` (seeded by `01`), `Resolve()` structurally prevents this.

## What to do

1. **Design `Resolve()`.** Something shaped like:
   ```go
   func Resolve(ctx context.Context, candidates []store.AgentReflex, state State, exec *Executor, cooldownFn func(store.AgentReflex) bool) (AppliedActions, []CandidateOutcome, error)
   ```
   (Exact signature is your call — the design doc explicitly leaves this as an implementation detail, not locked. `CandidateOutcome` — or whatever you name it — should carry enough per-candidate detail, fired-or-not and why, that `06-unified-reflex-telemetry.md` can build its `alternatives_considered`-style record from it without re-evaluating anything. Don't build `06`'s specific telemetry shape now — just don't return so little that `06` has to re-derive it.) Internally:
   - Evaluate each candidate's trigger (`EvaluateTrigger`, unchanged).
   - Apply `02`'s cooldown check — a candidate whose trigger fires but is still in cooldown is "not eligible," distinct from "trigger false" (both are useful telemetry facts, keep them distinguishable in whatever you return).
   - Group eligible-and-fired candidates by `action_kind`, look up each kind's `combining_algorithm` (via `01`'s lookup), and apply it:
     - `deny_overrides` — if any candidate of this kind is eligible-and-fired, it wins outright (tie-break: `priority DESC, created_at ASC`, matching `Store.ListAgentReflexesForAgent`'s existing order), and **no other kind's actions apply this pass** — short-circuit the whole resolution, not just this kind's slot.
     - `first_applicable` — among eligible-and-fired candidates of this kind, select only the highest-priority one (same tie-break).
     - `all_applicable` — select every eligible-and-fired candidate of this kind.
   - For each selected candidate, call `Executor.Apply` to get its `AppliedAction` (unchanged from today's per-call-site behavior).

2. **Rewire `Engine.EvaluateState`** (`engine.go:75-177`) to call `Resolve()` instead of its inline loop. Preserve: the plugin `ApplyFilter`/`EmitReflexFired`/`EmitReflexActionStaged` calls (still per-fired-action, now driven off `Resolve()`'s output instead of the inline loop's), the `fired_count` bump, and the `dispatch_to_agent` exclusion from this call site specifically (still filter `dispatch_to_agent` rows out of the candidate list *before* calling `Resolve()` here — `Resolve()` itself doesn't need to know about that exclusion, it's a property of what this caller passes in, not of the shared primitive).

3. **Rewire `attemptReflexDispatch`** (`chat_reflex_dispatch.go`) to call `Resolve()` for its `dispatch_to_agent` candidate list instead of its own hand-rolled loop. This is what the design doc means by "formalizes what `attemptReflexDispatch`/`matchDispatchToAgentReflex` already do informally today" for `first_applicable`. Keep this file's own `State`-building (the `ScopeTier`/`ExecutionPattern`/synthetic `UserMessages` construction, per design note 3 in its header comment) completely unchanged — only the selection loop moves.

4. **Rewire `matchDispatchToAgentReflex`** (`self_tools_dispatch.go`) the same way, for the same reason. Confirm this doesn't introduce an import-cycle — `Resolve()` living in `internal/agent/reflexes`, which `internal/mcp` already imports directly (for `reflexes.EvaluateTrigger`/`reflexes.State` today), means it shouldn't.

5. **Verify `formatReflexReminder`** (`chat_reflexes.go:46-82`) needs no change — it already loops per-action-kind safely, so once `Resolve()` guarantees at most one `force_tool_choice` action reaches `applied.Actions`, the existing loop should just naturally render one line instead of two. Confirm this with a test rather than assuming it; if it does need a change, make it and note why.

## Done means

- `Resolve()` (or equivalently-shaped primitive) exists once, in `internal/agent/reflexes`, and is the **only** place any of the three call sites implements "which fired candidates actually get applied." No call site still hand-rolls its own priority-ordering/first-wins/all-fire loop.
- **Regression test — the `force_tool_choice` bug**: two `force_tool_choice` reflexes naming different tools, both triggers true in the same evaluation pass → exactly one action reaches `formatReflexReminder`'s output (documented tie-break verified: higher priority wins; equal priority → earlier `created_at` wins).
- **Regression test — same-pass halt preemption** (half of the design doc's "halt must actually halt" fix; the other half, turn-level abort, is `04`'s job): a `halt_session` reflex and an unrelated `inject_reminder` reflex both fire in the same pass → `Resolve()`'s output contains only the halt action, not both.
- Phase 4 #09's existing regression test (`internal/agent/reflexes/dispatch_to_agent_generic_pass_test.go`) still passes — `dispatch_to_agent` rows remain invisible to `EvaluateState`'s pass, unchanged by this rewrite.
- `attemptReflexDispatch`'s and `matchDispatchToAgentReflex`'s own existing tests (real-session dispatch of a `dispatch_to_agent` reflex to a target agent) still pass, now running through `Resolve()` rather than the old inline loop — same observable outcome, different internal path.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Implemented `Resolve()` and rewired all three call sites as specified.

**New: `internal/agent/reflexes/resolve.go`**
```go
type ActionKindLookup func(ctx context.Context, actionKind string) (*store.ReflexActionKind, error)
type CooldownFunc func(r store.AgentReflex) bool
type CandidateOutcome struct {
    ReflexID, ReflexName, ActionKind, CreatedAt string
    Priority                                     int64
    TriggerFired, CooldownSuppressed, Eligible, Selected bool
    TriggerError, CombiningAlgorithm, ApplyError string
}
func Resolve(ctx context.Context, candidates []store.AgentReflex, state State, exec *Executor,
    cooldownFn CooldownFunc, kindLookup ActionKindLookup) (AppliedActions, []CandidateOutcome, error)
```
Went with the task's suggested shape plus one addition (`kindLookup ActionKindLookup`) — needed
because `matchDispatchToAgentReflex` (internal/mcp) has no `*Engine` to resolve a cached
combining-algorithm from, only a bare `*store.Store`; making the kind lookup a caller-supplied
function (same pattern as `cooldownFn`) lets each of the three call sites supply its own
resolution strategy (Engine's in-memory cache for `engine.go`'s own call; a direct/reused
`store.GetReflexActionKind` row for the two dispatch call sites, which only ever query one kind)
without widening `Resolve()`'s coupling to a concrete `*store.Store` or `*Engine`.

Algorithm: evaluate every candidate's trigger, then cooldown-eligibility (kept distinguishable in
`CandidateOutcome` per the brief — `TriggerFired=false` vs `CooldownSuppressed=true` are different
facts). Eligible candidates are grouped by `action_kind`; each present kind's `combining_algorithm`
(via `kindLookup`) is resolved once. `deny_overrides` candidates across *every* deny_overrides kind
present this pass are unioned and the single top-priority one (tie-break priority DESC, created_at
ASC) short-circuits the whole resolution. Otherwise `first_applicable` picks one winner per kind
and `all_applicable` selects every eligible candidate of that kind; selected candidates are applied
via `exec.Apply` in the original candidates-slice order. An unresolvable kind fails open to
`all_applicable` (today's pre-taxonomy universal behavior); a nil `Executor` is a real returned
error; a per-candidate `Apply` failure is recorded on that candidate's `ApplyError` and simply
isn't selected (never propagates as a `Resolve()` error — matches every pre-existing call site's
own "log and skip" handling).

One deliberate design note not spelled out in the task file: `deny_overrides` is resolved as a
union *across every kind* classified `deny_overrides`, not just per-kind — only `halt_session` is
seeded as `deny_overrides` today so this is unobservable in practice, but it generalizes correctly
if a second `deny_overrides` kind is ever added later without needing to revisit this file.

**Rewired `Engine.EvaluateState`** (`internal/agent/reflexes/engine.go`): the `dispatch_to_agent`
exclusion-before-Resolve() filter, the cooldown/kind-lookup closures (built from the Engine's own
private `actionKinds` cache — no new exported Engine method needed since `Resolve()` is called
from within the same package), and the fired_count bump / plugin `ApplyFilter`+`EmitReflexFired`+
`EmitReflexActionStaged` calls are all preserved, now driven off `Resolve()`'s returned
`AppliedActions`/`[]CandidateOutcome` instead of an inline loop. Per-candidate `TriggerError`/
`ApplyError` outcomes are still logged via `e.Logger.Warn`, matching the prior inline log calls.

**Rewired `attemptReflexDispatch`** (`internal/service/chat_reflex_dispatch.go`): candidate list is
now explicitly filtered to `dispatch_to_agent` before calling `Resolve()` (previously the filter
was inline in the loop) — same "caller's own job, not Resolve()'s" property as `EvaluateState`'s
exclusion. `cooldownFn` reuses the Engine's cached `ActionKindDefaultRecurrenceSeconds` (unchanged
lookup mechanism); `kindLookup` fetches `dispatch_to_agent`'s `reflex_action_kinds` row once (for
`combining_algorithm`) and returns that cached value for the closure's lifetime, since the filtered
candidate list only ever contains this one kind. `event_log`'s `alternatives_considered` metadata
is rebuilt from `Resolve()`'s returned `[]CandidateOutcome` instead of a hand-built `[]evalResult`
slice — same three fields (`reflex_id`, `reflex_name`, `fired`), same "exclude the winner" filter.
`ls`/`State`-building (`ScopeTier`/`ExecutionPattern`/synthetic `UserMessages`) is untouched, per
the instruction. Updated the file's header comment (design note 2) to stop saying
`matchDispatchToAgentReflex` "hand-copies this evaluation loop" — it now shares the same
`Resolve()` decision primitive, only the State-building/store-access stays duplicated (the
still-legitimate reason, per the architecture doc).

**Rewired `matchDispatchToAgentReflex`** (`internal/mcp/self_tools_dispatch.go`): same
candidate-filtering-before-Resolve() pattern. This call site has no `*reflexes.Engine` of its own
(`SelfToolsTransport` only holds `*store.Store`), so: (a) `kindLookup` fetches
`dispatch_to_agent`'s kind row directly via `store.GetReflexActionKind` (same single fetch this
function already did pre-task for the recurrence lookup — reused for both purposes, no added DB
round trip), and (b) a bare `&reflexes.Executor{Logger: slog.Default()}` is constructed locally
since there's no Engine-owned Executor to reuse — confirmed equivalent to this function's own
pre-task hand-rolled `json.Unmarshal(r.ActionSpec, &spec)` for the `dispatch_to_agent` kind
specifically, since `Executor.Apply`'s `dispatch_to_agent` case (executor.go) touches no hook, it
only parses `action_spec` into the returned `AppliedAction.Spec` — the exact same parse this
function did manually before. No import cycle: `internal/agent/reflexes` is already imported
directly by `internal/mcp` (for `reflexes.EvaluateTrigger`/`reflexes.State`), confirmed by a clean
`go build ./...`. Updated the function's header comment to stop describing the evaluation loop as
"replicat[ing] the shape of attemptReflexDispatch's own evaluation loop" — it now shares the
combining-algorithm decision via `Resolve()`; only State-building/store access remain duplicated,
which the architecture doc calls out as legitimate and permanent.

**One behavior narrowing worth flagging** (not covered by any existing test, and effectively
unreachable for validly-written data): the pre-task inline loops at both `dispatch_to_agent` call
sites would, on an `Executor.Apply`/JSON-parse failure for the current best candidate, fall through
and try the next-lower-priority candidate instead. `Resolve()`'s `first_applicable` does not
cascade like that — it picks exactly one candidate (the highest-priority eligible one) and if
*that* candidate's `Apply` fails, no candidate of that kind is selected this pass, full stop. This
matches the design doc's literal "the highest-priority eligible candidate ... is selected" wording
for `first_applicable` more faithfully than the old ad hoc fallback did, and `Executor.Apply` can
only fail here on a malformed `action_spec` JSON blob — a case `validateReflexDefinition`
(`internal/api/reflexes.go`) already rejects at write time for anything created through the CRUD
path, so this is a defensive-only edge case in practice, not an observed behavior change. Noted
here per the process's "note the correction, keep going" discipline rather than silently changing
behavior undocumented.

**`formatReflexReminder` (`internal/service/chat_reflexes.go`)**: verified via test, no code change
needed — it already loops per-action-kind independently, so once `Resolve()` guarantees at most one
`force_tool_choice` action reaches `applied.Actions` (via the kind's `first_applicable`
classification, seeded by task 01), the existing loop renders exactly one line, confirmed by
`TestFormatReflexReminder_ForceToolChoiceBug_ExactlyOneActionWins` and its equal-priority
tie-break sibling (below).

**New tests:**
- `internal/agent/reflexes/resolve_test.go` — direct `Resolve()` unit coverage: `all_applicable`
  selects every eligible candidate; `first_applicable` picks the higher-priority candidate and,
  separately, the earlier-`created_at` candidate on an exact priority tie; `deny_overrides`
  preempts an unrelated, much-higher-priority `all_applicable` kind in the same pass
  (`TestResolve_DenyOverrides_ShortCircuitsOtherKinds`); cooldown-suppression is kept
  distinguishable from trigger-false in `CandidateOutcome`; an unresolvable kind fails open to
  `all_applicable`; a nil `Executor` returns a real error.
- `internal/agent/reflexes/halt_preemption_test.go` —
  `TestEvaluateState_HaltSessionPreemptsInjectReminder_SamePass`: the task's named "same-pass halt
  preemption" regression, end-to-end through the real `Engine.EvaluateState` against a real store
  with a real seeded `reflex_action_kinds` row (not a hand-built lookup fixture) — a `halt_session`
  reflex (priority 10) and an unrelated `inject_reminder` reflex (priority 999, deliberately much
  higher, to prove preemption isn't just "higher priority wins") both fire in the same pass;
  asserts `EvaluateState`'s output contains only the halt action, the reminder's `fired_count`
  stays 0, and plugin hooks fire exactly once (for the halt only).
- `internal/service/chat_reflexes_force_tool_choice_regression_test.go` — the task's named
  "force_tool_choice bug" regression at the level the Done-means explicitly names
  (`formatReflexReminder`'s rendered output): two `force_tool_choice` reflexes naming different
  tools, both triggers true in the same pass, end-to-end through the real `Engine.EvaluateState` ->
  `formatReflexReminder` — exactly one line survives, naming the higher-priority tool
  (`TestFormatReflexReminder_ForceToolChoiceBug_ExactlyOneActionWins`), and a second test pins the
  equal-priority/earlier-`created_at` tie-break
  (`TestFormatReflexReminder_ForceToolChoiceBug_EqualPriority_EarlierCreatedAtWins`).

**Verified unchanged / still passing:**
- Phase 4 #09's `internal/agent/reflexes/dispatch_to_agent_generic_pass_test.go` — both tests pass
  unmodified; `dispatch_to_agent` rows are filtered out of `EvaluateState`'s candidate list before
  `Resolve()` is ever called, same as before.
- `attemptReflexDispatch`'s own tests (`chat_reflex_dispatch_integration_test.go`,
  `chat_reflex_dispatch_cooldown_test.go`, `chat_reflex_dispatch_promptrouter_migration_test.go`,
  `chat_reflex_dispatch_generic_pass_leak_test.go`) all pass unmodified, now running through
  `Resolve()`.
- `matchDispatchToAgentReflex`'s own tests
  (`self_tools_dispatch_cooldown_test.go`, `self_tools_dispatch_audit_test.go`) pass unmodified,
  now running through `Resolve()`.
- `internal/agent/reflexes/recurrence_test.go`, `engine_plugin_test.go` — unaffected, pass
  unmodified.

**Baseline check:** `go build ./cmd/nanite/` clean; `go build ./...` clean; `go vet ./...` shows
only the two pre-existing, documented-unrelated `internal/service/container.go` findings
(`stopReaper`/`stopRuntimeReaper` possible-context-leak, lines 1139/1159/1210) — left untouched per
the task file's own instruction; `go test ./...` all green (`internal/agent/reflexes`,
`internal/service`, `internal/mcp` explicitly re-run with `-count=1` to rule out cache
false-positives, plus a full repo-wide `go test ./...` pass).

No deviation from the task's stated instruction. `git status --short` after all edits shows only
the files this task actually touched plus the already-in-flight, pre-existing task 01/02 changes
(`internal/store/{agent_reflexes.go,reflex_taxonomy.go,migrations/124_reflex_action_taxonomy.sql,
migration_124_reflex_action_taxonomy_test.go}`, `internal/agent/reflexes/{recurrence.go,
recurrence_test.go}`, and the two pre-existing cooldown test files) — nothing stray written outside
scope.

## Review notes

<!-- Reviewer fills in. -->
