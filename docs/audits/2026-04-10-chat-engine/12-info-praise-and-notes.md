# [Info] Praise, patterns worth preserving, and design observations

**Scope:** chat-engine
**Topic:** Info / Praise / Design notes
**Date:** 2026-04-10

Things the chat engine does well that shouldn't regress, plus a few design observations for future reference.

## P1 — `loopState` and `ContinueSite` tracking is well-designed

`internal/service/chat_loop_state.go` consolidates all mutable state for the generate loop into one struct, with explicit typed constants for continuation reasons (`ContinueToolResults`, `ContinueCompaction`, `ContinueRecovery`, etc.). The `continueWith` / `captureSnapshot` pattern gives a clean instrumentation point for debugging tool loops without scattering log statements.

**Evidence.**
```go
// internal/service/chat_loop_state.go:L11-L22
type ContinueSite string

const (
    ContinueToolResults  ContinueSite = "CONTINUE_TOOL_RESULTS"
    ContinueCompaction   ContinueSite = "CONTINUE_COMPACTION"
    ContinueRecovery     ContinueSite = "CONTINUE_RECOVERY"
    ContinuePermission   ContinueSite = "CONTINUE_PERMISSION"
    ContinueAgentReturn  ContinueSite = "CONTINUE_AGENT_RETURN"
    ContinueHookModified ContinueSite = "CONTINUE_HOOK_MODIFIED"
    ContinueModeChange   ContinueSite = "CONTINUE_MODE_CHANGE"
)
```

The layered `shouldStop` check at L180-L208 is also clean — five explicit layers (consecutive failures, idle timeout, max turns, hard ceiling, retry budget) with clear reasons, each returning a structured `(stop bool, reason string)` pair. This is exactly the shape a loop guard should have.

**Preserve:** The named constants, the layered stop check, and the `TurnSnapshot` debug capture. These are the kind of structured introspection that pays off during incident response.

---

## P2 — The `preCheckTools` → `executeToolBatch` → `postProcessToolResults` split

The tool execution path in `chat_tool_executor.go` cleanly separates permission/concurrency-safety checks (`preCheckTools`) from execution (`executeToolBatch`) from post-processing (`postProcessToolResults`). The `toolPlan` struct records the pre-check decision so the executor doesn't re-check. The executor knows nothing about permissions or stuck-loop detection. Each stage has a single responsibility.

**Evidence.** `internal/service/chat_tool_executor.go:L67-L221` (preCheck), L223-L276 (executeBatch), L378-L480 (postProcess).

**Preserve:** The three-phase split and the `toolPlan.status` state machine (`ready` / `blocked` / `denied` / `meta`). Future changes (e.g., adding rate-limiting per tool, tool-level circuit breakers) should add a new phase or a new status value rather than inlining checks into the executor.

---

## P3 — `EnforceTokenBudget` reduction cascade

`internal/chat/context_client.go:L223-L301` implements a four-step reduction cascade for token budget enforcement: prune old tool results → reduce tool count → drop oldest messages → refuse. Each step returns a `TokenBreakdown` so callers get visibility into what was reduced. This is a clean, debuggable shape for a traditionally hairy problem.

**Evidence.**
```go
// internal/chat/context_client.go:L223-L228
func EnforceTokenBudget(
    systemPrompt string,
    messages []provider.ChatMessage,
    tools []provider.ToolDefinition,
    ceilingOverride int,
) ([]provider.ChatMessage, []provider.ToolDefinition, *TokenBreakdown, error) {
```

The function returns (possibly modified) messages, tools, a breakdown struct, and an error. Callers can log the breakdown, expose it to the user, and decide whether to retry with a tighter ceiling. The token estimation is approximate (`chars/4`), which is fine — it's a budget guardrail, not a billing line.

**Preserve:** The cascade structure, the per-step logging, and the refusal-as-last-resort semantics. This is the right shape for a safety gate.

**Note:** The `chars/4` estimation is documented as a rough heuristic. For future accuracy, consider optionally plugging in provider-specific token counters — but don't make that mandatory; the current approximation is correct enough and has no latency cost.

---

## P4 — Test harness uses `t.TempDir()` and real SQLite

`internal/chat/context_client_test.go:L13-L22` constructs a test store from `t.TempDir()` and a fresh `store.New(dbPath)`. No shared DB, no in-memory mock substituting for real SQL semantics. This is the right trade-off for store-adjacent tests — you catch real SQL errors at test time.

**Preserve:** The `newTestBroker` pattern. It's short enough that replicating it across other test files is easy. When adding new tests, reach for this pattern rather than introducing a mock `Store` interface.

---

## P5 — `StreamEvent` / `PresenceEvent` DTOs are flat and well-typed

`internal/chat/engine.go:L62-L95` defines `StreamEvent` and `PresenceEvent` as flat structs with JSON tags and clear omitempty markers. No nested `any` fields, no `map[string]interface{}` payload bags (except for `Data` in `StreamEvent` which is documented as intentionally JSON-encoded for tool warnings). The SSE protocol is discoverable from the struct definition alone.

**Preserve:** The flat-struct discipline. When adding new event types, resist the urge to pack them into an existing field — add a new typed field with JSON tags. The cost is a slightly bigger struct; the benefit is a discoverable API.

---

## P6 — `NewChatService(cfg)` config-struct constructor

`internal/service/chat.go:L109-L138` uses a config struct for dependencies rather than positional arguments. Adding a new dependency (e.g., `Permissions *permission.Engine` at L77) doesn't break existing callers. The config struct also doubles as documentation of what the chat service needs.

**Preserve:** The config-struct pattern for constructors with more than ~3 dependencies. Idiomatic Go, easy to extend.

---

## D1 — Design observation: the activity emitter is decoupled but unused in hot paths

The `ActivityEmitter` in `internal/chat/activity.go` has a rich set of typed Emit* helpers (EmitSessionCreated, EmitAgentAssigned, EmitRateLimitHit, EmitCircuitBreakerTripped, etc.) that POST to an external Engine URL. It's correctly designed as fire-and-forget with graceful degradation when the URL is empty.

**However**, I couldn't find usage of `ActivityEmitter` from the chat service. The chat service uses a different `EventEmitter` interface (`s.events.EmitSessionStart` etc. at `chat_generate.go:L218`). So there are two parallel event-emission paths — one typed-wrapper around direct HTTP calls, one going through a service abstraction. The former may be dead code. A quick `grep -rn "NewActivityEmitter" internal/` would confirm.

**Note, not a finding:** if `ActivityEmitter` is indeed unused from the main chat path, that's dead code worth cleaning up (or a missing integration). I didn't grep for call sites due to scope discipline. Flag for the follow-up tooling scope or for a maintainer to verify in a minute.

---

## D2 — `RegisterEnvelopeType` mutex pattern is correct

`internal/chat/envelope.go:L20-L41` uses a package-level `sync.RWMutex` for the envelope type registry. Writes take the write lock, reads take the read lock. This is correct for a registry that's populated at startup and occasionally extended by plugin load. Worth noting because the plugin audit flagged mutex-discipline issues elsewhere in the plugin host — this site is not affected.

---

## D3 — The `ExtractIntent` stop-word list is fine-grained English

`internal/chat/engine.go:L136-L197` has ~90 English stop words hardcoded. It's not a concern for this audit, but a future i18n pass will need to either parametrize this or accept English-centricity. Flag for future scope.

---

## References

No recommendations — this finding is praise + design notes only. References:
- `internal/service/chat_loop_state.go` (P1)
- `internal/service/chat_tool_executor.go` (P2)
- `internal/chat/context_client.go:L223-L301` (P3)
- `internal/chat/context_client_test.go:L13-L22` (P4)
- `internal/chat/engine.go:L62-L95` (P5)
- `internal/service/chat.go:L109-L138` (P6)
