# [Medium] SSE provider scanners use default 64KB buffer without explicit limit

**Scope:** Provider SSE readers
**Topic:** Memory & Resources
**Date:** 2026-04-11

## Problem

Six of the eight HTTP API provider SSE readers use `bufio.NewScanner(body)` without calling `scanner.Buffer()` to set a custom max token size. The default scanner max token size is 64KB (`bufio.MaxScanTokenSize`). This is bounded (not an OOM risk), but if a provider ever sends an SSE line larger than 64KB (e.g., a large tool result in a streaming response), the scanner will return `bufio.ErrTooLong` and stop reading the stream silently.

Only Anthropic sets a custom 1MB buffer. PTY and Subprocess bridges also set 1MB buffers. The remaining six providers do not.

## Evidence

Providers WITHOUT custom scanner buffer:

- `pkg/provider/openai.go:104` — `scanner := bufio.NewScanner(body)` (no `.Buffer()` call)
- `pkg/provider/ollama.go:109` — same
- `pkg/provider/gemini.go:142` — same
- `pkg/provider/mistral.go:91` — same
- `pkg/provider/openrouter.go:89` — same
- `pkg/provider/openzen.go:96` — same
- `pkg/provider/azure_openai.go:109` — same

Providers WITH custom scanner buffer:

- `pkg/provider/anthropic.go:492-494` — `scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` (1MB max)
- `pkg/provider/pty.go:130-132` — `scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)` (1MB max)
- `pkg/provider/subprocess.go:115-116` — `scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)` (1MB max)

## Impact

In practice, SSE data lines from most LLM providers are well under 64KB (individual delta events are small). The risk is primarily with providers that stream tool results or large structured payloads in a single SSE event. If an SSE line exceeds 64KB, the stream silently terminates with an error event. The user sees a truncated response with no clear explanation.

This is Medium rather than High because: (a) the buffer IS bounded (no OOM), (b) the failure mode is stream termination not data corruption, and (c) most provider SSE events are small in practice.

## Recommendation

Add `scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` to all provider SSE readers for consistency with Anthropic. A shared helper would reduce duplication:

```go
func newSSEScanner(r io.Reader) *bufio.Scanner {
    s := bufio.NewScanner(r)
    s.Buffer(make([]byte, 0, 64*1024), 1<<20) // 1MB max line
    return s
}
```

## References

- `pkg/provider/openai.go:104`, `ollama.go:109`, `gemini.go:142`, `mistral.go:91`, `openrouter.go:89`, `openzen.go:96`, `azure_openai.go:109`
- `pkg/provider/anthropic.go:492-494` (reference implementation)
