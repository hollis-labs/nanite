# Shared decision engine — combining algorithms, one `Resolve()` primitive for all three call sites

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`)
**Status:** not-started
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

<!-- Worker fills in as it goes. -->

## Review notes

<!-- Reviewer fills in. -->
