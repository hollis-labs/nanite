# [Medium] PTY bridge has latent double-cmd.Wait() risk

**Scope:** PTY goroutine lifecycle
**Topic:** Concurrency correctness
**Date:** 2026-04-11

## Problem

The PTY bridge's main goroutine and `killProcess` both call `cmd.Wait()`. Current control flow prevents simultaneous execution, but the separation is subtle and fragile — a future edit could introduce a double-Wait panic.

## Evidence

`pkg/provider/pty.go:L126-183` — main goroutine:
```go
go func() {
    defer close(ch)
    defer ptmx.Close()

    scanner := bufio.NewScanner(ptmx)
    for scanner.Scan() {
        select {
        case <-ctx.Done():
            ch <- StreamEvent{Type: "error", Error: "context cancelled"}
            p.killProcess(cmd)
            return  // <-- returns before reaching cmd.Wait() at :172
        default:
        }
        // ... parse events ...
    }

    // Wait for process to finish.
    if err := cmd.Wait(); err != nil {  // :172
        // ...
    }
}()
```

`pkg/provider/pty.go:L189-204` — killProcess:
```go
func (p *PTYBridge) killProcess(cmd *exec.Cmd) {
    _ = cmd.Process.Signal(syscall.SIGTERM)
    done := make(chan struct{})
    go func() {
        cmd.Wait()  // :196
        close(done)
    }()
    select {
    case <-done:
    case <-time.After(5 * time.Second):
        _ = cmd.Process.Kill()
    }
}
```

The main goroutine either:
- (a) Reaches `cmd.Wait()` at `:172` after the scanner loop exits (no ctx cancellation), OR
- (b) Calls `killProcess` on ctx cancellation, which calls `cmd.Wait()` at `:196`, then returns before reaching `:172`.

The exclusion depends on the `return` at `:139`. If a future edit removes that return or adds a fallthrough path, both `:172` and `:196` execute. Calling `cmd.Wait()` twice on the same `*exec.Cmd` panics.

Cross-ref: `2026-04-11-concurrency-cancellation-sweep` item 13 noted this same pattern.

## Impact

Not currently triggered. Latent risk: a future refactor that changes the control flow in the goroutine body can introduce a panic in a hot path (every PTY session). The panic would crash the host process (no recover — cross-ref panic-recovery sweep).

## Recommendation

Use `sync.Once` to ensure `cmd.Wait()` is called exactly once:

```go
var waitOnce sync.Once
var waitErr error
waitForProcess := func() error {
    waitOnce.Do(func() { waitErr = cmd.Wait() })
    return waitErr
}
```

Replace both `cmd.Wait()` calls with `waitForProcess()`. This makes double-call safe regardless of control flow.

## References

- `2026-04-11-concurrency-cancellation-sweep` item 13 — same observation.
- Go `exec.Cmd.Wait` docs: "If any of c.Stdin, c.Stdout, or c.Stderr are not an *os.File, Wait also waits for the respective I/O loop copying to or from the process to complete. Wait releases any resources associated with the Cmd." Calling it twice panics.
