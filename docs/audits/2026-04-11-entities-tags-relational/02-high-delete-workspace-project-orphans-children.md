# [High] DeleteWorkspace and DeleteProject orphan child records

**Scope:** internal/store/workspaces.go
**Topic:** FK integrity / Entity deletion
**Date:** 2026-04-11

## Problem

`DeleteWorkspace` and `DeleteProject` issue bare `DELETE FROM` statements without cleaning up child records that reference them via foreign keys. SQLite FK enforcement is ON, so these deletes will fail silently or raise FK constraint errors depending on whether child rows exist.

## Evidence

`internal/store/workspaces.go:L96-103`:

```go
func (s *Store) DeleteWorkspace(id string) error {
    _, err := s.DB.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
    if err != nil {
        return fmt.Errorf("delete workspace %s: %w", id, err)
    }
    return nil
}
```

`internal/store/workspaces.go:L171-178`:

```go
func (s *Store) DeleteProject(id string) error {
    _, err := s.DB.Exec(`DELETE FROM projects WHERE id = ?`, id)
    if err != nil {
        return fmt.Errorf("delete project %s: %w", id, err)
    }
    return nil
}
```

The schema defines FK references from:
- `projects.workspace_id` -> `workspaces(id)` (`001_schema.sql:L19`)
- `sessions.workspace_id` -> `workspaces(id)` (`001_schema.sql:L88`)
- `sessions.project_id` -> `projects(id)` (`001_schema.sql:L89`)
- `agent_projects.project_id` -> `projects(id)` (`001_schema.sql:L74`)
- `workflows.workspace_id` -> `workspaces(id)` (`001_schema.sql:L464`)

None of these FKs have `ON DELETE CASCADE`. With `PRAGMA foreign_keys=ON` (set at `internal/store/store.go:L46`), deleting a workspace that has projects or sessions will fail with a foreign key constraint error. The error is propagated to the caller, so it won't silently corrupt data -- but the delete simply won't work if child records exist.

## Impact

- Deleting a workspace with any projects, sessions, or workflows will fail with a cryptic SQLite FK error returned to the API caller.
- Deleting a project with sessions or agent_project links will similarly fail.
- The API handler (`handleDeleteWorkspace`, `handleDeleteProject`) will return a 500 with the raw SQLite error message, which leaks implementation details.
- No data corruption occurs (FKs prevent it), but the feature is broken for any workspace/project that has been used.

## Recommendation

Two options (pick one):

**Option A (recommended): Explicit cascading delete.**
Wrap each delete in a transaction that removes child records first, similar to `DeleteAgent`:

```go
func (s *Store) DeleteWorkspace(id string) error {
    tx, err := s.DB.Begin()
    if err != nil { return fmt.Errorf("begin tx: %w", err) }
    defer tx.Rollback()

    // Delete children in dependency order
    tx.Exec("DELETE FROM agent_projects WHERE project_id IN (SELECT id FROM projects WHERE workspace_id = ?)", id)
    tx.Exec("DELETE FROM sessions WHERE workspace_id = ?", id)
    tx.Exec("DELETE FROM workflows WHERE workspace_id = ?", id)
    tx.Exec("DELETE FROM projects WHERE workspace_id = ?", id)
    if _, err := tx.Exec("DELETE FROM workspaces WHERE id = ?", id); err != nil {
        return fmt.Errorf("delete workspace %s: %w", id, err)
    }
    return tx.Commit()
}
```

**Option B: Add `ON DELETE CASCADE` to FK definitions.** Requires a migration to recreate the tables (SQLite does not support `ALTER TABLE ... ADD CONSTRAINT`).

## References

- `internal/store/migrations/001_schema.sql:L6-27` -- workspace/project table definitions.
- `internal/store/store.go:L46` -- FK enforcement pragma.
- Cross-ref: `docs/audits/2026-04-11-store-and-migrations/01-high-delete-agent-disables-foreign-keys.md` -- same pattern class (incomplete entity deletion).
