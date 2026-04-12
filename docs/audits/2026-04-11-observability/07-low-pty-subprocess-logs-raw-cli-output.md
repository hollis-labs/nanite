# [Low] PTY and subprocess parse errors log raw CLI output lines

**Scope:** Observability — PII in logs
**Topic:** PII in log fields
**Date:** 2026-04-11

## Problem

When PTY or subprocess adapters fail to parse a line from a CLI agent's stdout, they log the raw line verbatim. CLI agent output can contain user conversation content, tool results with file contents, or error messages from the CLI's own provider that may include API keys or tokens.

## Evidence

`pkg/provider/pty.go:L150`:

```go
log.Printf("pty[%s]: parse error: %v (line: %s)", p.adapter.Name(), err, string(line))
```

`pkg/provider/subprocess.go:L136`:

```go
log.Printf("subprocess[%s]: parse error: %v (line: %s)", s.adapter.Name(), err, string(line))
```

The `line` variable is raw bytes from the CLI agent's stdout. This could contain:
- User message content (if the CLI echoes it)
- LLM response content (if the CLI outputs it in a non-JSON format)
- Error messages from the CLI's provider that may include request/response details
- The `provider-abstractions` audit finding 07 notes that Anthropic error bodies are forwarded verbatim and may contain API key context — if a PTY-bridged CLI echoes such an error, it lands in this log line

## Impact

Low severity because: (1) parse errors are infrequent in normal operation, (2) logs go to stderr which is typically local, (3) the content is from the CLI agent's stdout which is already on the local machine. However, if logs are forwarded to a remote aggregator, raw CLI output lines could contain PII or secrets.

## Recommendation

Truncate the logged line to a fixed length and redact known sensitive patterns:

```go
line := scanner.Bytes()
logLine := string(line)
if len(logLine) > 200 {
    logLine = logLine[:200] + "...(truncated)"
}
log.Printf("pty[%s]: parse error: %v (line_preview: %s)", p.adapter.Name(), err, logLine)
```

Also applies to `internal/chat/decomposer.go:87` which logs raw LLM response on parse failure:

```go
log.Printf("decomposer: failed to parse LLM response: %v (raw: %s)", err, raw)
```

## References

- `pkg/provider/pty.go:L150`
- `pkg/provider/subprocess.go:L136`
- `internal/chat/decomposer.go:L87`
- `provider-abstractions` audit finding 07 — error body may leak key
- `provider-abstractions` audit finding 08 — PTY parse errors log raw line (same finding, different audit scope)
