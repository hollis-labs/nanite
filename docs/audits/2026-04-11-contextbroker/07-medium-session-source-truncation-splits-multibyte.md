# [Medium] SessionSource message truncation can split multi-byte UTF-8

**Scope:** contextbroker
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

`SessionSource.Fetch` truncates individual messages using a byte-offset slice:

```go
// internal/contextbroker/source_session.go:L56-L58
if len(content) > 500 {
    content = content[:500] + "..."
}
```

`len(content)` returns the byte count, and slicing at byte offset 500 can split a multi-byte UTF-8 character. Same class of bug as finding 02 (Conduit raw truncation).

## Evidence

The code at `source_session.go:L56-L58` as quoted above.

## Impact

A truncated message with a trailing partial UTF-8 character flows into the session context item, then into `FormatPacket`, then into the system prompt. Provider APIs that validate UTF-8 may reject the request. Lower severity than finding 02 because message content is more likely to be ASCII-heavy, but CJK users, emoji in messages, or accented characters will hit this.

## Recommendation

Use rune-aware truncation:

```go
runes := []rune(content)
if len(runes) > 500 {
    content = string(runes[:500]) + "..."
}
```

## References

- `internal/contextbroker/source_session.go:L56-L58` — byte-offset truncation
- Related: finding 02 (same class in `source_conduit.go`)
