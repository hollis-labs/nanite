# Developer Mode Gate — Call Path Reference

**Scope:** CW-20260424-0001 Phase A  
**Implemented:** 2026-04-23  
**Owned by:** `internal/toolclient`

---

## What the gate protects

The `dev` MCP server exposes powerful filesystem and shell tools — `dev_bash`, `dev_read`, `dev_write`, `dev_edit`, `dev_glob`, `dev_grep` — that are safe for developers but inappropriate to expose to LLMs in production/beta sessions.  The gate ensures these tools are invisible to the LLM and non-executable unless the user has explicitly enabled **Developer Mode** in Settings.

---

## Gate architecture: two independent layers

The gate enforces at two independent points so that a bypass at one layer cannot enable the tool at the other.

```
User message
    │
    ▼
SelectForAgent (service/tool.go)
    │
    └─► ToolClient.SelectToolsAsProvider (toolclient/broker.go)
            │
            ├─ Read developer_mode via developerModeEnabled()
            │
            ├─ BUILTIN LOOP: for each builtin tool
            │       isDevTool(name) && !devMode → SKIP (tool never enters defs[])
            │
            └─ MCP LOOP: for each broker-selected MCP tool
                    isDevTool(prefixedName) && !devMode → SKIP
                    
                    Result: dev_* tools absent from the LLM's tool list
                    when developer_mode = false
    │
    ▼  LLM processes turn — cannot request or see dev tools
    │
    ▼
preCheckTools (service/chat_tool_executor.go)
    │  [standard gates: blocked/exhausted, permission engine, plugin pre-hook,
    │   enforceExecutionRules, arg validation]
    │
    ▼
toolServiceImpl.Execute (service/tool.go)
    │
    └─► ToolClient.CallTool (toolclient/broker.go)   ← SECOND GATE
            │
            ├─ isDevTool(toolName) && !developerModeEnabled()
            │       → error: "tool requires developer_mode to be enabled"
            │
            └─ [existing permission check, server resolution, execution]
```

---

## Key functions

| Function | File | Role |
|---|---|---|
| `isDevTool(name string) bool` | `internal/toolclient/broker.go` | Canonical membership check — matches both `dev_*` (bare) and `mcp__dev__*` (prefixed). |
| `developerModeEnabled() bool` | `internal/toolclient/broker.go` | Reads `user_settings.developer_mode`. Resolution order: `DeveloperModeFunc` (tests) → `Store.GetUserSettings()` (production). Fails closed (returns `false`) on any error. |
| `SelectToolsAsProvider` | `internal/toolclient/broker.go` | **Selection-time gate.** Filters dev tools from both the builtin loop and the MCP tool loop before the result is returned to the LLM. |
| `CallTool` | `internal/toolclient/broker.go` | **Execution-time backstop.** Denies execution of any dev tool regardless of how the tool name arrived (bare or prefixed). Runs before the agent permission check. |

---

## Canonical "dev" server tools

All tool names starting with `dev_` that belong to the `dev` MCP server are covered:

- `dev_bash` / `mcp__dev__dev_bash`
- `dev_read` / `mcp__dev__dev_read`
- `dev_write` / `mcp__dev__dev_write`
- `dev_edit` / `mcp__dev__dev_edit`
- `dev_glob` / `mcp__dev__dev_glob`
- `dev_grep` / `mcp__dev__dev_grep`

The `isDevTool` function uses a prefix check:

```go
func isDevTool(toolName string) bool {
    // Prefixed form: mcp__dev__*
    if strings.HasPrefix(toolName, "mcp__dev__") { return true }
    // Bare form: dev_*
    if strings.HasPrefix(toolName, "dev_") { return true }
    return false
}
```

> **Note:** The prefix `dev_` is deliberately specific — `developer_mode` (a settings field, not a tool) does NOT match because `isDevTool` checks `"dev_"` (with underscore), not `"dev"`.

---

## Developer Mode storage

`developer_mode` lives in `user_settings` (migration 001):

```sql
CREATE TABLE IF NOT EXISTS user_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    developer_mode INTEGER NOT NULL DEFAULT 0,
    ...
);
```

It is read on every gate check — no process-level caching. Toggling the setting in the UI takes effect on the next message turn without a restart.

---

## Test coverage

Tests live in `internal/toolclient/devmode_gate_test.go`:

| Test | What it verifies |
|---|---|
| `TestIsDevTool` | Canonical membership: all dev tool name forms match; non-dev tools do not. |
| `TestDeveloperModeEnabled_FuncOverride` | `DeveloperModeFunc` overrides the store read; nil+nil fails closed. |
| `TestSelectToolsAsProvider_DevToolsExcludedWhenDevModeOff` | Builtin dev tools absent from selection result when `devMode=false`. |
| `TestSelectToolsAsProvider_DevToolsIncludedWhenDevModeOn` | Builtin dev tools present when `devMode=true`. |
| `TestSelectToolsAsProvider_MCPDevToolsExcludedWhenDevModeOff` | MCP-registered dev tools absent from selection when `devMode=false`. |
| `TestCallTool_DevToolsDeniedWhenDevModeOff` | Both bare and prefixed dev tool names denied at execution. |
| `TestCallTool_DevToolsAllowedWhenDevModeOn` | Dev tools execute normally when `devMode=true`. |
| `TestCallTool_NonDevToolsUnaffectedByDevMode` | Non-dev tools unaffected by the gate when `devMode=false`. |
| `TestCallToolWithPolicyCheck_DevToolsDeniedWhenDevModeOff` | Policy-check wrapper inherits the gate from `CallTool`. |
| `TestDevModeFallClosed_NoStoreNoFunc` | No store + no func → dev tools absent (fail-closed). |

---

## Wiring in production (`cmd/nanite/main.go`)

The `dev` MCP server is registered unconditionally at startup — the gate is in the
selection/execution layer, not at registration. This is intentional: the transport
must be registered so developer sessions can discover and use the tools; the gate
then suppresses them for non-developer sessions at the point where the LLM sees the
tool list.

```go
// cmd/nanite/main.go — initMCP()
mcpManager.AddServer("dev", mcp.NewDevToolsTransport([...]), mcp.TierBuiltin)

// ToolClient is constructed with the real *store.Store.
// DeveloperModeFunc is nil → developerModeEnabled() reads from the store.
tb := toolclient.New(mcpManager, s, nil)
```

---

## Adding future dev-only tools

Any tool that should be gated behind developer_mode must:

1. Register on the `dev` MCP server (or a server whose prefix matches `isDevTool`).
2. If registering on a *different* server with a dev-only prefix, update `isDevTool`
   to cover the new prefix and add a test case in `TestIsDevTool`.

Do **not** bypass the gate by registering agentic tools on the `general` or `self`
server — those are always visible to all sessions.
