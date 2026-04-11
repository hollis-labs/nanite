# [Medium] `internal/chat/` has become a utility-module grab-bag — the actual chat engine lives in `internal/service/`

**Scope:** chat-engine
**Topic:** Package layout / Idiomatic Go / Scope-mismatch observation
**Date:** 2026-04-10

## Problem

The chat-engine audit scope was described as `internal/chat/` plus sub-packages. On inspection, `internal/chat/` is a bag of DTOs, parse helpers, and formatting utilities (~2,500 lines across 21 files). The actual chat orchestration — the generate loop, tool execution, delegation, provider calls, plugin hook wiring — lives in `internal/service/` (`chat_generate.go` at 1161 lines, `chat_tool_executor.go` at 534 lines, `delegation.go` at 340 lines, `chat.go` at 366 lines). The decomposition described in the reviewer-backend context ("`internal/chat/engine.go` — was decomposed from 1760→197 lines. Now a thin orchestrator") is accurate as a line count but misleading as a package structure: the orchestrator was extracted to a **different package**, leaving `internal/chat/engine.go` as a 197-line file of helper utilities that are no longer an "engine" at all.

This is a scope-mismatch observation, not a severity judgment about delivery timing. It has concrete technical consequences for how a reader navigates the code and for what tests cover.

## Evidence

`internal/chat/engine.go` is 197 lines of:
- `AgentConstraints` struct + parse helper (L12-L33)
- `BuildToolCatalog` string formatter (L37-L51)
- `ToolWarningPayload` struct (L54-L60)
- `StreamEvent`, `Usage`, `PresenceEvent` DTOs (L63-L95)
- `IsCLIProvider` / `IsPTYProvider` / `InferProvider` name-matching helpers (L99-L125)
- `TruncateStr` byte-slice truncation (L128-L133)
- `intentStopWords` table + `ExtractIntent` keyword extractor (L136-L197)

There is no `type Engine struct`. No `NewEngine()`. No generate loop. The file's comment at the top could reasonably say "misc chat utilities" — nothing in it justifies the name `engine.go`.

The actual chat engine lives in `internal/service/`:

```
$ wc -l internal/service/chat*.go internal/service/delegation.go internal/service/stream.go
    1161 internal/service/chat_generate.go
     316 internal/service/chat_loop_state_test.go
     295 internal/service/chat_loop_state.go
     440 internal/service/chat_test.go
     534 internal/service/chat_tool_executor.go
     366 internal/service/chat.go
     340 internal/service/delegation.go
     193 internal/service/stream.go
```

`ChatService` is defined in `internal/service/chat.go:L21-L51`:

```go
type ChatService interface {
    HandleMessage(ctx context.Context, sessionID, content string) (messageID string, err error)
    RetryLastMessage(ctx context.Context, sessionID string) (messageID string, err error)
    SendAgentMessage(ctx context.Context, fromSessionID, toSessionID, content string) (messageID string, err error)
    ...
}
```

And the implementation `chatServiceImpl` has 20+ methods split across `chat.go`, `chat_generate.go`, `chat_tool_executor.go`, and `delegation.go`. The orchestration loop is `generateResponse` in `chat_generate.go:L46-L763` — 700+ lines of direct orchestration logic.

### Cross-package coupling

`internal/service` imports `internal/chat` for the DTOs and helpers. `internal/chat` does not import `internal/service` (to avoid a cycle). This means:
- Every change in `internal/chat`'s DTO shapes has to be coordinated with consumers in `internal/service`.
- Tests for the actual chat loop (`chat_test.go`, `chat_loop_state_test.go`) live in `internal/service`. Tests for the helpers (`engine_test.go`, `commands_test.go`, etc.) live in `internal/chat`. They can't share test fixtures cleanly.
- The file name `internal/chat/engine.go` suggests something that isn't there. A new contributor looking for "the chat engine" will land on the wrong file first.

### Stuttering / misleading naming

Per the Go community idiom checklist, `chat.ChatService` stutters — the package is `service`, not `chat`, so `service.ChatService` reads fine, but `chat.ChatError`, `chat.ChatMessage`, `chat.ChatRequest` at `internal/chat/errors.go:L22`, elsewhere, are in the `chat` package. Go style is `chat.Error`, `chat.Message`, `chat.Request`. Minor but pervasive.

### Circular-ish dependency dance

`internal/service/chat.go:L294-L298` contains:

```go
// SetWorkers injects the worker manager after construction to break the
// circular dependency (ChatService <-> WorkerManager).
func (s *chatServiceImpl) SetWorkers(w *worker.Manager) {
    s.workers = w
}
```

A post-construction injector to work around a cycle. This is a code smell — it's what happens when responsibilities are split across packages without a clean dependency direction. The cycle is `ChatService → workers.Manager → ChatService` (presumably the worker spawns chat sessions, which it has to do via the chat service). A clean fix is to extract a smaller "session generator" interface that both the worker and chat service consume, but that's a refactor.

## Impact

- **Navigability.** A reviewer or contributor searching for chat-engine code has to know the right magic — the file named `engine.go` is not the engine, and the package named `chat` is the DTO package. This audit itself spent time discovering the layout.
- **Scope miscommunication.** The reviewer-backend context says "`internal/chat/engine.go` — was decomposed from 1760→197 lines. Now a thin orchestrator." The "orchestrator" phrasing is not accurate: what got extracted lives in a different package; what's left is not an orchestrator, it's helpers.
- **Test coverage gaps.** Tests for `chat.EstimateTokens` (`internal/chat/context_client_test.go`) don't exercise the unified `EnforceTokenBudget` under adversarial input — I found the test file covers the straightforward paths but no fuzz, no over-budget property tests, no tool-reduction assertions at the boundary. This is a natural consequence of the package split: the test file in the owning package only sees half the story.
- **Changes touch multiple packages.** Adding a field to `StreamEvent` requires touching `internal/chat/engine.go` and every consumer in `internal/service`. Refactoring the generate loop requires rebuilding the dependencies. The pre-split monolith had the opposite problem; the post-split state has this one.
- **Scope-mismatch framing:** not a time/effort estimate, but a technical observation that the "chat engine" as a concept no longer maps cleanly to a single package, directory, or file. The reviewer spent significant context window mapping it. A future contributor will too.

Severity Medium: the code works, tests pass (assumed — not run per scoped-review rule), no user-visible bug. But the layout has drifted from the naming, and that compounds over time.

## Recommendation

Two paths, both small:

**Option A (light-touch):** Rename `internal/chat/engine.go` to `internal/chat/types.go` or split into `internal/chat/events.go` (StreamEvent, PresenceEvent, Usage), `internal/chat/constraints.go` (AgentConstraints, ParseAgentConstraints), `internal/chat/provider_inference.go` (IsCLIProvider / IsPTYProvider / InferProvider), and `internal/chat/intent.go` (ExtractIntent + stop words). Update the reviewer-backend context to say "chat orchestration lives in `internal/service/chat*.go`; `internal/chat/` holds types and helpers". The `engine_test.go` file should be renamed accordingly.

**Option B (architectural):** Move the DTO types into a `pkg/chatapi/` or keep them in `internal/chat/` but move the utility functions (`TruncateStr`, `ExtractIntent`, `BuildToolCatalog`) into a separate helpers package. Then move `internal/service/chat*.go` **into** `internal/chat/` under new file names, and have a cleaner single-package chat engine. This is more churn but gives a single navigable subsystem.

**Recommended:** Option A, now. It's a rename pass with no behavioral change. The reader-confusion problem is real; the filename should not lie about what's inside.

Either way, update the reviewer-backend context file to reflect the actual layout so future audits don't spend the same discovery effort.

## References

- `internal/chat/engine.go` — helpers, not an engine
- `internal/service/chat.go:L21-L51` — `ChatService` interface
- `internal/service/chat_generate.go:L46-L763` — the real orchestration loop
- `internal/service/chat.go:L294-L298` — the `SetWorkers` post-construction injector (dependency-cycle smell)
- `.nanite/agents/reviewer-backend.md:L66-L67` — the misleading "thin orchestrator" framing this finding corrects

Scope-mismatch note (per the refined deep-review skill's "out of scope for findings" rule): this finding does **not** judge release timing. It's a technical observation about package layout and naming, framed as "the scope label maps to a split codebase". The mismatch is between the reviewer-backend context's description and the actual code layout, not between the work and any deadline.
