# Cut the strategy planner, in full

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/strategy/` (delete entire package: `types.go`, `plan.go`, `plan_test.go`, `review.go`, `review_test.go`, `logger.go`), `internal/store/strategy_log.go` + `internal/store/strategy_log_test.go` (delete), `internal/service/chat_strategy.go` + `internal/service/chat_strategy_test.go` (delete), `internal/service/chat_generate.go` (remove the E3 call site, ~lines 762–778), `internal/service/chat.go` (remove `StrategyLogger`/`strategyLogger` field + wiring, ~lines 160–164, 310–312, 500), `internal/service/container.go` (remove `StrategyLogger: cfg.Store` wiring, ~lines 1041–1044), `internal/inspector/types.go` (remove `StrategyRecord` type + `Strategy *StrategyRecord` field), `ui/src/lib/types.ts` (remove the `strategy?: { max_turns, reasoning }` field, ~line 1863), `ui/src/components/settings/inspector/InspectorPanel.tsx` (remove the "Strategy" panel block, ~lines 342–365)

**Not touched by this task** (verified — see Context): `internal/store/migrations/034_strategy_decisions.sql` and the `strategy_decisions` table itself (dropping it is Phase 0 item 23's job, which runs after this item lands and removes the table's last writer); `internal/promptrouter` (stays fully alive — consumed by `internal/mcp/self_tools_dispatch.go` and `internal/service/chat_broker_dispatch.go` independently of the strategy planner); `internal/service/chat_loop_state.go`'s `AgentConstraints.MaxTurns`/`checkSoftMaxTurnsWarning`/soft-warning machinery — that is Phase 0 item 12's separate cut (see the parallelization note below, this file and item 12 both touch `chat_generate.go` so **do not run them in parallel**).

## Context

TASKS.md Phase 0 item 11: "**Strategy planner, full removal.** Its replacement (the reaper's activity-based termination) is already built and shipped — no reason to wait for the steering redesign."

`docs/architecture-decision-log-2026-08-17.md` §10 ("Steering — agent broker, strategy planner, modes, promptrouter"):

> **Strategy planner — full cut, not partial.** Both outputs are gone: `Approach` was already decorative (logged, never gated behavior), `MaxTurns` is being replaced per above. Original intent was "hint/suggest to save context," not hard control — hard-gating experiments produced dead-end conversations, consistent with Anthropic's own published guidance (hints over control). Real-time wake/message delivery is available as the general steering mechanism if something like this is needed later; can always rebuild narrower if a real need appears.

§12 ("Harness — turn loop..."): "Two redundant 'soft' turn-budget mechanisms are cut: the strategy planner's `MaxTurns` and, separately, `AgentConstraints.MaxTurns`... The real, hard stoppers stay as-is: `RunawayFailCap` (10, hard), `HardCeiling` (200, hard...), `IdleTimeoutSeconds`, natural `end_turn`."

Architecture doc `docs/engineering/architecture/03-steering.md`: "**The strategy planner, in full.** Both outputs were already effectively dead: `Approach` was decorative (logged, never gated behavior); `MaxTurns` is superseded by harness-level termination bounds." `04-harness.md`: "Two redundant 'soft' turn-budget mechanisms are cut: the strategy planner's `MaxTurns` and, separately, `AgentConstraints.MaxTurns`... The real, hard stoppers stay as-is."

The `d92d8cf` commit named in the original brief as "the reaper's activity-based termination" **was not found in this repo's visible `git log --oneline` history** (checked — not present). Don't block on finding it; the replacement mechanism it refers to is independently verifiable in the current code (see below) regardless of which commit introduced it.

### Verified against real code (2026-08-18)

**Both `Approach` and `MaxTurns` are confirmed dead/decorative in the current code — the decision log's claim holds up exactly:**

1. **`Approach`** (`internal/strategy/types.go`) is computed by `PlanStrategy`, logged (`internal/service/chat_strategy.go:94`, `slog.Info("chat-service: strategy decision", "approach", ...)`), and persisted via `LogStrategyDecision` — and never read back or branched on anywhere else. `ListStrategyDecisions` (`internal/store/strategy_log.go:92`) has **zero callers** outside its own file and tests — no API handler, no frontend consumer. Fully write-only.

2. **`MaxTurns`** (`strategy.Strategy.MaxTurns`, values 10/20/40) is applied to the loop via `applyStrategyToLimits(ls, strat)` (`chat_strategy.go:145`, called from `chat_generate.go:778`), which sets `ls.limits.maxTurns = strat.MaxTurns`. **This looks like real gating at first glance, but it isn't**: `internal/service/chat_loop_state.go:29-59` documents (commit `CW-20260504-0001`) that `ls.limits.maxTurns` — regardless of whether it came from the strategy planner or the `AgentConstraints.MaxTurns` fallback — is **already a soft hint, not a hard terminator**. `shouldStop()` (`chat_loop_state.go:444-472`) only checks `runawayFailCap`, `idleTimeout`, and `hardCeiling`; hitting `maxTurns` no longer appears in `shouldStop`'s logic at all. The only thing `ls.limits.maxTurns` still drives is `checkSoftMaxTurnsWarning()` (`chat_loop_state.go:474-495`), a one-shot telemetry/SSE signal that does not stop the loop — that's Phase 0 item 12's `chat-loop-budget-soft-warning` signal, a **separate** cut.

3. **The "mid-execution review" half of the strategy planner (`ReviewMidExecution`/`ClarifyingQuestion`) is already fully orphaned, independent of this cut.** `reviewExhaustedBudget`, `strategyClarifyingQuestion`, and `strategy_pkg_ReviewAskToClarify` (all in `chat_strategy.go`) have **zero call sites** in `chat_generate.go` or anywhere else in production code — only their own unit tests in `chat_strategy_test.go` call them. This matches `chat_loop_state.go:39-43`'s comment: "the strategy-review-on-max-turns branch and max-turns synthesis paths are gone." So roughly half of `internal/strategy`'s public surface (`review.go`'s `ReviewMidExecution`/`ClarifyingQuestion`, plus the `chat_strategy.go` wrappers around them) is dead code today, not just soon-to-be-dead.

4. **The inspector's `Strategy` telemetry was never wired up at all.** `internal/inspector/types.go:29-30,124-133`: `Strategy *StrategyRecord` is documented "nil until the strategy producer wires up" — grepped for any `StrategyRecord{...}` construction or `.Strategy =` assignment anywhere in the codebase: **none exists**. The frontend's `InspectorPanel.tsx` "Strategy" panel (~line 342) always renders its `EmptyProducer label="Strategy — no signal yet, producer not wired."` fallback in practice, never the populated branch. This was inert scaffolding from day one — safe, zero-behavior-change cleanup to remove alongside the rest.

**No doc/reality mismatch found for this item** — reality matches the decision log's characterization exactly. The one nuance worth knowing (documented above) is that `MaxTurns` *looks* load-bearing (`applyStrategyToLimits` really does write into `ls.limits.maxTurns`) but a prior commit (`CW-20260504-0001`) already neutered that field's effect on termination — so this cut has zero behavior-visible effect on when a turn actually stops. It only removes an already-decorative telemetry/logging layer.

## What to do

1. Delete `internal/strategy/` entirely (`types.go`, `plan.go`, `plan_test.go`, `review.go`, `review_test.go`, `logger.go`).
2. Delete `internal/store/strategy_log.go` and `internal/store/strategy_log_test.go` (`LogStrategyDecision`, `ListStrategyDecisions`, `StrategyDecisionRow`, `scanStrategyDecisionRow` or equivalent). **Leave migration `034_strategy_decisions.sql` and the `strategy_decisions` table alone** — TASKS.md item 23 drops it later, after this item and items 21/22 remove all its writers; dropping it now would be scope creep into a different, not-yet-scheduled task.
3. Delete `internal/service/chat_strategy.go` and `internal/service/chat_strategy_test.go` in full (`planStrategyForTurn`, `applyStrategyToLimits`, `reviewExhaustedBudget`, `strategyHasUsableData`, `strategyClarifyingQuestion`, `strategy_pkg_ReviewAskToClarify`, `groundingSignalFromResult`, the `strategyDecisionLogger` interface).
4. In `internal/service/chat_generate.go`, remove the E3 strategy-planning block in full (currently ~lines 762–778: the comment block, `scopeTier, executionPattern := ls.Classification()`, the `planStrategyForTurn(...)` call, and `applyStrategyToLimits(ls, turnStrategy)`). Verified: `scopeTier`/`executionPattern` from that line have no other use in `generateResponse` — the only other `scopeTier`/`executionPattern` in the file (~lines 2840-2852) are locally-scoped variables inside a different function (`classifyAndAttach`), not the same binding, so removing line 769 is safe and complete, not a partial removal. Leave the loop's `checkSoftMaxTurnsWarning`/`emitChatLoopBudgetSoftWarning` call (~lines 807-814) untouched — that belongs to item 12, not this task.
5. In `internal/service/chat.go`, remove the `StrategyLogger strategyDecisionLogger` field (and its doc comment) from the service config struct (~line 160-164) and the `strategyLogger strategyDecisionLogger` field (and doc comment) from the service impl struct (~line 310-312), plus the `strategyLogger: cfg.StrategyLogger,` line in the constructor (~line 500).
6. In `internal/service/container.go`, remove the `StrategyLogger: cfg.Store,` wiring and its `CW-20260419-0026 (E3)` comment (~lines 1041-1044).
7. In `internal/inspector/types.go`, remove the `Strategy *StrategyRecord` field (~line 29-30) and the `StrategyRecord` type definition (~line 124-133).
8. In `ui/src/lib/types.ts`, remove the `strategy?: { max_turns: number; reasoning?: string }` field (~line 1863) from whatever turn-snapshot type it's attached to.
9. In `ui/src/components/settings/inspector/InspectorPanel.tsx`, remove the "Strategy" `PanelCard` block (~lines 342-365) including its `EmptyProducer` fallback branch — the whole `snap.strategy ? (...) : (...)` conditional.
10. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`; run `cd ui && npm run build` (or the project's frontend typecheck) to confirm the `types.ts`/`InspectorPanel.tsx` edits don't break the frontend build.
11. Grep the whole repo for `internal/strategy`, `chat_strategy`, `StrategyLogger`, `strategyDecisionLogger`, `planStrategyForTurn`, `StrategyRecord` after the cut to confirm no dangling references remain (besides the untouched `034_strategy_decisions.sql` migration and its table).

## Done means

- `internal/strategy/` no longer exists.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` all pass with zero references to the deleted package/types.
- `chat_generate.go`'s tool-use loop no longer calls `planStrategyForTurn`/`applyStrategyToLimits`; `ls.limits.maxTurns` is now populated exclusively from `chat.AgentConstraints.MaxTurns` (or its default) — confirm this still compiles and the loop still runs a real turn end-to-end (dogfeed a chat session, per `EXECUTION-PROCESS.md`'s validation-checkpoint guidance — full validation happens at the Phase 0 section boundary, but a basic manual sanity check here is cheap and catches a broken wiring early).
- `strategy_decisions` table and its migration file are untouched (still present, just no longer written to — item 23 handles the drop).
- The inspector's "Strategy" panel (frontend) and `StrategyRecord` (backend) are gone; the inspector's "meta" tab still renders the `ModeSection` and other panels without error.
- No remaining references anywhere in the repo to `internal/strategy`, `StrategyLogger`, `strategyDecisionLogger`, `planStrategyForTurn`, `applyStrategyToLimits`, or `StrategyRecord`.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
