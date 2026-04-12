# [Info] Sandbox exec has proper backpressure controls

**Scope:** Sandbox exec
**Topic:** Backpressure
**Date:** 2026-04-11

## Problem

No problem. This is a positive finding.

## Evidence

`internal/sandbox/exec.go:L179-L207`:

```go
func runCmd(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (*ExecResult, error) {
    var stdout, stderr limitedBuffer
    stdout.max = maxOutputBytes  // 1MB
    stderr.max = maxOutputBytes  // 1MB
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()
    // ...
}
```

`limitedBuffer` (`exec.go:L272-L290`) is a `bytes.Buffer` wrapper that silently discards writes after `max` bytes. Combined with `context.WithTimeout` (30s default for agent, 60s for user), the sandbox exec path has:

1. **Bounded output buffers** (1MB each for stdout and stderr).
2. **Timeouts** on the execution context.
3. **Synchronous execution** (`cmd.Run()` blocks until the command finishes).
4. **No goroutine spawning** for output reading.

This is the cleanest backpressure implementation in the codebase and serves as a reference for how other subprocess-reading sites should work.

## Impact

None. Positive.

## Recommendation

Use `limitedBuffer` or a similar pattern as the basis for bounding reads in other subprocess sites (MCP stdio, plugin subprocess).

## References

- `internal/sandbox/exec.go:L179-L290`
