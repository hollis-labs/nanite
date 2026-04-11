# [High] `dev_bash` reads unbounded process output into memory and drops caller context

**Scope:** `internal/mcp/dev_tools.go` — `callBash`
**Topic:** Memory & resources, cancellation, concurrency correctness
**Date:** 2026-04-10

## Problem

Two independent resource-management bugs in `callBash`:

1. **Unbounded output capture.** `cmd.CombinedOutput()` reads all of stdout + stderr into a single in-memory buffer with no size limit. A command like `yes` or `cat /dev/urandom` writes gigabytes per second until the timeout fires, and all of it is allocated in the nanite process heap before the function returns. On a 30-second default timeout against `yes`, that is on the order of several GB of RSS growth before the buffer is discarded. This is a trivial host OOM primitive.

2. **Caller context discarded.** The handler signature is `CallTool(_ context.Context, name string, args map[string]any)`, and `callBash` then builds its own fresh `context.Background()`. If the caller cancels the request (user aborts the chat turn, HTTP request is dropped, session is torn down), the subprocess keeps running until the internal timeout fires. This is a cancellation / goroutine-lifetime issue. Related bugs exist in every other tool handler (findings 08), but `dev_bash` is the one that spawns an actual subprocess, which makes the impact concrete.

## Evidence

```go
// internal/mcp/dev_tools.go:141-158
func (d *DevToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
    switch name {
    case "dev_read":
        return d.callRead(args)
    ...
    case "dev_bash":
        return d.callBash(args)
    ...
    }
}
```

Note the `_ context.Context` — the caller's context is discarded at the dispatch layer.

```go
// internal/mcp/dev_tools.go:499-544
func (d *DevToolsTransport) callBash(args map[string]any) (*ToolResult, error) {
    ...
    timeout := intArg(args, "timeout", 30)
    ...
    ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, "sh", "-c", command)
    cmd.Dir = workDir

    out, err := cmd.CombinedOutput()
    output := string(out)
    ...
}
```

`CombinedOutput()` internally sets `cmd.Stdout` and `cmd.Stderr` to a `bytes.Buffer` and returns it only after `Wait()` returns. There is no cap. The only bound on total bytes is the 30-second (or 120-second maximum) timeout, which at typical shell-pipe throughput is an order-of-magnitude too generous for "safe in a long-running daemon."

The `context.Background()` on line 524 is the second bug: even if the outer HTTP request or chat turn is aborted, this subprocess will run to the internal timeout. On a shared dev machine that can mean 120 seconds of zombie work per aborted request.

## Impact

- **OOM:** attacker prompt induces the model to call `dev_bash(command="yes", timeout=120)`. Host process RSS grows until the OS kills nanite or the user's machine swaps to death. If nanite is part of a daemon workflow (e.g. the user has `nanite serve` running), this is a denial-of-service of the whole dev environment.
- **Cancellation leak:** aborted turns leave subprocesses running for up to 120 seconds. Multiple aborted turns compound — there is no cap on concurrent `dev_bash` invocations. An attacker who can trigger 10 `dev_bash` calls per second and have each run for 120 seconds can have 1200 shells live at once.
- **Not exploitable without finding 01 being unfixed.** If finding 01 is resolved by removing `dev_bash` (Option A there), this finding disappears. If finding 01 is resolved by routing through `sandbox.AgentExec`, the sandbox has its own output-capture primitive (`sandbox.ExecResult`) and the unbounded-output bug moves there — audit item for the sandbox follow-up, noted below.

## Recommendation

The root fix is to resolve finding 01. Once `dev_bash` goes away (Option A) or routes through `sandbox.AgentExec` (Option B), this finding becomes part of the sandbox's output-capture responsibility.

If — and only if — the project keeps a direct-exec `dev_bash` for any reason:

1. **Cap output.** Replace `CombinedOutput()` with explicit `Stdout`/`Stderr` pipes, each wrapped in an `io.LimitReader` at (say) 256KB, with a truncation marker appended:

   ```go
   var stdout, stderr bytes.Buffer
   cmd.Stdout = &limitedWriter{w: &stdout, cap: 256 * 1024}
   cmd.Stderr = &limitedWriter{w: &stderr, cap: 256 * 1024}
   ```

   The `truncate` package already exists in `internal/truncate/` for formatting the steering-hint suffix — reuse that pattern from `code_exec_tools.go:L192-L198`.

2. **Honor caller context.** Plumb the `context.Context` parameter through:

   ```go
   func (d *DevToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
       ...
       case "dev_bash":
           return d.callBash(ctx, args)
       ...
   }

   func (d *DevToolsTransport) callBash(parent context.Context, args map[string]any) (*ToolResult, error) {
       ...
       ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
       defer cancel()
       ...
   }
   ```

3. **Add a process-group kill** (`syscall.Setpgid` + `syscall.Kill(-pid, SIGKILL)` on cancellation). This is the same issue the sandbox audit flagged in finding 07 (`07-high-subprocess-lifecycle-no-process-group-orphan-children.md`) — whatever helper that audit's fix produces should be shared here. Without a process group kill, `sh -c "long-running-thing | other-thing"` only kills the `sh` parent when cancelled, leaving orphans.

## References

- `internal/mcp/dev_tools.go:L141` — the `_ context.Context` that discards the caller's cancellation
- `internal/mcp/dev_tools.go:L499-L544` — `callBash`
- `internal/mcp/code_exec_tools.go:L192-L198` — `truncate.Output` pattern that should be reused
- `docs/audits/2026-04-10-sandbox-hardening/07-high-subprocess-lifecycle-no-process-group-orphan-children.md` — process-group-kill finding in the sandbox layer, same class
- Go stdlib: `exec.Cmd.CombinedOutput` (unbounded by design), `exec.CommandContext` (kills only the direct child, not the process group)
- Related: finding `01-critical-dev-bash-bypasses-sandbox.md`, finding `08-high-tool-context-not-propagated.md`
