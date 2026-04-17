# [Medium] SubprocessBridge uses SIGKILL without graceful SIGTERM

**Scope:** PTY goroutine lifecycle — SubprocessBridge
**Topic:** Concurrency correctness
**Date:** 2026-04-11

## Problem

On context cancellation, the `SubprocessBridge` immediately sends `SIGKILL` to the child process. The `PTYBridge` sends `SIGTERM` first, then waits 5 seconds before escalating to `SIGKILL`. The subprocess bridge skips the graceful shutdown entirely.

## Evidence

`pkg/provider/subprocess.go:L118-125`:
```go
for scanner.Scan() {
    select {
    case <-ctx.Done():
        ch <- StreamEvent{Type: "error", Error: "context cancelled"}
        if cmd.Process != nil {
            _ = cmd.Process.Kill()  // SIGKILL — no SIGTERM first
        }
        return
    default:
    }
```

Compare with PTY bridge `pkg/provider/pty.go:L134-139`:
```go
select {
case <-ctx.Done():
    ch <- StreamEvent{Type: "error", Error: "context cancelled"}
    p.killProcess(cmd)  // SIGTERM → wait 5s → SIGKILL
    return
default:
}
```

PTY's `killProcess` (`pty.go:L189-204`) does the graceful sequence. SubprocessBridge has no equivalent.

Additionally, `exec.CommandContext(ctx, ...)` at `subprocess.go:L88` will also kill the process when the context is cancelled (Go stdlib sends `os.Kill` by default). This means the process gets double-killed: once by the stdlib's context cancellation and once by the explicit `cmd.Process.Kill()` at `:123`. The explicit kill is redundant.

## Impact

- CLI tools spawned via `SubprocessBridge` (Junie, Kiro, Qwen, and any future adapter using subprocess mode) cannot clean up on cancellation. A CLI mid-write could leave partial files, corrupt state, or fail to release locks.
- `SIGKILL` cannot be caught by the child process. The child gets no opportunity to flush buffers, close connections, or save state.
- The redundant kill (both stdlib context and explicit) is harmless but indicates the author didn't account for `CommandContext`'s built-in kill behavior.

## Recommendation

Extract `killProcess` from `PTYBridge` to a shared function and use it in both bridges:

```go
// killProcess sends SIGTERM, then SIGKILL after a grace period.
func killProcess(cmd *exec.Cmd, grace time.Duration) {
    if cmd.Process == nil { return }
    _ = cmd.Process.Signal(syscall.SIGTERM)
    done := make(chan struct{})
    go func() { cmd.Wait(); close(done) }()
    select {
    case <-done:
    case <-time.After(grace):
        _ = cmd.Process.Kill()
    }
}
```

Also consider using `cmd.Cancel` (Go 1.20+) instead of `exec.CommandContext` to control the kill signal:

```go
cmd := exec.Command(s.cliPath, args...)
cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
cmd.WaitDelay = 5 * time.Second
```

This lets the stdlib handle SIGTERM-then-SIGKILL on context cancellation, removing the need for manual signal management.

## References

- Go `exec.Cmd.Cancel` — added in Go 1.20, allows custom cancellation behavior.
- `pkg/provider/pty.go:L189-204` — existing graceful kill implementation.
