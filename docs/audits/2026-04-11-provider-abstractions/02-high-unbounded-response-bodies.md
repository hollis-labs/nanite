# [High] Unbounded response body reads across all providers

**Scope:** Provider request construction — all HTTP API providers
**Topic:** Memory & Resources
**Date:** 2026-04-11

## Problem

Every HTTP API provider reads response bodies without size limits. Neither `io.LimitReader` nor `http.MaxBytesReader` is used anywhere in `pkg/provider/`. A malicious or misbehaving API server can send an arbitrarily large response body, causing OOM.

## Evidence

Grep for bounded-read patterns across the provider package:

```
rg 'io\.LimitReader|MaxBytesReader|LimitedReader' pkg/provider/
# (no matches)
```

Unbounded reads found in every provider:

**Error body reads (`io.ReadAll` — no limit):**
- `pkg/provider/anthropic.go:L368` — `errBody, _ := io.ReadAll(resp.Body)`
- `pkg/provider/anthropic.go:L715` — same
- `pkg/provider/openai.go:L90` — same
- `pkg/provider/openai.go:L225` — same
- `pkg/provider/openai.go:L324` — same
- `pkg/provider/ollama.go:L90` — same
- `pkg/provider/gemini.go:L84` — same
- `pkg/provider/gemini.go:L253` — same
- `pkg/provider/gemini.go:L364` — same
- `pkg/provider/mistral.go:L77` — same
- `pkg/provider/mistral.go:L206` — same
- `pkg/provider/mistral.go:L301` — same
- `pkg/provider/azure_openai.go:L95` — same
- `pkg/provider/azure_openai.go:L222` — same
- `pkg/provider/azure_openai.go:L325` — same
- `pkg/provider/openrouter.go:L75` — same
- `pkg/provider/openzen.go:L82` — same
- `pkg/provider/openzen.go:L211` — same

**Non-streaming response decodes (`json.NewDecoder` — no limit):**
- `pkg/provider/anthropic.go:L746` — `json.NewDecoder(resp.Body).Decode(&result)`
- `pkg/provider/openai.go:L236` — same
- `pkg/provider/ollama.go:L208` — same
- `pkg/provider/gemini.go:L266` — same
- `pkg/provider/mistral.go:L217` — same
- `pkg/provider/azure_openai.go:L233` — same
- `pkg/provider/openrouter.go:L215` — same
- `pkg/provider/openzen.go:L222` — same

**Streaming SSE reads (bufio.Scanner — limited but large):**

The SSE readers use `bufio.Scanner` with a 1MB buffer (`scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` in Anthropic; default 64KB in the others). These are bounded per-line, but the stream itself is unbounded in duration and total bytes. The scanner will happily read gigabytes of data line-by-line. This is less severe than `io.ReadAll` (no single allocation spike), but a malicious server sending an infinite stream without a `[DONE]` / `message_stop` event will consume goroutine + connection resources indefinitely.

## Impact

- **Error body reads:** A compromised or buggy API server returning a multi-GB error body causes immediate OOM. `io.ReadAll` allocates a single byte slice that grows to the full response size.
- **Non-streaming response decodes:** `json.NewDecoder` reads incrementally but the decoded struct grows unboundedly if the JSON contains large arrays or strings.
- **SSE streams:** Lower severity per-event, but an infinite stream without termination event ties up a goroutine and HTTP connection indefinitely.

Cross-ref: `2026-04-10-mcp-client-transport` found the same class of unbounded response body in MCP HTTP transports. This is the same pattern, independently present in providers.

## Recommendation

Wrap response bodies in `io.LimitReader` before reading. Suggested limits:

```go
// Error bodies — 1MB is generous for an error response.
const maxErrBody = 1 << 20
errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))

// Non-streaming response bodies — 10MB for completion responses.
const maxRespBody = 10 << 20
limitedBody := io.LimitReader(resp.Body, maxRespBody)
if err := json.NewDecoder(limitedBody).Decode(&result); err != nil { ... }
```

For SSE streams: add a total-bytes-read counter in the scanner loop. If total bytes exceed a threshold (e.g., 100MB), emit an error event and close.

A shared helper like `provider.LimitedReadAll(body, maxBytes)` would centralize this across all 8 providers.

## References

- `2026-04-10-mcp-client-transport` audit — same class finding on MCP HTTP transport.
- Go `io.LimitReader` — stdlib solution for bounded reads.
- CWE-400: Uncontrolled Resource Consumption.
