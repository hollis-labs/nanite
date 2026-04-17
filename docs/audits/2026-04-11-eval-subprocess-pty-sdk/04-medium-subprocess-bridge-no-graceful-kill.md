# [Medium] SubprocessBridge context cancellation uses SIGKILL and double-kills

**Scope:** SubprocessBridge lifecycle
**Topic:** Concurrency — subprocess shutdown
**Date:** 2026-04-11

## Problem

On context cancellation, `SubprocessBridge` sends `SIGKILL` directly and also triggers the `exec.CommandContext` built-in kill. This results in an ungraceful double-kill with no opportunity for the child process to clean up.

## Evidence

`pkg/provider/subprocess.go:L88,118-125`:
```go
cmd := exec.CommandContext(ctx, s.cliPath, args...)  // :88 — CommandContext kills on ctx.Done()

// ...
select {
case <-ctx.Done():
    ch <- StreamEvent{Type: "error", Error: "context cancelled"}
    if cmd.Process != nil {
        _ = cmd.Process.Kill()  // :123 — explicit SIGKILL, redundant with CommandContext
    }
    return
default:
}
```

`exec.CommandContext` already sends `os.Kill` (SIGKILL on Unix) when the context is cancelled. The explicit `cmd.Process.Kill()` at `:123` is redundant.

Compare with `PTYBridge` which uses `killProcess()` with SIGTERM-first at `pkg/provider/pty.go:L189-204`.

## Impact

- CLIs spawned via SubprocessBridge (all 8 adapters register a `sub-*` provider name) get no graceful shutdown opportunity.
- State corruption risk when CLI is mid-file-write or mid-git-operation.
- Already flagged in `2026-04-11-provider-abstractions/06-medium-subprocess-no-graceful-kill.md`. This finding confirms the same issue in the current code and adds the double-kill observation.

## Recommendation

Use `cmd.Cancel` (Go 1.20+) with `cmd.WaitDelay` for SIGTERM-first shutdown:

```go
cmd := exec.Command(s.cliPath, args...)
cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
cmd.WaitDelay = 5 * time.Second
```

Remove the explicit `cmd.Process.Kill()` from the goroutine's context-done path. The stdlib handles escalation from SIGTERM to SIGKILL after `WaitDelay`.

Alternatively, extract `PTYBridge.killProcess` to a shared `killProcess(cmd, grace)` function usable by both bridges.

## References

- `2026-04-11-provider-abstractions/06-medium-subprocess-no-graceful-kill.md` — original finding
- `pkg/provider/pty.go:L189-204` — graceful kill reference
- Go `exec.Cmd.Cancel` documentation
