# [Low] Grouped observations — small quality and correctness items

**Scope:** chat-engine
**Topic:** Mixed (Idioms / Error handling / Test quality / Antipatterns)
**Date:** 2026-04-10

Grouped low-severity items noticed during the audit. Each is small on its own; collectively they suggest places where a linting-and-polish pass would help.

## L1 — Byte-slice truncation on UTF-8 strings

**Problem.** Multiple truncation sites slice strings by byte index, not rune index. A multi-byte UTF-8 character split mid-sequence produces invalid UTF-8.

**Evidence.**
```go
// internal/chat/engine.go:L128-L133
func TruncateStr(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."   // ← byte slice
}
```

```go
// internal/chat/engine.go:L45-L47
desc := s.Description
if len(desc) > 80 {
    desc = desc[:80] + "..."  // ← byte slice in BuildToolCatalog
}
```

```go
// internal/service/delegation.go:L231
Title: "Orchestration: " + userMessage[:min(60, len(userMessage))],  // ← byte slice
```

```go
// internal/service/chat_generate.go:L421-L424
if len(warningError) > 300 {
    warningError = warningError[:300] + "..."  // ← byte slice
}
```

```go
// internal/service/chat_generate.go:L444-L447
if len(summary) > 500 {
    summary = summary[:500] + "... (truncated)"  // ← byte slice
}
```

```go
// internal/service/chat_generate.go:L751-L754
if len(contentPreview) > 500 {
    contentPreview = contentPreview[:500]  // ← byte slice
}
```

**Recommendation.** Centralize truncation in a `chat.Truncate(s, maxRunes)` helper that uses `[]rune` conversion for rune-safe truncation. Replace all byte-slice sites. The performance cost is negligible for these short truncations.

---

## L2 — `rand.Intn` without seeding

**Problem.** Error giphy query selection uses `math/rand` without seeding the default source. Not a security issue (the pool is fixed and deterministic selection is fine here), but it's a common reviewer trip-wire, and in Go 1.20+ the default source is auto-seeded — so this is fine on modern Go but confusing to reason about.

**Evidence.**
```go
// internal/chat/errors.go:L86-L93
func buildErrorEnvelope(code ErrorCode, message string, details map[string]interface{}) string {
    data := errorEnvelopeData{
        ...
        GiphyQuery: errorGiphyQueries[rand.Intn(len(errorGiphyQueries))],
        ...
```

**Recommendation.** If Go 1.22+ is the minimum (`.nanite/agents/reviewer-backend.md:L17` says "Go 1.26.1"), switch to `math/rand/v2` which has explicit semantics and is the modern idiom. Also consider a per-process `rand.NewSource(time.Now().UnixNano())` if reproducibility is undesired. Info-level refinement.

---

## L3 — `activity.go` payload construction with `fmt.Sprintf` instead of `json.Marshal`

**Problem.** `ActivityEmitter` builds payload JSON via `fmt.Sprintf(\`{"...":%q,...}\`, ...)` with the `%q` verb. This works for single-line strings but produces invalid JSON if the input contains characters that Go's `%q` escapes but JSON doesn't understand the same way — or vice versa. For example, `%q` uses `\u0026` for `&` in some cases, which is valid Go but non-standard JSON.

**Evidence.**
```go
// internal/chat/activity.go — multiple sites, e.g. L143-L149
e.Emit(ctx, activityEvent{
    EventType: EventAgentAssigned,
    ...
    Payload:   fmt.Sprintf(`{"agent_id":%q,"mode":%q}`, agentID, mode),
})
```

All 10+ Emit* helpers use the same pattern. The `Payload` is then serialized again as a JSON string when the whole `activityEvent` is marshalled, so the outer marshaller re-escapes the inner payload. In practice the `%q` round-trip happens to work for simple ASCII inputs but is not robust.

**Recommendation.** Build payloads via `json.Marshal(map[string]any{...})` and embed the result:

```go
payload, _ := json.Marshal(map[string]any{"agent_id": agentID, "mode": mode})
e.Emit(ctx, activityEvent{
    ...
    Payload: string(payload),
})
```

Cheaper to audit, doesn't depend on `%q` == JSON-string equivalence.

---

## L4 — `isProcessDone` string-matches error messages

**Problem.** `ProcessTracker.isProcessDone` detects "process already exited" errors by string comparison against Go runtime error messages. Any change in Go's error strings (they're not stable API) breaks the detection silently.

**Evidence.**
```go
// internal/chat/proctrack.go:L229-L238
func isProcessDone(err error) bool {
    if err == nil {
        return false
    }
    msg := err.Error()
    return msg == "os: process already finished" ||
        msg == "os: process already released" ||
        msg == "wait: no child processes"
}
```

**Recommendation.** Use `errors.Is(err, os.ErrProcessDone)` if available (Go 1.21+ exports `os.ErrProcessDone`). Otherwise use type assertions against `*os.SyscallError` and `syscall.ECHILD`. The stdlib error messages are not API.

---

## L5 — `KillStale` has an unlock/re-lock TOCTOU pattern

**Problem.** `ProcessTracker.KillStale` collects stale entries under the mutex, unlocks, and then iterates killing them. Inside the iteration it calls `Untrack(entry.sessionID, entry.tp.process)` which re-locks. Between the unlock and the per-entry re-lock, another goroutine can `Untrack` the same process, producing a duplicate-kill attempt or a missed cleanup. Not catastrophic because `proc.Kill()` is idempotent for already-exited processes (handled by `isProcessDone`), but it's a pattern that looks racy even if it isn't currently exploitable.

**Evidence.**
```go
// internal/chat/proctrack.go:L193-L227
func (pt *ProcessTracker) KillStale(staleThreshold time.Duration) int {
    pt.mu.Lock()
    ...
    var stale []staleEntry
    for sessionID, procs := range pt.processes {
        for _, tp := range procs {
            if now.Sub(tp.lastActivity) > staleThreshold {
                stale = append(stale, staleEntry{sessionID, tp})
            }
        }
    }
    pt.mu.Unlock()   // ← unlock here

    killed := 0
    for _, entry := range stale {
        if err := entry.tp.process.Kill(); err != nil {
            ...
        }
        pt.Untrack(entry.sessionID, entry.tp.process)   // ← re-lock inside Untrack
    }
    return killed
}
```

**Recommendation.** Either hold the lock for the duration (acceptable if kill is rare) or batch the Untrack work at the end. Cosmetic for now; flag for `-race` sweep.

---

## L6 — `detectStuckLoop` mutates maps passed by reference without documenting ownership

**Problem.** `detectStuckLoop` takes `lastResults`, `repeatCount`, `blocked` maps as parameters and mutates them. The caller (`postProcessToolResults`) passes the loopState's fields directly. The intent is clear to someone who reads both sides, but the function signature is silent about ownership. A future refactor that tries to parallelize post-processing will step on the shared state without warning.

**Evidence.**
```go
// internal/service/chat_generate.go:L831-L860
func (s *chatServiceImpl) detectStuckLoop(
    toolName, resultText string,
    lastResults map[string]string,
    repeatCount map[string]int,
    blocked map[string]bool,
) string {
    if prev, ok := lastResults[toolName]; ok && prev == resultText {
        repeatCount[toolName]++
        ...
        blocked[toolName] = true
```

**Recommendation.** Make it a method on `*loopState` so the ownership is explicit: `func (ls *loopState) detectStuckLoop(toolName, resultText string) string`. The current free-function-with-reference-params form is a C-style pattern that doesn't carry its invariants in the signature.

---

## L7 — Tests use sleep-based synchronization in places

**Observation.** `TestEstimateTokens` and related tests look clean. But I didn't run `grep -rn "time.Sleep" internal/chat/.*_test.go internal/service/chat.*_test.go` because the scoped-review rule defers tooling. The 1-second `time.Sleep(1 * time.Second)` at `chat_generate.go:L559` inside the iteration loop ("Brief pause between iterations") is production code, not test code, but the "tests are deterministic" check should be verified in the follow-up tooling scope. Flag as a thing to grep for.

**Evidence.**
```go
// internal/service/chat_generate.go:L558-L560
// Brief pause between iterations.
if ls.iteration > 0 {
    time.Sleep(1 * time.Second)
}
```

**Impact.** The production sleep adds 1 second per tool iteration, not for any clear reason documented in the code. A 6-iteration tool loop waits 5 seconds for no functional reason. Either remove the sleep or document why it's there (rate-limit avoidance? circuit-breaker settle time? UX throttle?).

**Recommendation.** Remove the sleep or replace with an adaptive backoff tied to rate-limiter state. If it's UX throttle (to avoid flickering), move it to the frontend. Low-severity because functional but wasteful.

---

## L8 — `log.Printf` everywhere instead of structured logging

**Observation.** The chat engine uses `log.Printf` with prefixes like `"chat-service: ..."`, `"broker: ..."`, `"delegation: ..."`. The project's stack snapshot mentions `hollis-labs/otel` for tracing but the logging is still stdlib `log`. Tracing spans are emitted (`feotel.StartSpan`) but logs are unstructured and not correlated with spans via IDs.

**Evidence.** Ubiquitous throughout `internal/service/chat_*.go` and `internal/chat/*.go`.

**Recommendation.** This is a cross-cutting refactor, not a chat-engine change. Flag for a broader logging-and-observability scope. Low-priority given "first beta for developer friends" release context — developer-friend logs are probably fine. But worth noting for post-beta polish.

---

## L9 — `isWorkTool` hardcoded tool name list

**Problem.** `isWorkTool` hardcodes a set of tool names to trigger a `work_changed` presence event. Any future agent tool that mutates work state has to be added here manually.

**Evidence.**
```go
// internal/service/chat_tool_executor.go:L485-L491
func isWorkTool(name string) bool {
    switch name {
    case "nanite_todo_create", "nanite_todo_update", "nanite_plan_create", "nanite_plan_update":
        return true
    }
    return false
}
```

**Recommendation.** Move to the tool metadata registry — each tool declares its own `emits_work_changed` flag. Then `executeSingleTool` reads the flag. Cosmetic; low-priority.

---

## L10 — `matchesAny` is O(n·m) on every turn

**Observation.** `classifyContextIntent` calls `matchesAny` which lowercases the (already-truncated-to-500-chars) input once per call inside the inner loop:

```go
// internal/chat/context_client.go:L378-L387
func matchesAny(text string, subs ...string) bool {
    lower := strings.ToLower(text)
    for _, sub := range subs {
        if strings.Contains(lower, sub) {
            return true
        }
    }
    return false
}
```

This is called 7 times from `classifyContextIntent` with different substring sets — each call re-lowercases the same 500-char string. Micro-optimization, not a correctness issue.

**Recommendation.** Lowercase once in `classifyContextIntent` and pass the lowercased form to `matchesAny`. Trivial.

---

## Impact (group)

None of the items above is a blocker. They're quality items that tend to grow into bigger problems if unaddressed — non-UTF-8-safe truncation eventually produces bad logs, `%q`-built JSON eventually mis-escapes a user input with a specific shape, hardcoded string matches on stdlib errors eventually break when a Go version bumps. A ~1-hour polish pass would clear most of them.

## References

- Individual evidence citations in each item above.
- `chat-engine-tooling-and-tests` recommended follow-up audit — this is where `errcheck`, `staticcheck`, and `golangci-lint` would surface several of these automatically, plus items I didn't hand-grep for.
