# [Low] PTY/subprocess parse errors log raw CLI output lines

**Scope:** PTY goroutine lifecycle
**Topic:** Security
**Date:** 2026-04-11

## Problem

When a PTY or subprocess adapter fails to parse a line of CLI output, the raw line is logged verbatim. CLI output may contain user prompts, tool results, or other sensitive content that should not appear in application logs.

## Evidence

`pkg/provider/pty.go:L150`:
```go
log.Printf("pty[%s]: parse error: %v (line: %s)", p.adapter.Name(), err, string(line))
```

`pkg/provider/subprocess.go:L136`:
```go
log.Printf("subprocess[%s]: parse error: %v (line: %s)", s.adapter.Name(), err, string(line))
```

Both log the full `string(line)` content. Lines can be up to 1MB (per the scanner buffer size at `pty.go:L132` and `subprocess.go:L116`).

## Impact

- **Sensitive content in logs.** User prompts containing credentials, personal information, or confidential code could appear in application logs.
- **Large log entries.** A 1MB line logged verbatim creates an outsized log entry that can disrupt log aggregation pipelines.
- Low severity because this only triggers on parse failures (the adapter's `ParseLine` returns an error), which should be rare in normal operation.

## Recommendation

Truncate the logged line and redact potentially sensitive content:

```go
const maxLogLine = 200
preview := string(line)
if len(preview) > maxLogLine {
    preview = preview[:maxLogLine] + "..."
}
log.Printf("pty[%s]: parse error: %v (line preview: %s)", p.adapter.Name(), err, preview)
```

## References

- `pkg/provider/pty.go:L102-103` — note that the launch log already avoids logging arguments: `log.Printf("pty[%s]: launching CLI with %d args", ...)`. The parse error log should follow the same pattern.
