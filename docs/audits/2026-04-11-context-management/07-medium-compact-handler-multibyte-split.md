# [Medium] Compact handler splits multi-byte UTF-8 at 2000-char boundary

**Scope:** context-management
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

The MVP compact handler at `sessions.go:L268` slices message content at a byte offset without checking for multi-byte rune boundaries:

```go
summary += m.Content[:2000-total]
```

If the 2000th byte falls in the middle of a multi-byte UTF-8 character (e.g., emoji, CJK, accented characters), the resulting `summary` string contains a broken rune.

## Evidence

```go
// internal/api/sessions.go:262-273
// MVP: concatenate all message contents, truncate to 2000 chars.
var total int
var summary string
for _, m := range messages {
    if total+len(m.Content) > 2000 {
        summary += m.Content[:2000-total]
        total = 2000
        break
    }
    summary += m.Content + "\n"
    total += len(m.Content) + 1
}
```

`len(m.Content)` returns byte count, not rune count. The slice `m.Content[:2000-total]` operates on bytes.

## Impact

Compaction summaries stored in the session row may contain invalid UTF-8. Downstream JSON serialization will either produce invalid JSON or replacement characters. The `contextbroker` audit (2026-04-11) flagged the same pattern in `source_conduit.go` and `source_session.go` (findings 02 and 07).

## Recommendation

Replace byte-based slicing with rune-aware truncation. The slot system's `truncateSlot()` in `window.go:L175-L187` already implements this pattern correctly:

```go
for maxBytes > 0 && !utf8.RuneStart(s.Content[maxBytes]) {
    maxBytes--
}
```

Apply the same pattern here. This is a one-line fix.

## References

- `internal/api/sessions.go:L268` — byte-based slice
- `internal/context/window.go:L175-L187` — correct rune-aware truncation (available but unused by this path)
- `docs/audits/2026-04-11-contextbroker/02-high-conduit-raw-truncation-splits-multibyte.md` — same pattern flagged in contextbroker
