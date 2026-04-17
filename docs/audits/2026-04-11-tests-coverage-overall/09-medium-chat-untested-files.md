# [Medium] Chat package: 6 source files untested including orchestrator and delegate

**Scope:** internal/chat/
**Topic:** Test Quality — orchestration layer gaps
**Date:** 2026-04-11

## Problem

The `internal/chat/` package has 13 source files. 6 have no matching test file. Two of the untested files — `orchestrator.go` (212 lines) and `commands_builtin.go` (244 lines) — contain significant logic.

## Evidence

Untested chat source files:

| File | Lines | Responsibility |
|------|-------|----------------|
| `commands_builtin.go` | 244 | Built-in slash command implementations (/clear, /compact, /delegate, /help, etc.) |
| `orchestrator.go` | 212 | Multi-agent task decomposition, plan generation |
| `context.go` | 120 | System prompt assembly, context composition |
| `errors.go` | 111 | Error type definitions, error formatting |
| `decomposer.go` | 93 | Task decomposition for orchestrator |
| `delegate.go` | 41 | Delegation request construction |

Tested chat files:
- `engine.go` (197 lines) — `engine_test.go` (141 lines)
- `commands.go` (228 lines) — `commands_test.go` (179 lines)
- `envelope.go` (201 lines) — `envelope_test.go` (221 lines)
- `activity.go` (249 lines) — `activity_test.go` (160 lines)
- `structured.go` (96 lines) — `structured_test.go` (164 lines)
- `proctrack.go` (238 lines) — `proctrack_test.go` (305 lines)
- `context_client.go` (439 lines) — `context_client_test.go` (279 lines)

Note: `commands.go` (the command router) is tested, but `commands_builtin.go` (the actual command implementations) is not. This means the routing is tested but the handlers are not.

## Impact

`orchestrator.go` implements multi-agent task decomposition — a complex feature involving plan generation and sub-task spawning. Without tests, regressions in orchestration logic will go undetected. `commands_builtin.go` contains 244 lines of slash command implementations that users invoke directly; untested command handlers risk silent breakage.

## Recommendation

1. `commands_builtin_test.go` — test each built-in command handler with mock dependencies
2. `orchestrator_test.go` — test plan generation with a mock LLM provider that returns a known decomposition
3. `context_test.go` — test system prompt assembly with various agent configurations

## References

- `internal/chat/commands_test.go` — existing router tests (template for command handler tests)
- `internal/chat/engine_test.go` — existing engine tests
