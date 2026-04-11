# [Info] Observations and praise

**Scope:** MCP client transport
**Topic:** Info / Praise
**Date:** 2026-04-10

## Praise to preserve

### 12.1 — `MCPTransport` interface is the right shape

The `MCPTransport` interface at `internal/mcp/manager.go:L20-L23` is small (two methods), declared at the consumer (`Manager`), and decouples stdio / HTTP / in-process transports cleanly. This matches Go community conventions and makes the transport layer testable with fakes. The in-process transports (DevToolsTransport, GeneralToolsTransport, SelfToolsTransport, MemoryToolsTransport, CodeExecTransport) all implement it with minimal ceremony. The interface is the foundation that makes every fix in findings 01-07 possible without breaking callers.

### 12.2 — `ToolLoadChecker` is a clean opt-in filter

`internal/mcp/manager.go:L28-L30` declares a minimal interface for per-tool loadType filtering:

```go
type ToolLoadChecker interface {
    IsToolEnabled(toolName string) bool
}
```

One method, consumer-defined, optional (nil-safe at `manager.go:L216`). This is the kind of interface the Go community idiom section of the deep-review rubric calls out as "done right."

### 12.3 — `Manager.GetToolsForIntent` is broker-aware and fails open

`internal/mcp/manager.go:L174-L201` threads the broker through for intent-aware tool selection, but falls back to the full tool list if the broker errors. "Fail open" is the right default here — the broker is an optimization, not a correctness gate.

### 12.4 — `memory_tools.go` is the one transport that honors the context contract

`internal/mcp/memory_tools.go:L34` is the reference implementation the other four in-process transports should match. See finding 05 for the gap. Note that memory_tools.go exists and got it right before the others — indicates the pattern was understood, just not enforced.

### 12.5 — `NewHTTPTransport` sets a timeout

`internal/mcp/http_transport.go:L68-L72`:

```go
client: &http.Client{
    Timeout: 60 * time.Second, // longer than stdio's 30s to account for network latency
},
```

A timeout on `http.Client` is the single most commonly forgotten piece of HTTP-client hygiene. This one has it. The comment also explains the rationale. Good.

### 12.6 — Parser idempotency on auto-discovered skills

`internal/mcp/manager.go:L362-L441` (`AutoDiscover`) is idempotent on repeated runs: it skips existing slugs, creates only new ones, marks removed ones. The logic has the `strings.Replace` fragility noted in finding 11.6, but the overall shape (read current, diff against DB, apply only deltas) is the correct pattern.

## Architectural observations (no action)

### 12.7 — No plugin-hosted MCP server integration yet

The reviewer-backend context mentions "plugins/adapters that host MCP servers" as a potential scope target. This audit found no such integration — the plugin system and the MCP system are independent in the current codebase. `internal/plugin/` does not call any `internal/mcp/` registration helpers. The plugin audit's plan Track B.5 proposes a `PluginMCPTransport` wrapper, which would land this integration. When it does, it inherits all the findings in this audit — particularly findings 01-04 — so the plan should either share transport internals with the plugin subprocess transport or explicitly fork.

### 12.8 — `mcp_servers.go` API handler is the trust-boundary gate

The `POST /api/mcp-servers` handler at `internal/api/mcp_servers.go:L26-L67` is the actual trust boundary for user-installed stdio servers: the `command`, `args`, and `env` fields come in over HTTP (behind basic auth) and flow through to `exec.Command`. The API validation today checks only that `Name` is non-empty and `TransportType` is `stdio|sse`. No validation of the command path, no constraint on args, no allowlist on env keys. **This belongs to the `api-privilege-boundary` scope**, not this one, so no finding is filed. But the mapping is worth recording: any defense added at that layer benefits every finding in this audit.

### 12.9 — `internal/mcpserver/` (Nanite-as-MCP-server) is out of scope but shares `SelfToolsTransport`

`internal/mcpserver/server.go` exposes Nanite's self-tools to external MCP clients (e.g., Claude CLI) via `mark3labs/mcp-go`. It reuses `SelfToolsTransport` directly. Any fix to `SelfToolsTransport` (finding 05, finding 06) automatically improves the server-side too. The server side itself is out of scope for this audit but is worth a separate `mcp-server-hosting` scope if Nanite is going to ship the `nanite mcp serve` subcommand.

### 12.10 — `dev_tools.go`, `general_tools.go`, `code_exec_tools.go` are parallel-audit scope

Per the task mandate, the handler bodies in these three files are covered by the parallel `dev-tools-input-validation` audit and the already-completed sandbox audit. The only finding this audit files against them is the transport-contract `_ context.Context` drop in finding 05. Any input-validation or path-traversal concerns in the handler bodies are out of scope here.
