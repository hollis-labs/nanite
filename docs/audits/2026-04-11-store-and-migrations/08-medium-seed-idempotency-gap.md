# [Medium] Seed() is not re-run safe -- first provider INSERT lacks OR IGNORE

**Scope:** internal/store/seed.go
**Topic:** Seed idempotency
**Date:** 2026-04-11

## Problem

`Seed()` checks `SELECT COUNT(*) FROM workspaces` and returns early if non-zero, making it appear idempotent. However, if the initial seed partially completed (e.g., workspaces inserted but crash before commit), the `defer tx.Rollback()` on line 22 would roll back the transaction. On the next startup, `Seed()` would re-run (workspaces count is still 0) and succeed.

The real idempotency gap is in `SeedProviders()`. That function is called on every boot (per the docstring "Safe to call on every boot") and correctly uses `INSERT OR IGNORE`. However, `Seed()` uses a mix:

- Lines 31-36: Workspace INSERT with **no** `OR IGNORE` -- would fail on conflict.
- Lines 44-49: Anthropic provider INSERT with **no** `OR IGNORE` -- would fail on conflict.
- Lines 53-60: Model INSERT with **no** `OR IGNORE` -- would fail on conflict.
- Lines 65-71: PTY provider INSERT **with** `OR IGNORE` -- safe.

The inconsistency means if `Seed()` runs on a database where the "fragments-engine" workspace already exists (e.g., user manually created it), the seed fails entirely and returns an error, preventing the rest of the seed data from being inserted.

## Evidence

```go
// seed.go:L31-36
if _, err := tx.Exec(
    "INSERT INTO workspaces (id, name, description) VALUES (?, ?, ?)",
    w.id, w.name, w.desc,
); err != nil {
    return fmt.Errorf("insert workspace %s: %w", w.id, err)
}
```

vs.

```go
// seed.go:L65-71
if _, err := tx.Exec(
    `INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
     VALUES (?, ?, ?, ?)`,
    ptyProviderID, "Claude CLI (PTY)", "pty", "",
); err != nil {
```

## Impact

If a user creates a workspace with ID "fragments-engine" or "personal" before the first seed runs, or if the seed partially succeeds and is re-attempted, the seed fails and the system starts without default providers, models, or settings.

The `count > 0` guard makes this unlikely in normal operation, but edge cases exist: manual DB creation, restore from a partial backup, or testing scenarios.

## Recommendation

Use `INSERT OR IGNORE` consistently for all seed data in `Seed()`, matching the pattern already used in `SeedProviders()`, `SeedBuiltinSkills()`, `SeedBuiltinModes()`, and `SeedBuiltinTemplates()`.

## References

- `SeedProviders()` (seed.go:L255-358) -- correctly uses `INSERT OR IGNORE` throughout.
- `SeedBuiltinSkills()` (skills.go:L283-296) -- correctly uses `INSERT OR IGNORE`.
