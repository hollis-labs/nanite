# [High] DeleteAgent disables foreign keys process-wide without synchronization

**Scope:** internal/store/agents.go
**Topic:** Transaction handling / Security
**Date:** 2026-04-11

## Problem

`DeleteAgent` disables foreign key enforcement with `PRAGMA foreign_keys=OFF` on the shared `*sql.DB` connection pool, then re-enables it in a `defer`. This affects all concurrent queries on the connection pool, not just the transaction.

## Evidence

```go
// internal/store/agents.go:L192-194
s.DB.Exec("PRAGMA foreign_keys=OFF")
defer s.DB.Exec("PRAGMA foreign_keys=ON")

tx, err := s.DB.Begin()
```

The PRAGMA is executed on the `*sql.DB` (connection pool), not on the `*sql.Tx`. In SQLite via `database/sql`, PRAGMA statements executed on the pool can land on any connection. Meanwhile, the `defer` to re-enable runs *after* the function returns, meaning:

1. Any concurrent write on a different goroutine that acquires a connection from the pool during this window may bypass FK constraints.
2. If `DeleteAgent` returns an error before the transaction commits, the PRAGMA OFF was applied but no useful work was done, yet the window was open.
3. The `defer` re-enable races with concurrent operations.

Additionally, errors from both PRAGMA calls are silently discarded (`s.DB.Exec` without error check).

## Impact

During the window between FK-OFF and FK-ON (however brief), any concurrent INSERT or UPDATE on the same `*sql.DB` pool could violate referential integrity. In practice, the window is short and the code is correct in what it deletes, but the pattern is architecturally dangerous:

- A future developer copying this pattern for a longer-running operation creates a real data-integrity hole.
- The `fmt.Sprintf("DELETE FROM %s WHERE agent_id = ?", table)` call on line 205 uses string interpolation for the table name. The `table` values come from a hardcoded slice on line 202, so this is NOT injectable today -- but it establishes a pattern that bypasses the project's consistent use of parameterized queries.

## Recommendation

Run the PRAGMAs on the transaction's connection, not on the pool. Since `database/sql` doesn't expose per-connection PRAGMAs easily, the better approach is to avoid disabling FKs entirely:

```go
func (s *Store) DeleteAgent(slug string) error {
    agent, err := s.GetAgentBySlug(slug)
    if err != nil {
        return nil
    }

    tx, err := s.DB.Begin()
    if err != nil {
        return fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback()

    // Delete from junction tables (no FK back to agent_profiles)
    for _, q := range []string{
        "DELETE FROM session_agents WHERE agent_id = ?",
        "DELETE FROM agent_modes WHERE agent_id = ?",
        "DELETE FROM agent_skills WHERE agent_id = ?",
        "DELETE FROM agent_prompt_templates WHERE agent_id = ?",
        "DELETE FROM agent_mode_assignments WHERE agent_id = ?",
    } {
        if _, err := tx.Exec(q, agent.ID); err != nil {
            return fmt.Errorf("cleanup %s: %w", q, err)
        }
    }

    // Nullify agent_id on messages
    if _, err := tx.Exec("UPDATE messages SET agent_id = NULL WHERE agent_id = ?", agent.ID); err != nil {
        return fmt.Errorf("nullify messages: %w", err)
    }

    if _, err := tx.Exec("DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
        return fmt.Errorf("delete agent %s: %w", slug, err)
    }

    return tx.Commit()
}
```

Key changes:
1. Remove `PRAGMA foreign_keys=OFF/ON` entirely.
2. Delete from junction tables explicitly (they don't have FKs back to `agent_profiles` -- they use no-FK comments in the schema).
3. Check all errors from cleanup queries instead of discarding them with `_, _`.
4. Use literal SQL strings instead of `fmt.Sprintf` for table names.

## References

- SQLite PRAGMA docs: PRAGMAs are per-connection, not per-transaction. `database/sql` connection pooling makes this unpredictable.
- `001_schema.sql:L62-63` -- `agent_modes` has `-- no FK: agents may be file-based`, confirming no actual FK constraint exists back to `agent_profiles` from these tables.
