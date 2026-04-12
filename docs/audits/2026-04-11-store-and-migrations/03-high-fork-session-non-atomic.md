# [High] ForkSession and CopyMessages are non-atomic multi-step write operations

**Scope:** internal/store/sessions.go
**Topic:** Transaction handling
**Date:** 2026-04-11

## Problem

`ForkSession` performs multiple write operations (create session, copy agents, copy messages) without wrapping them in a single transaction. `CopyMessages` calls `s.CreateMessage` in a loop, where each `CreateMessage` starts its own transaction. A failure partway through leaves the database in an inconsistent state: a forked session with partial agents or partial messages.

## Evidence

```go
// sessions.go:L481-549
func (s *Store) ForkSession(sourceID string, overrides *Session, copyMessages bool) (*Session, error) {
    // ... build newSess ...

    if err := s.CreateSession(newSess); err != nil {    // write 1
        return nil, fmt.Errorf("create forked session: %w", err)
    }

    agents, err := s.ListSessionAgents(sourceID)
    // ...
    for _, sa := range agents {
        if err := s.EnsureSessionAgent(newSess.ID, ...); err != nil {  // write 2..N
            return nil, fmt.Errorf("copy agent %s: %w", sa.AgentID, err)
        }
    }

    if copyMessages {
        if err := s.CopyMessages(sourceID, newSess.ID); err != nil {   // write N+1..N+M
            return nil, fmt.Errorf("copy messages: %w", err)
        }
    }
    return newSess, nil
}
```

```go
// sessions.go:L553-578
func (s *Store) CopyMessages(sourceSessionID, targetSessionID string) error {
    // ...
    for _, m := range msgs {
        newMsg := &Message{...}
        if err := s.CreateMessage(newMsg); err != nil {  // each starts its own tx
            return fmt.Errorf("copy message: %w", err)
        }
    }
    return nil
}
```

Each `CreateMessage` call (sessions.go:L418-453) opens its own `tx, err := s.DB.Begin()`, inserts the message, updates the session's `message_count`, and commits. For a session with 500 messages, that's 500 separate transactions.

## Impact

1. **Partial state on error.** If message copy fails at message 250 of 500, the forked session has 250 messages and a `message_count` of 250. The user sees a half-copied session with no way to distinguish it from a complete one.
2. **Performance.** 500 individual transactions with WAL mode is far slower than a single bulk insert. Each transaction requires an fsync to the WAL file.
3. **`message_count` consistency.** Each `CreateMessage` increments `message_count` by 1 with its own UPDATE. Under concurrent access to the same session (unlikely for fork, but possible), these could conflict.

The cross-audit context notes that "Store uses `database/sql` transactions which are atomic -- verify that all multi-step write operations are properly wrapped in transactions." This is a specific instance where they are not.

## Recommendation

Refactor `ForkSession` to use a single transaction, and make `CopyMessages` accept a `*sql.Tx`:

```go
func (s *Store) ForkSession(sourceID string, overrides *Session, copyMessages bool) (*Session, error) {
    src, err := s.GetSession(sourceID)
    if err != nil {
        return nil, fmt.Errorf("load source session: %w", err)
    }

    tx, err := s.DB.Begin()
    if err != nil {
        return nil, fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback()

    // ... create session in tx ...
    // ... copy agents in tx ...
    // ... copy messages in tx (bulk INSERT) ...

    return newSess, tx.Commit()
}
```

For bulk message copy, use a single INSERT with multiple value sets or at minimum a single transaction wrapping all inserts.

## References

- `CreateMessage` (sessions.go:L418-453) -- starts its own transaction.
- `Seed()` (seed.go:L9-251) -- correctly wraps all seed data in a single transaction. This is the project's own reference for how multi-step writes should work.
