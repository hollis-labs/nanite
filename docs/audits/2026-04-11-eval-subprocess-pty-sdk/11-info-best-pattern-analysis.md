# [Info] Best-pattern analysis: Claude adapter is the reference implementation

**Scope:** All 8 CLI adapters + 2 bridges
**Topic:** Architecture — best pattern determination
**Date:** 2026-04-11

## Observation

### Spawning strategy

The codebase offers three subprocess execution strategies:

| Strategy | Implementation | Used by |
|---|---|---|
| PTY (pseudo-terminal) | `PTYBridge` via `creack/pty` | All 8 adapters (registered as `pty-<name>`) |
| Subprocess (stdin/stdout pipes) | `SubprocessBridge` via `cmd.StdoutPipe()` | All 8 adapters (registered as `sub-<name>`) |
| Sandbox exec | `sandbox.AgentExec` / `sandbox.UserExec` | User-triggered shell commands only |

Every adapter is registered twice in `main.go:L363-387` — once as a PTY provider and once as a subprocess provider. The consumer (chat engine / provider selection) chooses which to use.

### Lifecycle comparison across adapters

| Adapter | Structured output | Session resume | Completion detection | Usage reporting | Tool visibility |
|---|---|---|---|---|---|
| **Claude** | stream-json | `--resume <id>` | `result` event | Full (input/output/cache tokens) | `tool_use` events |
| **Codex** | `--json` JSONL | No (single-turn) | `turn.completed` | No | No |
| **Gemini** | stream-json | `--resume <id>` | `result` event | Partial (input/output) | `tool_use` events |
| **Qwen** | stream-json | `--continue` | `result` event | Partial (input/output) | `tool_use` events |
| **Junie** | `--output-format json` | `--session-id` | `result`/`done` event | Partial (input/output) | `tool_use` events |
| **Copilot** | None (plain text) | No | EOF only | No | No |
| **Aider** | None (plain text) | No (single-turn) | EOF only | No | No |
| **Kiro** | Mixed (JSON optional) | `--resume` (no value) | JSON done / EOF | No | No |

### Best pattern: Claude adapter

`pty_claude.go` is the most complete implementation:
1. Full structured output parsing (stream-json envelope with typed events)
2. Session resume via `--resume <session_id>`
3. Explicit completion detection via `result` event with `subtype` field
4. Full token usage reporting including cache hits
5. Tool visibility (tool_use events forwarded to the chat engine)
6. Well-tested: `pty_claude_test.go` covers all event types

Qwen and Gemini are close seconds — they follow the same stream-json pattern with minor differences in field naming.

### Adapter plugin vs. provider adapter confusion

There are two separate adapter concepts in the codebase:

1. **`CLIAdapter` (pkg/provider)** — Controls how a CLI is spawned and its output is parsed. 8 implementations.
2. **`CLIAgentAdapter` (internal/agent)** — Controls sandbox population and agent discovery. 5 implementations as plugins.

These are not 1:1. The provider adapters control the spawning/parsing lifecycle. The agent adapter plugins control file layout. They share names (claude, codex, gemini) but have different responsibilities and live in different packages.

### Recommendation for new adapters

New CLI adapters should follow the Claude adapter pattern:
1. Implement `CLIAdapter` with structured JSON output parsing
2. Emit `session_id`, `delta`, `tool_use`, `usage`, and `done` events
3. Support `--resume` if the CLI provides it
4. Use `lookPathExpanded()` for detection (no subprocess execution during detect)
5. Test all event types with table-driven tests

## References

- `pkg/provider/pty_claude.go` — reference provider adapter
- `pkg/provider/cli_adapter.go` — CLIAdapter interface
- `internal/agent/adapter.go` — CLIAgentAdapter interface
- `cmd/nanite/main.go:L362-387` — dual registration pattern
