# Halt must actually halt — abort the current turn synchronously

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`)
**Status:** not-started
**Depends on:** `03-shared-decision-engine.md` (needs `Resolve()`'s `deny_overrides` guarantee that a fired `halt_session` action is alone in the result set, making "did a halt fire this pass" unambiguous to check)
**Touches:** `internal/service/chat_generate.go` (the `evaluateAndInjectReflexes` call site, ~line 523), `internal/service/chat_reflexes.go` (`evaluateAndInjectReflexes`'s return handling).

## Context

`docs/engineering/architecture/10-reflex-action-taxonomy.md`, "Halt must actually halt" — two distinct, both-in-scope fixes, operator sign-off quoted directly: *"deny-overrides actually stop the turn."* `03` builds fix (1), same-pass preemption. This task is fix (2), turn-level synchronicity:

Today, a fired `halt_session` reflex only reaches `internal/agent/reflexes/executor.go`'s `HaltHook` (wired in `internal/service/container.go:918-922` to call `cfg.Store.MarkSessionHalted(sessionID, reason)` → `internal/store/session_halt.go:49`), which stamps `halted_at`/`halted_reason` on the session row. That flag is only checked later — `internal/api/frontend_readiness.go` surfaces it on a **subsequent** request. The turn that triggered the halt completes normally and reaches the LLM anyway.

The actual gap: `internal/service/chat_generate.go`, right after the `evaluateAndInjectReflexes` call:

```go
// FU-30: evaluate DB-backed agent reflexes for this turn and inject any
// staged actions (e.g. inject_reminder) into SlotUserContext. nil-safe via
// the engine guard inside evaluateAndInjectReflexes.
if actions := s.evaluateAndInjectReflexes(ctx, session, agent, slotResult); len(actions) > 0 {
    systemPrompt = slotResult.SystemPrompt
}
```

`evaluateAndInjectReflexes` (`chat_reflexes.go:14-44`) returns `[]reflexes.AppliedAction` — this call site only checks `len(actions) > 0` to decide whether to refresh `systemPrompt`. It never inspects `action.ActionKind`, so a `halt_session` action in that slice is treated identically to an `inject_reminder` — the turn proceeds straight through to the LLM call that follows later in `generateResponse`.

## What to do

1. In `evaluateAndInjectReflexes` or at the `chat_generate.go` call site (your call which — a bare `for _, a := range actions { if a.ActionKind == store.ReflexActionHaltSession { ... } }` inline check at the call site is probably simplest and needs no signature change; only introduce a richer return type if it clearly reads better), detect whether any returned action is `store.ReflexActionHaltSession`. Since `03`'s `Resolve()` applies `deny_overrides` to `halt_session`, if one is present it is guaranteed to be the *only* action in the slice for that pass — no need to handle "halt plus other actions" as a real case, but don't rely on that invisibly; a one-line comment noting why is enough.

2. When a halt action is present, abort `generateResponse` **before** it reaches the LLM provider call later in the same function. Look at how this file already handles other early-abort conditions (e.g. a provider-resolution error) and match that existing return/error shape rather than inventing a new one — read the surrounding ~100 lines of `chat_generate.go` around the reflex call site and the function's own early-return patterns before choosing.

3. Confirm the existing `HaltHook` → `MarkSessionHalted` path (`container.go:918-922`) is unaffected — this task doesn't change how the session gets flagged, only ensures the *current* turn also stops. Both must be true after this task: the session is marked halted (unchanged) AND no LLM call happens for the turn that triggered it (new).

4. Confirm `internal/api/frontend_readiness.go`'s existing halted-session surface for a *subsequent* request is untouched — that mechanism stays correct and necessary; this task only closes the gap for the turn that fired the halt, not future ones (those were never broken).

## Done means

- A test (real or near-real slice of `generateResponse` — use whatever harness the existing reflex/generate tests in this package already use) seeds a `halt_session` reflex whose trigger fires on the test turn, invokes the turn, and confirms **both**: no LLM provider call was made for that turn, and the session row shows `halted_at`/`halted_reason` set. Testing only one of the two would not actually prove the fix.
- Existing halt-adjacent tests (`internal/api/frontend_readiness_test.go`, `internal/store/migration_105_drop_unused_session_status_values_test.go`'s halt cases) still pass unmodified.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- **Live dogfeed note for this task's own validation checkpoint** (per `EXECUTION-PROCESS.md`'s "actually exercise the feature" requirement, not just green tests): against a real deployed instance, trigger a real `halt_session` reflex (e.g. temporarily seed or lower the threshold on one of the three existing `halt_session` seeds — `drift_detector_echo`, `task_complete_self_terminate`, `task_timeout`, per `internal/agent/reflexes/seeds.go`) on a real session and confirm no LLM call is logged/billed for that turn. Restore whatever you changed to trigger it afterward.

## Work log

<!-- Worker fills in as it goes. -->

## Review notes

<!-- Reviewer fills in. -->
