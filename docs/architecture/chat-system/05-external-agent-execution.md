# 05 · External Agent Execution

> **Scope:** every way nanite spawns or invokes another agent. Four agent-spawn types: Nanite Harness, Inline Parallel, Async PTY/CLI, Chat-Session Parallel. Includes the full PTY Claude Code flow.
>
> **Related:** [04 Harness](04-chat-harness-and-loop-orchestration.md), [06 Permission](06-permission-and-authority.md), [07 Provider](07-provider-routing.md), [12 Inbox](12-inbox-and-internal-messaging.md), [13 Notifications](13-notification-and-card-surface.md).

## Purpose

Provide a uniform abstraction for "run another agent" while accommodating four distinct agent-spawn shapes. A subagent in nanite is a **child session** running the same chat harness — there is no separate "subagent runtime."

## Key files

- `internal/dispatch/execute.go:168` — `ExecuteTask` → `Spawner.Spawn(ctx, SpawnRequest)`
- `internal/subagent/service.go` — `subagent.Service`
- `internal/service/subagent_runner.go:228-249` — `ChatRunner.Run` (creates child session, binds agent, registers lineage, drives `generateResponse`)
- `internal/service/chat_tool_executor.go` — `executeToolBatch` (Inline Parallel)
- `~/Projects-apps/go-providers/provider/pty.go:92-243` — `PTYBridge.streamCLI` (Async PTY)
- `~/Projects-apps/go-providers/provider/pty_claude.go:29-44` — `ClaudeAdapter.BuildArgs`
- `internal/sandbox/os_*.go` — OS sandbox-exec
- `internal/plugin/builtin/adapter-claude/plugin.go:231` — `buildCLAUDEMD`, `buildAgentContext`

## The four agent-spawn types

| Type | Functional? | Mechanism | Tmp dir | Injected context | Sandbox | Permissions | File-dir enforcement |
|---|---|---|---|---|---|---|---|
| **Nanite Harness** (default chat turn) | YES | `chatServiceImpl.generateResponse` in-process; provider via `go-providers` HTTP | none (HTTP) | system prompt + slot blocks | OS sandbox-exec for `dev_*` | path grants + profile perms ([06](06-permission-and-authority.md)) | `dev_*` `resolveAllowed` gate |
| **Inline Parallel** (multiple `tool_use` blocks in one assistant turn) | YES | `executeToolBatch` runs concurrent-safe tools in parallel goroutines, serial otherwise | n/a | n/a | n/a | path grants honored, shared mutex serializes SSE writes | yes |
| **Async PTY/CLI** (subagent with `Provider="pty"` / `pty-claude` etc) | YES | `subagent_runner.Run` → child session → `provider.PTYBridge.streamCLI` → `pty.Start(exec.CommandContext)` | yes — `sandbox.Dir(sessionID)` populated via `sandbox.Populate` | `CLAUDE.md` + `.sandbox/envelope-schema.md` + `.sandbox/agent-context.md` + `.mcp.json` | `cmd.Dir = sbDir`; OS sandbox-exec on darwin/linux | path-grant lineage walks to parent | yes via `dev_tools` allowlist + grants |
| **Chat-Session Parallel** (two FE chat sessions both streaming) | YES | independent http/SSE streams; backend keeps `inFlightGen` per session | n/a | n/a | n/a | independent | n/a |

## Note: shell-task backend (not in the agent-spawn lane)

`~/Projects-apps/nanite/internal/background/pty.go` exists in the codebase but is **not** a CLI-agent spawn primitive — it's a detached **shell-task backend**.

- **Mechanism:** `exec.CommandContext` running `/bin/sh -c <task>` with `Setpgid=true`, captured stdout/stderr to a bounded buffer, `CompletionFunc` callback. Despite the package path containing "pty", it does **not** allocate a real PTY.
- **Use cases:** file scrapers, scheduled health probes, fire-and-forget shell tasks.
- **Privileged:** **no sandbox, no permissions, no file-dir enforcement.** Audit any caller; this is **NOT** the right primitive for spawning CLI agents (Claude Code, Codex, etc.) — those go through the Async PTY/CLI lane above.
- **Audit reference:** the agent-boot-adoption work (Phase 4b, implementer report 2026-05-08) confirmed this distinction during the spawn-type consolidation audit.
- Privileged-shell-task hardening (if needed) is tracked separately as [G-BG-PRIVILEGED](gaps.md#g-bg-privileged) and is a distinct concern from the `agent.Boot` consolidation pattern.

## Subagent = child session

The spawn primitive at `internal/service/subagent_runner.go:228`:

1. `ChatRunner.Run` creates a child `*store.Session` (parent is `parentSessionID`).
2. `EnsureSessionAgent(childID, agent.ID, ...)` binds an agent profile.
3. Persist the prompt as a user `*store.Message` on the child session.
4. `pathGrants.RegisterLineage(childID, parentSessionID)` (line 249) — clears via defer.
5. `s.chat.generateResponse(ctx, childID, ...)` runs in a goroutine — full chat harness.
6. Provider/model inherit from agent profile → falls back to parent.

The child session has its own SSE stream, its own loop state, its own `inFlightGen` entry. It posts results back to the parent via the inbox ([12](12-inbox-and-internal-messaging.md)) or by being the parent's tool result.

## Async PTY — the Claude Code flow

This is the answer to "if I start a PTY chat session with claude code, am I just using Claude Code with nanite as the GUI?" — **partially**, and the details matter.

When `providerName == "pty"` or `pty-*`:

1. Chat session's `provider.StreamChat` is `PTYBridge.streamCLI` (`pty.go:92-243`).
2. The harness still runs the full slot-assembly pipeline → builds `ChatRequest{SystemPrompt, SlotBlocks, Messages}`.
3. `EffectiveSystemPrompt()` flattens `SystemPrompt` + each non-empty slot into a single string.
4. PTY bridge **extracts only the last user message as `prompt`** (`pty.go:94-100`). Slot blocks + prior messages collapse into `systemPrompt` only.
5. `ClaudeAdapter.BuildArgs(prompt, systemPrompt, cliSessionID)` (`pty_claude.go:29-44`):
   - **First turn:** `claude -p "<prompt>" --output-format stream-json --verbose --system-prompt "<systemPrompt>"`
   - **Subsequent turns:** `claude --resume <cliSessionID> -p "<prompt>" --output-format stream-json --verbose`
   - **`--system-prompt` is dropped on resume.** Slot-block changes after turn 1 never reach Claude.
   - Devmode adds `--dangerously-skip-permissions`.
6. Working dir is `sandbox.Dir(sessionID)`, populated with:
   - `CLAUDE.md` (from `buildCLAUDEMD`) — Claude reads this as project context
   - `.sandbox/agent-context.md` — agent profile + mode
   - `.sandbox/envelope-schema.md` — static envelope-emission cheat sheet
   - `.mcp.json` — exposes nanite's MCP server to Claude (so it can call `nanite_*` tools back into nanite)
7. `system/init` event from Claude's stream-json output yields the CLI session ID; persisted to `session.metadata.cli_session_id` (`chat_generate.go:1160-1163`, `persistCLISessionID`).
8. **Resume-based, NOT long-lived.** Each user message is a fresh `claude -p` subprocess that uses `--resume <cliSessionID>` after the first turn. Claude maintains its own internal state; nanite proxies one turn at a time.

### What signal IS available from a PTY child

From `ClaudeAdapter.ParseLine`:
- `EventDelta` (text chunks)
- `EventToolUse` (tool block — but **not surfaced as nanite `tool_call` SSE**)
- `EventUsage` (token counts)
- `EventDone`, `EventError`
- `pty_turn_start` / `pty_turn_complete` / `pty_turn_failed` rows in `session_events`
- `ActivityCallback` from context fires per parsed line — drives presence throttle
- `ProcessCallback` notifies the process tracker of start/stop

**No-silent-drop guard** at `pty.go:151-158`: if Claude emits only `tool_use` blocks with no text delta, the bridge emits a single `EventError` ("tool calls cannot be forwarded"). This is the sentinel for Claude running its own internal tool loop and producing no narrative.

### What does NOT exist

- **Per-tool SSE for CLI/PTY children.** Tool calls inside Claude's loop run internally; nanite sees only the final text.
- **No bidirectional channel.** Nanite cannot inject mid-turn input or notifications.
- **No streaming progress between Claude's tool calls.** That's the source of "spinner stays on, nothing visible."

## Logic gates

- **Path-grant lineage** is the only inheritance — profile permissions are NOT inherited. Workers get their own profile bound by `EnsureSessionAgent`. ([06](06-permission-and-authority.md))
- **Sandbox dir per session** Async PTY children get `~/.nanite/sandboxes/<sessionID>` populated per turn.
- **CLAUDE.md is regenerated per turn** so changes to agent profile / mode reach Claude even though `--system-prompt` doesn't.
- **`.mcp.json` injection is the back-channel** — Claude's own MCP client hits nanite's MCP server, which is how a CLI child can `nanite_message_send`, `nanite_show_card`, etc. ([12](12-inbox-and-internal-messaging.md))

## Current gaps

- **G-PTY-RESUME-DROP** — `--system-prompt` is dropped on `--resume` turns. Slot changes after the first turn don't reach Claude. The CLAUDE.md / agent-context.md regenerated per turn is the partial workaround. See [gaps.md](gaps.md#g-pty-resume-drop).
- **G-PTY-NO-TOOL-EVENTS** — Claude Code's internal tool calls aren't surfaced as nanite `tool_call` SSE events. Only final text streams. The "no-silent-drop" guard is the sentinel for tool-only turns. Direct cause of the user-reported "hard to tell anything is happening" symptom. See [gaps.md](gaps.md#g-pty-no-tool-events).
- **G-BG-PRIVILEGED** — `internal/background/pty.go` shell-task backend has no sandbox / permissions / file-dir enforcement. Separate concern from the agent-spawn lane (the file is not a CLI-agent spawn primitive). See [gaps.md](gaps.md#g-bg-privileged).

## Improving CLI/PTY visibility (design notes)

The user asked: can we get realtime SSE during CLI tool steps? Two layers of leverage:

1. **Per-line `ActivityCallback`** already fires on every parsed line. We could emit a `status` SSE per `EventToolUse` (the tool *name* is in the parsed event even when text is absent), giving a single-line "WORKING — calling Read on …" indicator that overwrites itself.
2. **`.mcp.json` back-channel** — Claude's MCP client can already call `nanite_message_send`. We could prompt the agent (via `CLAUDE.md` / `agent-context.md`) to emit a `status_update` message between major steps. This is opt-in via prompt, not transport-level.

Both can be added without changing transport. Layer 1 is more reliable (transport-level); Layer 2 is richer but depends on the agent following instructions.

## Test surface

- Mock `provider.Provider` for Nanite Harness path.
- Real `pty.Start` against a fake CLI binary that emits stream-json — exercise no-silent-drop guard, `--resume` behavior, sandbox dir population.
- Lineage path-grant test: Async PTY child claims `~/Projects-apps/foo` granted only to the parent.
- Chat-Session Parallel: two sessions in flight, assert no `inFlightGen` cross-talk, separate SSE streams.
- Inline Parallel: a turn with two `tool_use` blocks, one parallel-safe and one serial-only — assert ordering.
