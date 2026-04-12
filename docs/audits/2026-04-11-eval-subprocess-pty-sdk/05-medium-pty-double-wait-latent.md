# [Medium] PTYBridge has latent double-cmd.Wait() risk

**Scope:** PTYBridge lifecycle
**Topic:** Concurrency — process wait correctness
**Date:** 2026-04-11

## Problem

The PTY bridge's main goroutine calls `cmd.Wait()` in the normal path and `killProcess()` calls `cmd.Wait()` in the cancellation path. Current control flow prevents both from executing, but the exclusion is fragile and depends on a `return` statement.

## Evidence

`pkg/provider/pty.go:L126-183` — main goroutine:
```go
go func() {
    // ...
    for scanner.Scan() {
        select {
        case <-ctx.Done():
            // ...
            p.killProcess(cmd)
            return  // <-- prevents reaching cmd.Wait() at :172
        default:
        }
    }
    // ...
    if err := cmd.Wait(); err != nil { ... }  // :172
}()
```

`pkg/provider/pty.go:L189-204` — killProcess:
```go
func (p *PTYBridge) killProcess(cmd *exec.Cmd) {
    // ...
    go func() {
        cmd.Wait()  // :196
        close(done)
    }()
    // ...
}
```

If a future edit removes the `return` or adds a code path after `killProcess()`, `cmd.Wait()` is called twice, causing a panic. Go's `exec.Cmd.Wait` documentation states it releases resources on first call; a second call panics.

This is independently confirmed by `2026-04-11-provider-abstractions/05-medium-pty-double-wait-risk.md`.

## Impact

Not currently triggered. A future refactor in a hot code path (every PTY session) could introduce a panic. The panic would crash the host process because provider goroutines have no `recover()` (cross-ref: `2026-04-11-panic-recovery-sweep`).

## Recommendation

Use `sync.Once` to make `cmd.Wait()` idempotent:

```go
var waitOnce sync.Once
var waitErr error
waitForProcess := func() error {
    waitOnce.Do(func() { waitErr = cmd.Wait() })
    return waitErr
}
```

Replace both `cmd.Wait()` call sites with `waitForProcess()`.

## References

- `2026-04-11-provider-abstractions/05-medium-pty-double-wait-risk.md` — original finding
- `2026-04-11-panic-recovery-sweep` — no recover in provider goroutines
