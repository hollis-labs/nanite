# [Critical] Unbounded MCP response reading allows a malicious or misbehaving server to OOM the host

**Scope:** MCP client transport — stdio and HTTP response handling
**Topic:** Security / Memory & Resources
**Date:** 2026-04-10
**Status:** Resolved. Hotfix PR #42 (`9384714`, 2026-04-14) installed a
10 MiB per-response floor on both transports (`http.MaxBytesReader` and
bounded `readLineBounded`). S4b (2026-04-16) tightens that ceiling
per-trust-tier via `SetMaxResponseBytes` and adds a defense-in-depth
`ValidateResultSize` check on the assembled body in `Manager.ExecuteTool`.
See `docs/mcp-trust-model.md`.

## Problem

Neither the stdio nor the HTTP MCP transport places any upper bound on the size of a response read from an MCP server. Both use unbounded readers:

- Stdio: `t.stdout.ReadBytes('\n')` on a `bufio.Reader` with default 4 KiB initial buffer but no cap — `ReadBytes` will grow the internal buffer without limit until it finds a newline or the reader errors.
- HTTP: `json.NewDecoder(resp.Body).Decode(&rpcResp)` with no `io.LimitReader` wrapping the response body.

The `ToolResult` that comes back is a Go struct containing `[]ToolContent`, where each `ToolContent.Text` is also unbounded. A malicious MCP server (or a buggy one returning a log dump) can hand the host a 1 GiB single-line JSON document. Nanite will buffer the entire thing, `json.Unmarshal` the entire thing, and then hand the resulting string to `Manager.ExecuteTool` which builds a `strings.Builder` containing the entire payload and returns it to the chat engine.

## Evidence

`internal/mcp/stdio_transport.go:L108-L112`:

```go
readCh := make(chan readResult, 1)
go func() {
    line, err := t.stdout.ReadBytes('\n')   // unbounded
    readCh <- readResult{line, err}
}()
```

`internal/mcp/http_transport.go:L96-L110`:

```go
resp, err := t.client.Do(req)
if err != nil { return nil, fmt.Errorf("send request: %w", err) }
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
    errBody, _ := io.ReadAll(resp.Body)   // unbounded even for error path
    return nil, fmt.Errorf("MCP server error %d: %s", resp.StatusCode, string(errBody))
}

var rpcResp JSONRPCResponse
if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {   // unbounded
    return nil, fmt.Errorf("decode response: %w", err)
}
```

For comparison, `SelfToolsTransport.callShowGiphy` at `internal/mcp/self_tools_transport.go:L477` DOES use `io.ReadAll(io.LimitReader(resp.Body, 64*1024))` when calling the Giphy API. The pattern is already known to the codebase; it is simply not used at the transport layer.

After reading, `Manager.ExecuteTool` at `internal/mcp/manager.go:L282-L300` copies every `Content` block into a `strings.Builder` with no size check:

```go
var sb strings.Builder
for _, c := range result.Content {
    if c.Type == "text" && c.Text != "" {
        if sb.Len() > 0 { sb.WriteString("\n") }
        sb.WriteString(c.Text)   // no cap
    }
}
```

From there the string flows back to `internal/service/tool.go:L194-L199`, `internal/chat/` (via the engine's tool-result handler), the store (`message_parts.content`), OTel spans, and the HTTP stream back to the UI — every layer passes it through unbounded.

## Impact

A single malicious MCP server can OOM the host with one RPC call. The trust model here matters:

- **User-installed stdio MCP servers** — the user typed a command into `POST /api/mcp-servers` (`internal/api/mcp_servers.go:L26-L67`). If that endpoint is compromised via CSRF/auth bypass, or if a user is tricked into installing a hostile server (`.mcp.json` import via `handleImportMCPServers` at L137), the attacker gets memory-level DoS plus everything below.
- **HTTP MCP servers over the public internet** — any remote server with a URL Nanite is configured to talk to can return a gigabyte response. Network latency doesn't save us; the reader just keeps accepting.
- **Prompt-injection pivot** — a compromised MCP server can return a ToolResult whose text is a crafted prompt injection payload. With no size bound the injection can include as much context as needed to overcome the host model's instructions. Chat engine audit finding 01 already flagged that `scope_guard` is dead code; there is no downstream filter here either.

Even a non-malicious MCP server that returns debug output can cause the same crash. A user running a local `mcp-server-weather` that has a bug and dumps its entire HTTP log to one response crashes Nanite, taking every chat session and plugin down with it.

A practical exploit: the `mcp-server-fetch` pattern (an MCP server whose tool fetches a URL and returns its body) becomes an SSRF + memory amplification attack when any user can call it.

This is Critical in the rubric because: (a) triggered by normal operation of an MCP server that the user trusted by installing it, (b) crashes the host process, (c) exists on every transport concurrently.

## Recommendation

Enforce a configurable cap at each layer, with a safe default around 10 MiB for tool results:

1. **Stdio reader.** Replace `bufio.Reader.ReadBytes('\n')` with a bounded reader that tracks accumulated bytes per call and errors out past the limit. Example:

```go
const maxResponseBytes = 10 * 1024 * 1024  // 10 MiB

var buf bytes.Buffer
r := io.LimitReader(t.stdout, maxResponseBytes+1)
// Read until newline or limit+1 bytes consumed.
for {
    b, err := t.stdout.ReadByte()
    if err != nil { ... }
    if buf.Len() >= maxResponseBytes {
        return nil, fmt.Errorf("response exceeded %d bytes", maxResponseBytes)
    }
    if b == '\n' { break }
    buf.WriteByte(b)
}
```

Or retain `bufio.Reader` and wrap with `io.LimitReader` at the pipe level: `t.stdout = bufio.NewReader(io.LimitReader(stdoutPipe, maxResponseBytes))`. The caveat is that `LimitReader` must be reset per call, which the `io.LimitReader` API doesn't support directly; a custom wrapper that resets its counter each call is cleaner.

2. **HTTP body.** Wrap `resp.Body` in `http.MaxBytesReader(nil, resp.Body, maxResponseBytes)` before decoding:

```go
body := http.MaxBytesReader(nil, resp.Body, maxResponseBytes)
if err := json.NewDecoder(body).Decode(&rpcResp); err != nil { ... }
```

Also apply to the error-body branch: `errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))`.

3. **Tool result content.** Add a `MaxResultBytes` guard in `Manager.ExecuteTool` that returns an error if the assembled text exceeds a threshold. This is a defense-in-depth step for the in-process transports too (which can construct arbitrarily large results themselves).

4. **Surface the error clearly.** Return an error that the chat engine can propagate to the LLM as a tool-call failure rather than a host crash.

The default cap should be configurable per-server in `MCPServerConfig` for the niche case where a legitimate tool returns large bodies (e.g., a `file_read` MCP). Without a per-server override the global default stays protective.

## References

- `internal/mcp/stdio_transport.go:L108-L112` — unbounded stdout read
- `internal/mcp/http_transport.go:L96-L110` — unbounded HTTP response + error body
- `internal/mcp/manager.go:L282-L300` — result assembly without cap
- `internal/mcp/self_tools_transport.go:L477` — the LimitReader pattern the project already uses for Giphy
- Related: sandbox audit finding 02 (SSRF — MCP HTTP transport shares the trust class)
- Related: chat engine audit finding 01 (`scope_guard` dead code — no downstream filter)
