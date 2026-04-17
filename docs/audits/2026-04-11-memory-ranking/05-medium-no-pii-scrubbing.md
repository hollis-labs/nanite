# [Medium] No PII or secret scrubbing before memory storage

**Scope:** memory / extraction
**Topic:** Security
**Date:** 2026-04-11

## Problem

When a user message triggers memory extraction (via `HasMemorySignal` + LLM extraction), the raw user message content is sent to the utility LLM for extraction, and the LLM's extracted summary/body is stored verbatim in the Conduit memory store. There is no scrubbing step that detects and redacts secrets, API keys, passwords, PII (email addresses, phone numbers, SSNs), or other sensitive content before storage.

## Evidence

The extraction prompt includes the raw user content:

```go
// internal/memory/extraction.go:L159-L172
prompt := fmt.Sprintf(`Extract a memory from this user message. ...

User message:
%s

Return a JSON object with these fields: ...`, truncateForPrompt(content, 1500))
```

The extracted content is stored directly:

```go
// internal/memory/extraction.go:L211-L221
m := Memory{
    Namespace:  namespace,
    MemoryKey:  extracted.MemoryKey,
    Summary:    extracted.Summary,
    Body:       extracted.Body,
    // ...
}

if err := e.service.Store(context.Background(), m); err != nil {
```

No scrubbing, redaction, or sensitive-content detection exists anywhere in the `internal/memory/` package. Grep for `scrub|redact|sanitize|secret|password|api.key|pii` in `internal/memory/` returns zero hits.

## Impact

If a user pastes an API key, password, database connection string, or PII into a chat message that contains a memory signal word (e.g., "remember this API key: sk-..."), the secret will be:

1. Sent to the utility LLM (third-party API call)
2. Stored in the Conduit SQLite database at `~/.conduit/data/index/context.db`
3. Potentially surfaced in future context assembly for other sessions

The memory store persists across sessions (by design), so a secret stored once remains accessible indefinitely until manually deprecated.

## Recommendation

Add a pre-storage scrubbing step. Minimum viable approach:

1. Pattern-based detection for common secrets (API keys, passwords, connection strings, JWT tokens):

```go
var sensitivePatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token|bearer)\s*[:=]\s*\S+`),
    regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`),          // OpenAI keys
    regexp.MustCompile(`(?i)(postgres|mysql|mongodb)://[^\s]+`), // connection strings
}

func scrubSensitive(text string) string {
    for _, pat := range sensitivePatterns {
        text = pat.ReplaceAllString(text, "[REDACTED]")
    }
    return text
}
```

2. Apply before storage in both `extractPerTurn` and `extractPostCompact`.

3. Consider adding a `sensitivity_check` instruction to the LLM extraction prompt: "If the message contains API keys, passwords, or credentials, set confidence to 0 and do not include them in the memory."

## References

- `internal/memory/extraction.go:L159-L172` -- raw content in prompt
- `internal/memory/extraction.go:L211-L221` -- unscrubed store
- `.nanite/agents/reviewer-backend.md:L153` -- "PII leakage in memory extraction" called out as review target
