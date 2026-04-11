# [Low] Collateral observations across the MCP client transport

**Scope:** MCP client transport
**Topic:** Idiomatic Go / Memory & Resources / Error handling
**Date:** 2026-04-10

Small items that don't rise to individual-file severity but are worth tracking together. Each has a concrete `file:line` citation.

## 11.1 — `HTTPTransport` has no per-request retry or circuit breaker

### Problem
`internal/mcp/http_transport.go:L65-L72` constructs a plain `http.Client{Timeout: 60 * time.Second}` with no retry policy, no circuit breaker, and no backoff. Any transient 5xx from a remote MCP server propagates directly as a tool error. The project has `internal/provider/retry.go` + `circuit.go` for LLM providers — the MCP HTTP transport does not reuse either.

### Recommendation
Either wrap `http.Client.Do` in a small retry+backoff helper (already ad hoc in a few places) or hoist `internal/provider/retry.go` to a shared `internal/netretry` package and reuse. Low priority until MCP HTTP transports see serious production use.

## 11.2 — `Manager.DiscoverTools` silently skips failing servers

### Problem
`internal/mcp/manager.go:L126-L131`:

```go
for name, transport := range m.servers {
    tools, err := transport.ListTools(ctx)
    if err != nil {
        log.Printf("mcp: failed to discover tools from %s: %v", name, err)
        continue
    }
```

On discovery failure, the server is logged and skipped. It stays in `m.servers` forever with zero tools and no retry. A user who installed 5 MCP servers might have 4 working — but there's no UI or API that says "server X failed to discover tools."

### Recommendation
Track per-server discovery status on the `toolEntry` or on a new `serverStatus map[string]discoveryStatus`. Expose via `ListServers`. Pairs with finding 09's real-state tracking.

## 11.3 — `Manager.RemoveServer` closer check uses inline interface assertion

### Problem
`internal/mcp/manager.go:L85-L87`:

```go
if closer, ok := transport.(interface{ Close() error }); ok {
    closer.Close()
}
```

Same pattern at L455-L457 inside `Close()`. The inline anonymous interface works but obscures whether every transport implements `io.Closer`. If the `MCPTransport` interface declared `Close() error` explicitly, the compiler would enforce it for all implementations and this check would disappear. Today, only `StdioTransport` has `Close`; the others don't.

### Recommendation
Either widen `MCPTransport` to include `Close(ctx context.Context) error` (preferred; gives lifecycle control), or keep the assertion but add a lint note. Not urgent.

## 11.4 — `parsePrefixedToolName` is fragile against tool names containing `__`

### Problem
`internal/mcp/manager.go:L463-L478`:

```go
rest := strings.TrimPrefix(name, "mcp__")
idx := strings.Index(rest, "__")
// ...
server = rest[:idx]
tool = rest[idx+2:]
```

Uses first-match on `__`. A server named `foo__bar` and a tool named `baz` produces prefixed name `mcp__foo__bar__baz`, which `parsePrefixedToolName` parses as server=`foo`, tool=`bar__baz`. The real server was `foo__bar`. Same class of first-match bug as installer audit finding 03 (managed-section parser). Not currently exploitable because server names don't contain `__` in practice, but it's a latent bug.

### Recommendation
Either: (a) disallow `__` in server names at config time, or (b) use `strings.LastIndex` and document the precedence, or (c) switch to a length-prefixed encoding. Cheapest is (a): validate at `handleCreateMCPServer` in `internal/api/mcp_servers.go`. Note for the `api-privilege-boundary` scope.

## 11.5 — `Manager.RemoveServer` in-place slice filter re-uses the backing array

### Problem
`internal/mcp/manager.go:L92-L98`:

```go
filtered := m.tools[:0]
for _, entry := range m.tools {
    if entry.serverName != name {
        filtered = append(filtered, entry)
    }
}
m.tools = filtered
```

The `[:0]` idiom reuses the backing array, which means the tail of the original slice still references the removed `toolEntry` values (and their `Tool.InputSchema map[string]any` payloads). Not a leak per se because `m.tools = filtered` discards the length, but the underlying array's tail elements still live until the next `append` past them overwrites them. For a tools slice that shrinks and never grows back to its old size, a handful of old entries pin their `InputSchema` maps in memory.

### Recommendation
Either assign to a fresh slice (`filtered := make([]toolEntry, 0, len(m.tools))`) or zero the tail before truncating. Minor; only matters if a server with hundreds of tools is removed and the slice never regrows. Note only.

## 11.6 — `AutoDiscover` mutates skill JSON with `strings.Replace` (string-concatenated JSON)

### Problem
`internal/mcp/manager.go:L428-L429`:

```go
sk.Settings = strings.Replace(sk.Settings, `"auto_discovered":true`, `"auto_discovered":true,"removed":true`, 1)
```

Manipulating a JSON-serialized string via `strings.Replace` is fragile. If the existing `Settings` doesn't contain the literal `"auto_discovered":true` (e.g., it got re-serialized with different whitespace, or the field was later renamed), the replace silently no-ops and `"removed":true` never lands. A maintainer who later changes the JSON shape has no compile-time or runtime guard.

### Recommendation
Unmarshal → mutate → marshal. Standard pattern:

```go
var settings map[string]any
if err := json.Unmarshal([]byte(sk.Settings), &settings); err != nil { continue }
settings["removed"] = true
data, _ := json.Marshal(settings)
sk.Settings = string(data)
```

Low priority — the code path is auto-discover cleanup, rarely exercised.

## 11.7 — `SelfToolsTransport.callShowGiphy` uses `http.DefaultClient` instead of a scoped client

### Problem
`internal/mcp/self_tools_transport.go:L471`:

```go
resp, err := http.DefaultClient.Do(req)
```

`http.DefaultClient` has no timeout (Giphy request has a 5s context, which saves us here). Other modules in Nanite use scoped clients for retry and observability hooks. Mixed styles is not a functional bug but is an idiom inconsistency.

### Recommendation
Use the same scoped client pattern as elsewhere. Trivial diff. Note.

## 11.8 — No `defer` on goroutine spawned in `StdioTransport.call`

### Problem
`internal/mcp/stdio_transport.go:L109-L112`:

```go
go func() {
    line, err := t.stdout.ReadBytes('\n')
    readCh <- readResult{line, err}
}()
```

The goroutine has no panic recovery (see finding 06) and no labeled provenance for debugging. If `ReadBytes` panics (it won't under normal bufio behavior, but a corrupted reader isn't impossible), the panic is lost. Combined with the goroutine-leak from finding 01 (this goroutine blocks forever on timeout), there's no way to inspect or clean up.

### Recommendation
Fold into the reader-goroutine-per-transport rework from finding 03. Don't fix in isolation.
