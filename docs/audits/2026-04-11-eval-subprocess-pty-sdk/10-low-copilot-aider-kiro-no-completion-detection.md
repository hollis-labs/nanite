# [Low] Copilot, Aider, and Kiro adapters lack reliable completion detection

**Scope:** CLI adapter lifecycle
**Topic:** Idioms — lifecycle completeness
**Date:** 2026-04-11

## Problem

Three of the eight CLI adapters rely on plain-text line parsing and have no reliable mechanism to detect when the CLI agent has finished producing output. They never emit a `"done"` event from their parser unless specific text patterns or JSON events are encountered.

## Evidence

### CopilotAdapter — `pkg/provider/pty_copilot.go:L29-43`
```go
func (a *CopilotAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
    // ...
    return []StreamEvent{{Type: "delta", Content: text + "\n"}}, nil
}
```
Every non-empty line becomes a delta. No `"done"` event is ever emitted. Completion relies entirely on the process exiting and the scanner reaching EOF.

### AiderAdapter — `pkg/provider/pty_aider.go:L58-108`
```go
func parseAiderLine(line []byte) ([]StreamEvent, error) {
    // ...
    // JSON path can emit "done", but Aider doesn't output JSON by default.
    // Plain text path:
    return []StreamEvent{{Type: "delta", Content: text + "\n"}}, nil
}
```
A `"done"` event is only emitted if Aider outputs a JSON `{"type":"done"}` line, which it currently does not.

### KiroAdapter — `pkg/provider/pty_kiro.go:L64-106`
Same pattern. JSON `"done"` or `"result"` events are handled, but the plain-text fallback path never emits `"done"`.

In contrast, Claude, Gemini, Junie, and Qwen all emit `"done"` from their `"result"` event handling. Codex emits `"done"` from `"turn.completed"`.

## Impact

For these three adapters, the caller relies on channel close (process exit + scanner EOF) to detect completion rather than an explicit `"done"` event. This works in practice because the PTY/subprocess bridge goroutine closes the channel after the scanner loop, but:
- The consumer cannot distinguish between "finished successfully" and "process crashed" without checking the error channel.
- Usage/token tracking is never reported for these adapters (no `"usage"` event).
- Streaming progress indicators in the UI depend on `"done"` to transition state.

## Recommendation

For adapters without structured output, emit a synthetic `"done"` event when the scanner loop exits normally (process exited with code 0). This could be done in the bridge rather than in each adapter:

```go
// After scanner loop exits in pty.go / subprocess.go:
if err := cmd.Wait(); err != nil {
    // ...
} else {
    ch <- StreamEvent{Type: "done"}
}
```

This ensures all adapters emit `"done"` on successful completion regardless of their output format.

## References

- `pkg/provider/pty.go:L162-177` — scanner exit + cmd.Wait in PTYBridge
- `pkg/provider/subprocess.go:L148-161` — same in SubprocessBridge
