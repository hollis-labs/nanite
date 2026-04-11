# [High] Both `DevToolsTransport.CallTool` and `GeneralToolsTransport.CallTool` discard caller context

**Scope:** `internal/mcp/dev_tools.go`, `internal/mcp/general_tools.go` — `CallTool` dispatch
**Topic:** Concurrency correctness — context propagation, cancellation
**Date:** 2026-04-10

## Problem

Both transports receive a `context.Context` on their `CallTool` entrypoint and immediately discard it via `_ context.Context`. Every handler then either uses `context.Background()` directly (`callBash`) or runs synchronously with no cancellation at all (everything else). The consequence:

- User-aborted chat turns cannot stop in-flight tool calls.
- Server shutdowns cannot drain in-flight tool calls.
- HTTP-client outbound calls (`web_fetch`) cannot be cancelled.
- The `filepath.Walk` inside `dev_grep` and `dev_glob` runs to completion regardless of whether anyone still cares about the result.

`dev_bash` has a concrete downstream effect (subprocesses leak for up to 120 seconds on a cancelled request; filed separately as finding 04). `web_fetch` has a concrete downstream effect (outbound request completes even after abort). `dev_grep` has a concrete downstream effect (minutes of disk I/O on large trees). The rest are lower-impact but share the same root cause — one fix addresses all of them.

## Evidence

`DevToolsTransport`:

```go
// internal/mcp/dev_tools.go:141
func (d *DevToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
```

Every handler below has signature `func (d *DevToolsTransport) callRead(args map[string]any)` — no `ctx` parameter at all. `callBash` synthesizes its own `context.Background()` at line 524.

`GeneralToolsTransport`:

```go
// internal/mcp/general_tools.go:148
func (g *GeneralToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
```

Same pattern. `callWebFetch` at line 181 does `client := &http.Client{Timeout: 10 * time.Second}` with no `context.Context` on the request — the timeout is a hard wall, not a cancellation signal, and the caller has no way to abort earlier.

`ListTools` on both also takes `_ context.Context` (L56, L31). This is less impactful but equally lazy.

## Impact

- **Cancellation broken across the board.** Any tool invoked through the MCP layer ignores the caller's cancellation signal.
- **Resource consumption past a cancellation.** Subprocesses, outbound HTTP, and disk walks keep running. In aggregate this is a "death of a thousand paper cuts" on availability — one aborted turn doesn't hurt, but a server doing 100 aborted turns per hour is doing work for nothing.
- **Test quality.** Tests rely on `context.Background()` everywhere, so this bug is invisible to the existing test suite. A "context cancellation propagates" test is the right regression guard.

The sandbox audit's finding 07 (`07-high-subprocess-lifecycle-no-process-group-orphan-children.md`) has a closely related concern at the subprocess layer; a full fix pairs ctx plumbing here with process-group handling there.

## Recommendation

Change both transports' `CallTool` to accept and thread the context:

```go
// dev_tools.go
func (d *DevToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
    switch name {
    case "dev_read":
        return d.callRead(ctx, args)
    case "dev_grep":
        return d.callGrep(ctx, args)
    case "dev_write":
        return d.callWrite(ctx, args)
    case "dev_glob":
        return d.callGlob(ctx, args)
    case "dev_edit":
        return d.callEdit(ctx, args)
    case "dev_bash":
        return d.callBash(ctx, args)
    ...
}
```

Each handler:

- `callRead` / `callEdit`: wrap the `os.Open` in a goroutine that's interruptable via ctx, or at minimum check `ctx.Err()` between chunks in the scanner loop.
- `callGrep` / `callGlob`: inside the `filepath.Walk` callback, `if ctx.Err() != nil { return ctx.Err() }` early-return. Walk terminates on any non-nil error.
- `callWrite`: the operation is atomic at the filesystem layer; ctx plumbing is cosmetic but harmless.
- `callBash`: pass `ctx` into `context.WithTimeout(ctx, ...)` instead of `context.Background()`. See finding 04 for the full fix.
- `callWebFetch`: use `http.NewRequestWithContext(ctx, "GET", url, nil)` and `client.Do(req)`.

Add a test:

```go
func TestDevGrep_ContextCancel(t *testing.T) {
    dt, dir := tempDevTools(t)
    // Populate dir with thousands of files.
    ...
    ctx, cancel := context.WithCancel(context.Background())
    cancel() // pre-cancelled
    _, err := dt.CallTool(ctx, "dev_grep", map[string]any{
        "pattern": ".",
        "directory": dir,
    })
    if err == nil {
        t.Fatal("expected ctx.Err from pre-cancelled context")
    }
}
```

Recommended severity: High. Affects every tool in the package, fix is mechanical, test is easy.

## References

- `internal/mcp/dev_tools.go:L141` — dispatch that drops ctx
- `internal/mcp/dev_tools.go:L499-L544` — `callBash` with `context.Background()`
- `internal/mcp/general_tools.go:L148` — dispatch that drops ctx
- `internal/mcp/general_tools.go:L175-L197` — `callWebFetch` without request-scoped context
- `docs/audits/2026-04-10-sandbox-hardening/07-high-subprocess-lifecycle-no-process-group-orphan-children.md` — related subprocess-lifecycle concern
- Go stdlib idiom: `context.Context` as first parameter, threaded through all blocking calls
- Related: `04-high-dev-bash-output-unbounded-and-context-ignored.md`, `06-high-dev-grep-loads-full-files-into-memory.md`
