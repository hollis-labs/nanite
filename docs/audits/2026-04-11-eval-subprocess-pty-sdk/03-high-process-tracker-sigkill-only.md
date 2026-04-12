# [High] ProcessTracker uses SIGKILL without graceful shutdown

**Scope:** Process lifecycle management
**Topic:** Memory & Resources — subprocess reaping
**Date:** 2026-04-11

## Problem

`ProcessTracker.KillSession()`, `KillAll()`, and `KillStale()` all call `tp.process.Kill()` (SIGKILL) without first attempting a graceful `SIGTERM`. CLI processes being killed cannot flush buffers, release locks, save state, or clean up temporary files.

## Evidence

`internal/chat/proctrack.go:L85-104` — `KillSession`:
```go
func (pt *ProcessTracker) KillSession(sessionID string) int {
    // ...
    for _, tp := range procs {
        if err := tp.process.Kill(); err != nil {  // SIGKILL, no SIGTERM
            // ...
        }
    }
    return killed
}
```

`internal/chat/proctrack.go:L107-132` — `KillAll`:
```go
for _, tp := range procs {
    if err := tp.process.Kill(); err != nil {  // SIGKILL, no SIGTERM
```

`internal/chat/proctrack.go:L193-227` — `KillStale`:
```go
for _, entry := range stale {
    if err := entry.tp.process.Kill(); err != nil {  // SIGKILL, no SIGTERM
```

Compare with `PTYBridge.killProcess` (`pkg/provider/pty.go:L189-204`) which does SIGTERM, waits 5s, then SIGKILL. The process tracker does not use this pattern.

## Impact

- CLI agents (Claude, Codex, Gemini, etc.) killed mid-operation cannot clean up. Claude Code specifically uses local files and git state that can be left inconsistent.
- On engine shutdown (`KillAll`), all PTY processes are terminated without grace. This fires on every server restart.
- `KillStale` runs periodically as a background reaper. Stale but alive processes get SIGKILL.
- The process tracker stores `*os.Process` which only supports `Kill()` (SIGKILL) and `Signal()`. The graceful pattern requires `Signal(syscall.SIGTERM)` followed by a timed wait, then `Kill()`.

## Recommendation

Add a `gracefulKill` helper to the process tracker:

```go
func gracefulKill(proc *os.Process, grace time.Duration) error {
    if err := proc.Signal(syscall.SIGTERM); err != nil {
        return proc.Kill() // already dead or can't signal — force kill
    }
    done := make(chan struct{})
    go func() {
        proc.Wait()
        close(done)
    }()
    select {
    case <-done:
        return nil
    case <-time.After(grace):
        return proc.Kill()
    }
}
```

Use this in `KillSession`, `KillAll`, and `KillStale` with a 3-5 second grace period.

Note: `proc.Wait()` from `ProcessTracker` may conflict with `cmd.Wait()` called in the PTY/subprocess goroutine. This needs coordination — the process tracker should either own the wait or use signaling only. Consider storing the `*exec.Cmd` instead of `*os.Process` and using the `sync.Once` wait pattern from finding 05 in the provider-abstractions audit.

## References

- `pkg/provider/pty.go:L189-204` — existing graceful kill pattern
- `2026-04-11-provider-abstractions/06-medium-subprocess-no-graceful-kill.md` — same class of issue
- `2026-04-11-concurrency-cancellation-sweep` — provider lifecycle mapping
