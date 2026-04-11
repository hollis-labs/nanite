# [Medium] Stdio transport sends subprocess stderr to /dev/null — crashes and diagnostics vanish

**Scope:** MCP client transport — observability
**Topic:** Error handling / Idiomatic Go
**Date:** 2026-04-10

## Problem

`StdioTransport.start` sets `t.cmd.Stderr = nil`. Per `exec.Cmd` docs, a nil `Stderr` is wired to `os.DevNull`. All diagnostic output the subprocess writes — panics, stack traces, log lines, error messages, upstream failures, handshake debug — is silently discarded. When an MCP server crashes, the transport returns `read from stdout: EOF` and the operator has no way to know why.

The in-code comment claims the motive is "avoid blocking":

```go
// Discard stderr to avoid blocking
t.cmd.Stderr = nil
```

That's half-right: if you connected stderr to a pipe and never read from it, it would block on full buffer. But discarding it entirely is the wrong fix. The right fix is a background draining goroutine that copies stderr to the host's log stream (ideally with a per-line prefix so which MCP server generated it).

## Evidence

`internal/mcp/stdio_transport.go:L63-L64`:

```go
// Discard stderr to avoid blocking
t.cmd.Stderr = nil
```

Compared to `internal/plugin/subprocess/manager.go` (which the plugin audit found does drain stderr — though with its own issues), the MCP client never even gets that far.

The impact is visible in the broader code: `Manager.DiscoverTools` at `internal/mcp/manager.go:L127-L130` logs:

```go
tools, err := transport.ListTools(ctx)
if err != nil {
    log.Printf("mcp: failed to discover tools from %s: %v", name, err)
    continue
}
```

The error message is `read from stdout: EOF` — useless for debugging. Whatever the subprocess actually wrote to stderr (e.g., "failed to connect to Postgres: authentication failed") is gone.

## Impact

- **Silent configuration errors.** An MCP server that can't find its API key logs the error to stderr. The operator sees "tools/list: read from stdout: EOF" and has to speculate.
- **Silent crashes.** A Node.js MCP server that panics writes a stack trace to stderr. Nanite says "EOF."
- **Silent upstream failures.** An MCP server that depends on an internet API and can't reach it writes a sensible error. Nanite says "EOF."
- **Beta UX.** For developer friends running their first MCP server, "it doesn't work and there's no error message" is a terrible first impression. The error is literally in memory inside a child process and the parent threw it away.

## Recommendation

Stream stderr through a log prefix and keep the draining behavior.

```go
stderrPipe, err := t.cmd.StderrPipe()
if err != nil { return fmt.Errorf("stderr pipe: %w", err) }
// Drain stderr, logging each line with the server name prefix.
go func() {
    scanner := bufio.NewScanner(stderrPipe)
    scanner.Buffer(make([]byte, 0, 4096), 1024*1024) // cap at 1 MiB per line
    for scanner.Scan() {
        log.Printf("mcp[%s]: stderr: %s", t.command, scanner.Text())
    }
    // scanner returns when pipe closes (subprocess exited or we killed it).
}()
```

Notes:
- The scanner buffer cap prevents a malicious server from OOMing the host via a 10 GiB stderr line.
- The draining goroutine exits when the pipe closes, which happens naturally on subprocess exit or `cmd.Process.Kill()`.
- Use a dedicated logger (structured or tagged) if the project has one. The existing pattern in `stdio_transport.go` uses `log.Printf` directly.
- For the plugin-hosted MCP server integration (if/when that lands per the plugin plan), share the same draining helper.

If the intent is to hide noisy stderr in production, gate it behind a verbosity flag on `MCPServerConfig` — but the default should be "log it, prefixed." Silent-by-default is too costly for a system whose primary failure mode is "my MCP server doesn't work and I don't know why."

## References

- `internal/mcp/stdio_transport.go:L41-L72` — `start` and stderr discard
- Related: finding 04 (no crash detection) — stderr draining feeds into crash diagnosis
