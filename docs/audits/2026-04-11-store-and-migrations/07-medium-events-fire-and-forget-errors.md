# [Medium] LogEvent and CountSessionToolCalls silently discard all errors

**Scope:** internal/store/events.go
**Topic:** Error handling / Nil check pattern deviation
**Date:** 2026-04-11

## Problem

`LogEvent` discards the INSERT error entirely. `CountSessionToolCalls` discards the query error and returns 0 on failure. This deviates from the project convention where writes return errors.

## Evidence

```go
// events.go:L17-25
func (s *Store) LogEvent(sessionID, eventType, category, detail, metadata string) {
    // ...
    _, _ = s.DB.Exec(
        `INSERT INTO event_log ...`,
        // ...
    )
}
```

```go
// events.go:L63-70
func (s *Store) CountSessionToolCalls(sessionID string) int {
    var count int
    _ = s.DB.QueryRow(
        `SELECT COUNT(*) FROM event_log WHERE session_id = ? AND event_type = 'tool_call'`,
        sessionID,
    ).Scan(&count)
    return count
}
```

The `LogEvent` function is intentionally fire-and-forget (common for event logging), but `CountSessionToolCalls` is a read that returns a meaningful value used for logic. Returning 0 on DB error could cause incorrect behavior in callers.

## Impact

- `LogEvent`: Low impact. Event logging is best-effort by convention. However, if the DB is full or corrupted, all event logging silently fails with no signal.
- `CountSessionToolCalls`: If the DB is temporarily locked (e.g., long write transaction), this returns 0 instead of the actual count. Any caller using this count for rate limiting, display, or budget decisions will make incorrect decisions.

This is a deviation from the project convention documented in `backend.md`: "reads return empty/safe defaults, writes return errors." `LogEvent` is a write that doesn't return an error. `CountSessionToolCalls` is a read that masks errors as zero.

## Recommendation

For `LogEvent`, the fire-and-forget pattern is acceptable for event logging, but add a log-on-error:

```go
func (s *Store) LogEvent(sessionID, eventType, category, detail, metadata string) {
    if metadata == "" {
        metadata = "{}"
    }
    if _, err := s.DB.Exec(...); err != nil {
        log.Printf("warn: event_log insert failed: %v", err)
    }
}
```

For `CountSessionToolCalls`, return an error:

```go
func (s *Store) CountSessionToolCalls(sessionID string) (int, error) {
    var count int
    err := s.DB.QueryRow(...).Scan(&count)
    if err != nil {
        return 0, fmt.Errorf("count session tool calls: %w", err)
    }
    return count, nil
}
```

## References

- `backend.md` "Anti-Patterns / Resolved" section: "Inconsistent nil checks -- Deliberate pattern now: reads return empty/safe defaults, writes return errors."
