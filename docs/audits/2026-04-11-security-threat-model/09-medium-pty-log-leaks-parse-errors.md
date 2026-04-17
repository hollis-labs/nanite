# [Medium] PTY adapter logs raw CLI output lines on parse errors

**Scope:** Provider — trust boundary #7 (PTY bridge input/output), #9 (Environment variables)
**Topic:** Security
**Date:** 2026-04-11

## Problem

When a PTY adapter's `ParseLine` returns an error, the bridge logs the raw line content to the server log. If the CLI subprocess outputs sensitive data (API keys, tokens, user secrets) in a non-JSON format, those values appear in plaintext in the log.

## Evidence

`pkg/provider/pty.go:L148-L152`:

```go
events, err := p.adapter.ParseLine(line)
if err != nil {
    log.Printf("pty[%s]: parse error: %v (line: %s)", p.adapter.Name(), err, string(line))
    continue
}
```

Same pattern in `pkg/provider/subprocess.go:L134-L138`:

```go
events, err := s.adapter.ParseLine(line)
if err != nil {
    log.Printf("subprocess[%s]: parse error: %v (line: %s)", s.adapter.Name(), err, string(line))
    continue
}
```

CLI agents may output non-JSON lines during startup, error conditions, or when they print debug information. These lines fail JSON parse and are logged verbatim. If a CLI prints its environment during an error dump (common in Python tracebacks), API keys in the environment (which the subprocess inherits per finding 02) appear in the log.

## Impact

Sensitive data from CLI subprocess output is written to server logs. If logs are shipped to a centralized logging system, secrets are exposed to anyone with log access.

## Recommendation

1. Truncate the logged line to a maximum length (e.g., 200 characters).
2. Redact the line content through `filterSecrets`-style pattern matching before logging.
3. Or simply omit the raw line content from the log message and log only the error and line length.

## References

- `pkg/provider/pty.go:L148-L152` — PTY bridge parse error logging
- `pkg/provider/subprocess.go:L134-L138` — subprocess bridge parse error logging
- Cross-ref: finding 02 (full env inheritance means CLI has secrets to leak)
