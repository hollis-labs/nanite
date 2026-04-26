# Handoff — Phase D Beta Gate (CW-20260424-0007)
# Date: 2026-04-25

## Status

Three smoke scenarios for Phase D beta gate. Scenario 3 ✓ passes. Scenarios 1 and 2 still failing.

---

## Scenario 1 — Subagent reads a file (Go chat runner)

**Test:** Parent agent calls `nanite_spawn_subagent` with `role: nanite-backend`, asks subagent to read first 5 lines of `/Users/chrispian/Projects-apps/nanite/README.md`.

**Latest result (c62):** "The subagent encountered permission issues accessing the file. It appears that the nanite-backend role doesn't have the necessary file system tools loaded."

### What was fixed this session

1. **Spawn return type** — `Spawn()` now returns `(string, string, error)` (runID, summary, error). Previously returned `(string, error)` so summary was lost.
2. **Sync timeout** — sync subagent run now uses `context.Background()` as exec ctx so 30s `messageCallTimeout` doesn't cancel the child runner before it completes.
3. **Dev tools as builtins** — added `DevToolProviderDefinitions()` to `internal/mcp/dev_tools.go` and registered dev tools (`dev_read`, `dev_grep`, `dev_bash`, `dev_glob`, `dev_edit`, `dev_write`) as builtins in `initMCP()` in `main.go`. This was intended to make dev tools appear in ALL sessions regardless of broker selection.

### Why it still fails

**Root cause under investigation:** Despite registering dev tools as builtins, the subagent's LLM still reports no file system tools are available. The most likely remaining issue is one of:

**A. Builtin registration not reaching SelectForAgent for the child session.**
`SelectToolsAsProvider` prepends builtins via `tb.Builtins.GetBuiltins()`. All child sessions use the SAME shared `ToolClient` instance. If the builtins are registered (confirmed by `go build` passing), they should be in `tb.Builtins`. To verify: add a temporary log in `SelectToolsAsProvider` logging the count of dev builtins included.

**B. `filterToolsByAllowlist` stripping dev tools.**
`SelectForAgent` runs `filterToolsByAllowlist(allTools, agent.Tools)` for the agent. If the subagent's resolved agentID maps to an agent config that has an explicit `tools.allow` list not including `dev_*`, they'd be stripped. Need to check what `agent.Tools` contains for the subagent run's agentID.

**C. The `developer_mode` gate.**
`SelectToolsAsProvider` checks `devMode := tb.developerModeEnabled()` and skips dev builtins if false. **DB confirmed:** `developer_mode = 1`. Migration 026 sets it, `container.go` sets permission engine to ModeYolo on startup when DeveloperMode=1.

**D. The subagent's agentID.**
What agentID is used for the child ChatRunner session? If `GetAgent(agentID)` returns an agent with a restrictive tool allowlist, dev tools could be filtered. If it fails (no such agent), allowlist is skipped entirely. Need to trace `execute()` → `runnerFactory` → how agentID is passed to `SelectForAgent`.

### Key files

- `internal/mcp/dev_tools.go` — `DevToolProviderDefinitions()` (new, line ~218)
- `cmd/nanite/main.go` — `initMCP()`, `tb.Builtins.RegisterBuiltins("dev", devToolDefs)` (new)
- `internal/toolclient/broker.go` — `SelectToolsAsProvider()` lines 209–281, builtin gate lines 224–236
- `internal/service/tool.go` — `SelectForAgent()`, `filterToolsByAllowlist`, progressive threshold (line 73: `ProgressiveDiscoveryThreshold = 10`)
- `internal/subagent/service.go` — `execute()`, `Spawn()` signature
- `internal/mcp/self_tools_transport.go` — `callSpawnSubagent()` (returns summary text)

### Next debugging step

Add a temporary `slog.Info` in `SelectToolsAsProvider` after the builtin loop:
```go
slog.Info("toolclient: builtins included for agent", "agent", agentID, "count", len(defs), "names", toolNames(defs))
```
Then run scenario 1 and check logs to see exactly which tools the subagent session received.

---

## Scenario 2 — PTY agentic task (Claude CLI subprocess)

**Test:** Start a PTY session (provider: pty-claude / adapter-claude plugin). Ask it to read `/Users/chrispian/Projects-apps/nanite/go.mod`.

**Latest result (c59):** "I don't have permission to read `/Users/chrispian/Projects-apps/nanite/go.mod` from this session — the harness only grants access to the sandbox directory."

### What was fixed this session

1. **CLAUDE.md updated in `claudeMDBody`** (`internal/plugin/builtin/adapter-claude/plugin.go`) — added a `## File Access` section stating:
   - Running with `--dangerously-skip-permissions`
   - Sandbox directory is NOT a restriction
   - `dev_read`, `dev_bash`, `dev_grep` are always allowed
   - Native Read/Bash tools also work anywhere

### Why it still fails

The Claude CLI subprocess is the Claude Code CLI (`claude --dangerously-skip-permissions`). Its behavior is governed by:

1. **The CLAUDE.md written to the sandbox** — the `claudeMDBody` constant is what gets written to `~/.nanite/sandboxes/{sessionID}/CLAUDE.md`. If the text is clear that all paths are allowed, the LLM should use its native tools (Read/Bash) or the dev MCP tools without restriction.

2. **HOWEVER** — the Claude CLI subprocess uses its **own** MCP configuration. The MCP dev tools from the nanite server are NOT automatically available inside the Claude CLI subprocess. The PTY subprocess gets the nanite MCP server via the conduit/self connection, but dev tools in the subprocess are the **Claude Code native tools** (Read, Bash, etc.), not `dev_read`.

3. **Claude Code native tools** — when using `--dangerously-skip-permissions`, Claude Code's native Read/Bash tools should have NO restrictions. They shouldn't need dev_read at all. The LLM should just call `Read(path=...)` directly.

**Core question:** Is the Claude CLI failing because:
- (A) It sees a CLAUDE.md/system prompt telling it it's sandboxed and infers restrictions that don't actually exist?
- (B) The `--dangerously-skip-permissions` flag is NOT being passed to the PTY subprocess?

### Check the PTY launch command

In `internal/plugin/builtin/adapter-claude/plugin.go`, find where the PTY subprocess is launched. Verify:
- `--dangerously-skip-permissions` IS in the args
- The CLAUDE.md is written to the correct sandbox directory before the process starts
- The `cwd` for the process is set to the sandbox dir (not a restriction path)

**Key question:** What does the CLAUDE.md look like in the sandbox at test time? Check `~/.nanite/sandboxes/` for recent session directories and read their CLAUDE.md.

### Key files

- `internal/plugin/builtin/adapter-claude/plugin.go` — PTY launch code, `claudeMDBody` constant
- `~/.nanite/sandboxes/` — per-session sandbox dirs with CLAUDE.md
- The PTY subprocess stdin/stdout pipeline

---

## Infrastructure changes made this session (all in main repo `~/Projects-apps/nanite`)

| File | Change |
|------|--------|
| `internal/store/migrations/026_subagent_approval_default_off.sql` | Sets `subagent_approval_required = 0` AND `developer_mode = 1` |
| `internal/service/container.go` | Sets permission engine to ModeYolo when DeveloperMode=1 |
| `internal/subagent/types.go` | Added `Summary string` field to `Run` struct |
| `internal/subagent/service.go` | `Spawn()` returns `(string, string, error)`, sync mode uses background ctx, `execute()` sets `run.Summary` |
| `internal/subagent/service_test.go` | All ~23 `Spawn` call sites updated to 3-value destructure |
| `internal/mcp/self_tools_transport.go` | `callSpawnSubagent()` passes sync timeout, returns actual summary |
| `internal/mcp/dev_tools.go` | Added `DevToolProviderDefinitions()`, added `provider` import |
| `cmd/nanite/main.go` | `initMCP()` registers dev tools as builtins |
| `internal/toolclient/tool_knowledge.go` | Removed `project-management` category (11 engine_* entries) |
| `config/agents/worker.yaml` | Removed `engine` from `mcp_servers` |

---

## Decisions locked

- Dev tools registered as **builtins** (bare names, no `mcp__` prefix) so they bypass `ProgressiveDiscoveryThreshold = 10` and appear regardless of broker selection. Same pattern as self-service tools.
- Subagent `execute()` uses `context.Background()` for exec ctx (NOT the parent tool-call ctx which has a 30s timeout).
- Engine tools removed from static catalog and worker agent — Fragments Engine is an optional external service.
- Migration 026 sets both `subagent_approval_required = 0` and `developer_mode = 1` so beta testers get full access without approval prompts.
