# [Critical] Service layer chat_generate.go / chat_tool_executor.go have zero test coverage

**Scope:** internal/service/
**Topic:** Test Quality — missing coverage on critical orchestration paths
**Date:** 2026-04-11

## Problem

The two largest files in the service layer — `chat_generate.go` (1161 lines) and `chat_tool_executor.go` (534 lines) — have no test files. These files contain the primary code paths through which every chat message, tool execution, and streaming response flows. Together they are 1695 lines of untested orchestration code.

## Evidence

Untested source files in `internal/service/` with no matching `*_test.go`:

| File | Lines | Responsibility |
|------|-------|----------------|
| `internal/service/chat_generate.go` | 1161 | `generateResponse` loop, `captureEnvelopeData` goroutine, provider callback wiring, streaming orchestration |
| `internal/service/chat_tool_executor.go` | 534 | Tool execution dispatch, permission checking integration, tool result handling |
| `internal/service/container.go` | 531 | Service container wiring, dependency injection |
| `internal/service/delegation.go` | 340 | Child session spawning for delegation/orchestration |
| `internal/service/session.go` | 158 | Session lifecycle management |
| `internal/service/stream.go` | 193 | SSE stream management |
| `internal/service/store.go` | 272 | Store service layer |
| `internal/service/todo.go` | 272 | Todo service operations |
| `internal/service/events_composite.go` | 134 | Composite event emitter |
| `internal/service/events.go` | 54 | Event interface definitions |

Total untested: **3649 lines** across 10 files in the service layer.

The tested files (`chat_test.go` at 366 lines of source, `chat_loop_state_test.go` at 295 lines, `agent_test.go`, `skill_test.go`, `tool_test.go`, `context_test.go`) cover helper/utility functions but not the primary orchestration paths.

Prior audits have identified specific concerns in these untested paths:
- `concurrency-cancellation-sweep`: orphaned `generateResponse` goroutines at `internal/service/chat.go:165/209/240`
- `panic-recovery-sweep`: missing recover() in provider callback goroutines
- `exhaustive` lint finding at `chat_tool_executor.go:118`: missing `permission.DecisionAllow` case in permission switch

## Impact

Every chat interaction flows through `chat_generate.go`. Every tool call flows through `chat_tool_executor.go`. These are the highest-traffic code paths in the system. Regressions in these files will not be caught by any automated test. The `captureEnvelopeData` goroutine (the one working envelope emission path per reviewer-backend context) is completely untested.

## Recommendation

Write integration-style tests for `chat_generate.go` using a mock provider that emits a known stream of events, verifying:
1. Happy-path streaming: delta events arrive, done event terminates
2. Tool-use loop: tool_use event triggers tool execution, result feeds back
3. Context cancellation: mid-stream cancellation cleans up goroutines
4. `captureEnvelopeData` produces correct envelope on stream end

Write unit tests for `chat_tool_executor.go` covering:
1. Permission check integration: each `Decision` variant (Allow, Deny, Ask) is handled
2. The missing `DecisionAllow` exhaustive case (verify the default branch handles it correctly or add the case)

## References

- `2026-04-11-concurrency-cancellation-sweep` — orphaned goroutine findings in this path
- `2026-04-11-panic-recovery-sweep` — missing recover() findings
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — exhaustive lint finding at `chat_tool_executor.go:118`
