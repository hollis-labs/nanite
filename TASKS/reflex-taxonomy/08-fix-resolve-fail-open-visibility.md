# Fix `Resolve()`'s silent fail-open on an unresolvable action-kind lookup

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`), fix-as-new-worker-task per `EXECUTION-PROCESS.md`'s review discipline
**Status:** implemented
**Depends on:** `03-shared-decision-engine.md` (fixes code it introduced)
**Touches:** `internal/agent/reflexes/resolve.go` (`Resolve()`'s per-kind combining-algorithm lookup), `internal/agent/reflexes/engine.go` (the `kindLookup` closure `Resolve()` is called with from `EvaluateState`), `internal/service/chat_reflex_dispatch.go` and `internal/mcp/self_tools_dispatch.go` (optional, see item 2 below).

## Context

Found by the fresh Reviewer during Phase 1's (`01`-`04`) review, 2026-08-19/20 — a real, moderate-severity finding, not a hypothetical:

`internal/agent/reflexes/resolve.go`'s `Resolve()` resolves each present `action_kind`'s `combining_algorithm` via a caller-supplied `kindLookup` function:

```go
algo := "all_applicable"
if kindLookup != nil {
    if k, err := kindLookup(ctx, kind); err == nil && k != nil && k.CombiningAlgorithm != "" {
        algo = k.CombiningAlgorithm
    }
}
```

A `kindLookup` failure or an empty `CombiningAlgorithm` is swallowed with **zero logging anywhere in the call chain**. `Resolve()` itself never logs it. `engine.go`'s `kindLookup` closure (around lines 202-209) constructs an error string (`"action kind %q not cached"`) that is never surfaced anywhere — it's simply discarded by `Resolve()`'s `err == nil` check.

The fail-open target is `all_applicable` — which is exactly the behavior `halt_session` (a `deny_overrides` kind) must **not** have. If `Engine`'s in-memory `actionKinds` cache is ever empty or stale after its one-time construction-time `Warn` (a transient DB error during `NewEngine`, a migration that ran partially, a future dev directly editing `reflex_action_kinds`), every subsequent `EvaluateState` pass silently regresses `halt_session` from "single winner this pass, preempts everything else" back to "applies alongside whatever else also fired" — **the exact bug `03`'s `Resolve()` exists to fix, and the batch's own operator-quoted requirement ("deny-overrides actually stop the turn")** — with no per-occurrence signal an operator could ever see in logs.

The two `dispatch_to_agent` call sites (`chat_reflex_dispatch.go`, `self_tools_dispatch.go`) already `Warn`-log their own `GetReflexActionKind` failures before calling `Resolve()` — this gap is specific to `Resolve()`'s internal swallow and to `engine.go`'s cache-lookup closure, which currently have no equivalent.

A closely related, lower-severity finding from the same review, worth folding into this same fix rather than a separate task: in both `chat_reflex_dispatch.go` and `self_tools_dispatch.go`, if the one-off `GetReflexActionKind(dispatch_to_agent)` call fails (already logged there), `Resolve()`'s fail-open selects *every* eligible `dispatch_to_agent` candidate (`all_applicable` default) instead of just the top-priority one — but both call sites only read `resolved.FiredReflexes[0]`/`resolved.Actions[0]`, so any additional selected-and-applied candidates are silently discarded rather than used or logged as discarded. No crash risk, but worth a one-line log-if-more-than-one-selected note while you're already in this code for the primary fix.

## What to do

1. Add a `Warn`-level log call whenever `Resolve()`'s per-kind lookup fails or resolves to an empty `CombiningAlgorithm` — include the action kind name and, if available, the lookup error. Whether this lives inside `Resolve()` itself (it doesn't currently take a logger — check whether adding one is the cleanest shape, or whether logging belongs in each caller's `kindLookup` closure instead, matching how the two dispatch call sites already log their own lookup failures before calling `Resolve()`) is your call — pick whichever keeps `Resolve()`'s signature/testability clean, and document the choice in this file's Work Log. Either way, the fail-open behavior itself (defaulting to `all_applicable`) stays exactly as `03` designed it — this task adds *visibility*, it does not change the fallback's target algorithm.
2. Specifically fix `engine.go`'s `kindLookup` closure so its already-constructed `"action kind %q not cached"` error string actually reaches a log line when `Resolve()` (or the closure itself) hits it — right now it's built and then discarded.
3. (Fold in, same task) In `chat_reflex_dispatch.go` and `self_tools_dispatch.go`, after calling `Resolve()`, if `len(resolved.FiredReflexes) > 1` (or the equivalent "more than the one candidate we actually use" condition), log a `Warn` noting N candidates were selected but only the first is used — this should only ever fire in the already-logged kind-lookup-failure fail-open case, so treat it as a canary for that condition, not a new code path to build defenses around.
4. Do not change the fail-open *target* (still `all_applicable` on lookup failure) — that's `03`'s deliberate, already-reviewed design choice for graceful degradation. This task is purely about making a degraded state observable, not about changing what happens during one.

## Done means

- A kind-lookup failure inside `Resolve()` (simulate via a `kindLookup` that returns an error, or a kind not present in the map) produces a `Warn`-level log line naming the action kind — verified by a test that captures/asserts on log output (or refactors to a small interface that's easy to assert against — your call on the cleanest testable shape).
- `engine.go`'s existing `"action kind %q not cached"` error is no longer silently discarded — it reaches a log line when it occurs.
- The `dispatch_to_agent` multi-selected-but-only-first-used canary log is added at both call sites.
- No change to any existing passing test's behavior/assertions — this is additive logging only, not a logic change. Re-run task `03`'s and `04`'s full test suites (`internal/agent/reflexes/...`, `internal/service/...`, `internal/mcp/...`) and confirm all still pass unmodified.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass (the two pre-existing `internal/service/container.go` go vet findings are known-unrelated; don't try to fix them).

## Work log

**Design call (item 1 — where the logging lives):** logging lives *inside* `Resolve()` itself, via the package-level `log/slog` logger (`slog.Warn(...)`, not an injected `*slog.Logger`). Rejected the alternative (pushing the log into each caller's `kindLookup` closure) because:

- `Resolve()`'s per-kind lookup loop (`resolve.go`) is the one place all three real call sites' `kindLookup` results funnel through — logging there catches `engine.go`'s closure, `chat_reflex_dispatch.go`'s closure, and `self_tools_dispatch.go`'s closure uniformly, with one code path instead of three near-duplicated ones.
- It keeps `Resolve()`'s exported signature completely unchanged (no new logger parameter), so none of the ~7 existing call sites/tests across `resolve_test.go`, `engine.go`, `chat_reflex_dispatch.go`, `self_tools_dispatch.go` needed a signature-shape update — directly satisfying the task's "keeps `Resolve()`'s signature/testability clean" guidance.
- It automatically satisfies item 2 (`engine.go`'s discarded `"action kind %q not cached"` error) for free: that error is exactly the `err` value `Resolve()` receives back from `kindLookup(ctx, kind)` and now logs — no separate log call needed inside `engine.go`'s closure itself. Added a doc comment there instead, explaining why no code changed.
- Using the package-level `slog` (not `e.Logger`/an injected logger) matches the existing sibling pattern at the two `dispatch_to_agent` call sites, which already call package-level `slog.Warn(...)` directly rather than threading a logger through — consistent style for this exact call chain.

**Implementation (item 1 + 2):** `internal/agent/reflexes/resolve.go`'s per-kind `algoByKind` loop now branches on `kindLookup`'s result via a `switch`:
- `err != nil` → `slog.Warn("reflexes.Resolve: action-kind lookup failed, defaulting combining_algorithm to all_applicable", "session_id", state.SessionID, "action_kind", kind, "err", err)`.
- `k == nil || k.CombiningAlgorithm == ""` → `slog.Warn("reflexes.Resolve: action-kind resolved with empty combining_algorithm, defaulting to all_applicable", "session_id", state.SessionID, "action_kind", kind)`.
- Otherwise (unchanged): `algo = k.CombiningAlgorithm`.

Fail-open target is bit-for-bit unchanged — still `all_applicable` in both branches, exactly as `03` designed; only the two new `slog.Warn` calls are additive. `engine.go`'s `kindLookup` closure itself is untouched except for a doc comment explaining the fix now lives one level up in `Resolve()`.

**Implementation (item 3 — multi-candidate canary):** added to both `chat_reflex_dispatch.go`'s `attemptReflexDispatch` and `self_tools_dispatch.go`'s `matchDispatchToAgentReflex`, right after the existing `len(resolved.FiredReflexes) == 0` early-return and before `resolved.FiredReflexes[0]`/`resolved.Actions[0]` are read: `if len(resolved.FiredReflexes) > 1 { slog.Warn("<service: | mcp:> dispatch-reflex resolved multiple candidates, only the first is used", "session_id", sessionID, "candidate_count", len(resolved.FiredReflexes)) }`.

**Testing:**
- `internal/agent/reflexes/resolve_test.go`: added `captureSlogDefault(t)` (swaps `slog.Default()` for a `bytes.Buffer`-backed text handler for the test's duration, restored via `t.Cleanup`) plus four new tests — `TestResolve_KindLookupFailure_LogsWarning`, `TestResolve_EmptyCombiningAlgorithm_LogsWarning` (the two positive Done-means assertions), and `TestResolve_KindLookupSuccess_NoWarningLogged` (negative-space check: a clean lookup produces zero Warn noise). All assert on the captured log text (`level=WARN`, the action-kind name, and — for the lookup-error case — the underlying error text) while also re-asserting the fail-open behavior itself (`Actions` count) is unchanged. The pre-existing `TestResolve_KindLookupFailure_DefaultsToAllApplicable` was left untouched — it now incidentally also emits a Warn line (visible in `go test -v` output, going to the real default stderr logger since that test doesn't swap it), but its own assertions are unmodified and it still passes.
- Added one integration-level test per `dispatch_to_agent` call site — beyond what Done-means strictly required (only `Resolve()`'s own log line was named as needing a test) — because both call sites' canary log was easy to exercise against a real `*store.Store` and worth the extra confidence: `internal/service/chat_reflex_dispatch_multi_candidate_canary_test.go` (`TestAttemptReflexDispatch_KindLookupDegraded_LogsMultiCandidateCanary`) and `internal/mcp/self_tools_dispatch_multi_candidate_canary_test.go` (`TestMatchDispatchToAgentReflex_KindLookupDegraded_LogsMultiCandidateCanary`). Both insert two real `dispatch_to_agent` `agent_reflexes` rows sharing one always-matching `user_regex_window` trigger, then remove the `dispatch_to_agent` `reflex_action_kinds` row (via a `PRAGMA foreign_keys = OFF`-bracketed `DELETE`, since a real FK from `agent_reflexes.action_kind` blocks a plain delete while referencing rows exist) to force the same kind-lookup-failure fail-open at the real call site, and assert the canary Warn line (`"resolved multiple candidates"`, `candidate_count=2`) appears.
  - Deviation from an initial approach worth recording: first tried simulating the "empty `CombiningAlgorithm`" variant (matching `resolve_test.go`'s second test) via `UPDATE reflex_action_kinds SET combining_algorithm = ''`, but `reflex_action_kinds.combining_algorithm` carries a real `CHECK (... IN ('deny_overrides','first_applicable','all_applicable'))` constraint — an empty value is not a reachable real-DB state. Switched to removing the row entirely (the "lookup fails" variant) for these two integration tests instead; the empty-string variant stays covered only at the `Resolve()` primitive level, where any `ActionKindLookup` a caller supplies is fair game regardless of what a real schema would allow.

**Verification:**
- `go build ./cmd/nanite/` — ok.
- `go vet ./...` — the two pre-existing `internal/service/container.go` findings (`stopReaper`/`stopRuntimeReaper` "not used on all paths") are the only output, exactly as the task file flagged as known-unrelated; not touched.
- `go test ./internal/agent/reflexes/... ./internal/service/... ./internal/mcp/...` — all pass (`ok`), including every pre-existing test in those three packages unmodified.
- `go test ./...` — full repo suite, all 88 packages `ok`, zero `FAIL`.
- `git status --short` after all verification — only this task's own new/modified files plus the batch's other already-in-flight, pre-existing uncommitted changes (tasks `01`-`05`); no stray writes to any tracked file outside this task's scope. All verification ran through `go test` against `t.TempDir()`-rooted `*store.Store` instances (the existing `newTestStore`/inline `store.New(..., filepath.Join(t.TempDir(), ...))` patterns already used by sibling tests in these packages) — no live-server dogfeed step was needed for this task, so the relative-path/managed-agent-write footgun noted elsewhere in `TASKS/ESCALATIONS.md` doesn't apply here.

## Review notes

<!-- Reviewer fills in. -->
