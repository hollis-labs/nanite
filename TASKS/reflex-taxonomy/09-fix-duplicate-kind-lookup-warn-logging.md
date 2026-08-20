# Fix duplicate WARN logging on a dispatch_to_agent action-kind lookup failure

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`), fix-as-new-worker-task per `EXECUTION-PROCESS.md`'s review discipline
**Status:** implemented
**Depends on:** `03-shared-decision-engine.md`, `08-fix-resolve-fail-open-visibility.md` (removes logging this task's `08` added, at two call sites)
**Touches:** `internal/service/chat_reflex_dispatch.go` (`attemptReflexDispatch`, ~line 254-258), `internal/mcp/self_tools_dispatch.go` (`matchDispatchToAgentReflex`, ~line 390-396).

## Context

Found by GitHub Copilot's automated review of PR #263 (the Reflex Action Taxonomy batch), 2026-08-20 — two identical inline comments, both correct, verified against the real code by the Orchestrator before dispatching this fix.

Both `attemptReflexDispatch` and `matchDispatchToAgentReflex` resolve `dispatch_to_agent`'s `reflex_action_kinds` row once per call via `store.GetReflexActionKind`, then wrap the already-fetched `(dispatchKind, kindErr)` pair in a `kindLookup` closure passed to `reflexes.Resolve()`:

```go
dispatchKind, kindErr := s.reflexEngine.Store.GetReflexActionKind(ctx, store.ReflexActionDispatchToAgent)
if kindErr != nil {
    slog.Warn("chat-service: dispatch-reflex action-kind lookup failed",
        "session_id", sessionID, "err", kindErr)
}
kindLookup := func(_ context.Context, _ string) (*store.ReflexActionKind, error) {
    return dispatchKind, kindErr
}
```

(`self_tools_dispatch.go`'s shape is identical, minus `session_id` in its own log line.)

When `kindErr != nil`, this call-site log fires. Then `Resolve()` (`internal/agent/reflexes/resolve.go`, its per-kind lookup loop added by `08-fix-resolve-fail-open-visibility.md`) calls `kindLookup(ctx, kind)`, gets back the same non-nil error, and **also** logs:

```go
slog.Warn("reflexes.Resolve: action-kind lookup failed, defaulting combining_algorithm to all_applicable",
    "session_id", state.SessionID, "action_kind", kind, "err", err)
```

Two WARN lines for one underlying failure. `Resolve()`'s own log line is strictly a superset of the call-site log's information (`session_id` + `action_kind` + `err`, vs. the call-site's `session_id`/`err` only at `chat_reflex_dispatch.go`, or `err` only at `self_tools_dispatch.go` — neither includes `action_kind` today). This is real, if minor, log noise: a single degraded-lookup event currently looks like two independent failures to anyone reading logs or an alerting rule keyed on this message.

## What to do

1. **`internal/service/chat_reflex_dispatch.go`** (~line 254-258): remove the `if kindErr != nil { slog.Warn(...) }` block immediately after the `GetReflexActionKind` call. Keep the call itself and the `kindLookup` closure exactly as-is — only the redundant log line goes.
2. **`internal/mcp/self_tools_dispatch.go`** (~line 390-396): same fix — remove the `else { slog.Warn("mcp: dispatch-reflex action-kind lookup failed", "err", kindErr) }` branch. Keep the `if kindErr == nil { dispatchKindDefaultSeconds = ... }` logic itself unchanged (that branch does real work, not just logging — don't touch it).
3. Do not touch `Resolve()`'s own logging (`08`'s addition) — it's the one that stays, since it's the single point every caller (including `Engine.EvaluateState`, which has no call-site-level log of its own for this exact case today) funnels through.
4. Confirm no other real signal is lost: grep both files for any other place that reads `kindErr` for something other than the removed log line, to make sure the variable isn't now unused (it still feeds `kindLookup`'s closure and, in `self_tools_dispatch.go`, the `if kindErr == nil` branch — should still compile fine, but verify).

## Done means

- Both redundant WARN log lines are removed; `Resolve()`'s single log line (from `08`) is the only WARN emitted for a `GetReflexActionKind` failure at either call site.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass (the two pre-existing `internal/service/container.go` go vet findings are known-unrelated).
- If either file has an existing test asserting on the now-removed log line's exact message (check `chat_reflex_dispatch_multi_candidate_canary_test.go`, `self_tools_dispatch_multi_candidate_canary_test.go`, and any other cooldown/audit test touching this code path), update it to assert on `Resolve()`'s log line instead, or drop the assertion if it was only ever checking for "a warning was logged" generically and `Resolve()`'s own line already satisfies that. Don't leave a test silently checking nothing.
- No other behavior change — this is a pure logging deduplication, not a logic change.

## Work log

Verified branch (`reflex-action-taxonomy`, already checked out) and re-read both call sites against the task's line-number pointers before editing — both matched the task's excerpts exactly.

1. `internal/service/chat_reflex_dispatch.go` (~line 254-258 pre-edit): removed the
   `if kindErr != nil { slog.Warn("chat-service: dispatch-reflex action-kind lookup failed", "session_id", sessionID, "err", kindErr) }`
   block immediately after the `GetReflexActionKind` call. Left the call and the
   `kindLookup` closure untouched. `kindErr` is still consumed by the closure's
   `return dispatchKind, kindErr`, so no unused-variable issue.

2. `internal/mcp/self_tools_dispatch.go` (~line 390-396 pre-edit): removed the
   `else { slog.Warn("mcp: dispatch-reflex action-kind lookup failed", "err", kindErr) }`
   branch, leaving `if kindErr == nil { dispatchKindDefaultSeconds = dispatchKind.DefaultRecurrenceSeconds }`
   as a plain `if` with no `else`. `kindErr` is still consumed by both that `if`
   condition and the `kindLookup` closure further down, so no unused-variable
   issue.

3. Did not touch `internal/agent/reflexes/resolve.go` — its `08`-added
   `slog.Warn("reflexes.Resolve: action-kind lookup failed, defaulting
   combining_algorithm to all_applicable", "session_id", ..., "action_kind", ...,
   "err", ...)` line is unchanged and is now the sole WARN emitted for a
   `GetReflexActionKind` failure reached through either call site.

4. Grepped both files' own tests plus every other reflex/cooldown/audit test in
   `internal/service` and `internal/mcp` for the removed message text
   (`dispatch-reflex action-kind lookup failed`, and the broader
   `action-kind lookup failed` / `action kind lookup failed` substring) and for
   any `kindErr` reference outside the two edited call sites. No hits anywhere
   except `resolve.go` itself (the line this task deliberately preserves).
   `chat_reflex_dispatch_multi_candidate_canary_test.go` and
   `self_tools_dispatch_multi_candidate_canary_test.go` only assert generically
   on `level=WARN` plus the unrelated "resolved multiple candidates" canary
   message — both still pass unmodified since `resolve.go`'s own WARN lines
   (multi-candidate canary and, when triggered, the kind-lookup-failure line)
   remain untouched. No test file needed updating.

Ran `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` after the edits.
Build: ok. Vet: the two known-unrelated pre-existing `internal/service/container.go`
findings (`stopReaper`/`stopRuntimeReaper` possible context leak) are still the
only findings — no new ones. Test: full suite green, all packages `ok` (or
cached-`ok` from before this change, re-verified as pass after this diff by the
`go test ./...` run above).

No other behavior change — pure logging deduplication as scoped, matching the
task's exact excerpt down to the line contents.

## Review notes

<!-- Not re-reviewed by a fresh Reviewer — small, mechanical, PR-review-driven fix; the Orchestrator verified the finding directly against real code before dispatching, and will re-verify the diff directly before pushing. -->
