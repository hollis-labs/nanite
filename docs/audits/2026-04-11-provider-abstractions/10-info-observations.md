# [Info] Positive observations and architectural notes

**Scope:** Provider abstractions — overall
**Topic:** Idioms / Architecture
**Date:** 2026-04-11

## Observations

### Clean Provider interface design

The `Provider` interface (`provider.go:L95-102`) is minimal (4 methods), follows "accept interfaces, return structs," and uses channels for streaming — idiomatic Go. The `Embedder` interface is a clean optional extension. The `CLIAdapter` interface cleanly separates CLI-specific concerns from the bridge infrastructure.

### Anthropic provider is the reference implementation

`anthropic.go` demonstrates the full pattern: retry with exponential backoff, circuit breaker integration, rate tracking with header calibration, OTel tracing, cache hint support. The 771-line file is well-structured with clear method decomposition (`streamChatInternal`, `readSSEWithTracking`, `readSSE`, `handleSSEData`, `calibrateRateTracker`). This is the template the other providers should follow.

### PTY launch logging avoids sensitive data

`pty.go:L102-103` correctly logs only the argument count, not the arguments themselves:
```go
log.Printf("pty[%s]: launching CLI with %d args", p.adapter.Name(), len(args))
```
Same pattern in `subprocess.go:L86`. This is the right approach — args contain user prompts and system prompts.

### Circuit breaker implementation is sound

`circuit.go` is clean, well-tested (circuit_test.go exists), uses `sync.Mutex` consistently, and handles the closed->open->half-open->closed state machine correctly. The `IsOpen` method transitions to half-open when cooldown expires, which is the standard pattern. No race conditions detected.

### Rate limiter implementation is sound

`ratelimit.go` uses a sliding window with proper mutex protection. `WaitTime` correctly walks the window to calculate when enough budget frees up. `UpdateLimit` allows runtime calibration from API headers. `expire()` is called at every entry point. No issues found.

### Retry logic correctly handles Retry-After header

`retry.go` parses both integer-seconds and HTTP-date formats. `BackoffDelay` caps server-provided retry-after at 60s (separate from the 8s max for computed backoff). Jitter is applied correctly.

### Scanner buffer sizing is deliberate

PTY and subprocess bridges set a 1MB scanner buffer for large tool results. SSE readers use 64KB initial / 1MB max (Anthropic) or defaults (others). These are reasonable for LLM output.

### Process tracker integration is clean

The `ProcessCallback` and `ActivityCallback` context values decouple the provider package from the service layer. Both PTY and subprocess bridges correctly notify on start and exit.
