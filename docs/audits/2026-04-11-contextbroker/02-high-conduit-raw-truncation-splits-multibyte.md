# [High] Conduit source raw-response truncation can split multi-byte UTF-8 characters

**Scope:** contextbroker
**Topic:** Error Handling / Correctness
**Date:** 2026-04-11

## Problem

When the Conduit MCP response is not valid JSON, `ConduitSource.parseResult` truncates it by byte index (`raw[:budget*4]`). Go strings are UTF-8, and slicing by byte offset can split a multi-byte character, producing invalid UTF-8. The resulting `ContextItem.Content` will contain a partial character at the end, which can cause downstream rendering issues, encoding errors, or garbled text in the system prompt.

## Evidence

```go
// internal/contextbroker/source_conduit.go:L118-L123
if err := json.Unmarshal([]byte(raw), &response); err != nil {
    // If it's not JSON, treat the whole response as a single item.
    tokens := EstimateTokens(raw)
    if tokens > budget {
        raw = raw[:budget*4]
    }
```

`budget*4` is a byte count derived from the `(len+3)/4` token heuristic. If `raw` contains multi-byte characters (CJK text, accented characters, emoji), slicing at an arbitrary byte boundary can land in the middle of a UTF-8 sequence.

Example: a string ending with `"\xe4\xb8\xad"` (Chinese character) sliced at offset that includes only the first two bytes produces `"\xe4\xb8"`, which is invalid UTF-8.

## Impact

Occurs only on the non-JSON fallback path, which is a degraded-mode path (Conduit returned something unexpected). The truncated content flows into `FormatPacket` and then into the system prompt. Invalid UTF-8 in a system prompt can cause provider API errors (most LLM APIs validate UTF-8) or, at best, garbled text.

Severity is High because the fix is trivial and the failure mode (broken API call on an already-degraded path) compounds the original error.

## Recommendation

Use a rune-aware truncation. The simplest fix:

```go
if tokens > budget {
    runes := []rune(raw)
    maxRunes := budget * 4 // approximate; rune count != byte count but close enough
    if maxRunes < len(runes) {
        raw = string(runes[:maxRunes])
    }
}
```

Or, since the token estimate is `(len+3)/4` using byte length, and the goal is to respect the token budget, convert to runes and truncate to approximately `budget` tokens worth of runes.

## References

- `internal/contextbroker/source_conduit.go:L118-L123` — byte-offset truncation
- `internal/contextbroker/broker.go:L264-L266` — `EstimateTokens` uses `len(text)` (byte length)
