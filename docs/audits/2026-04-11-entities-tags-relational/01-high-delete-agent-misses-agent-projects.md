# [High] DeleteAgent misses agent_projects cleanup

**Scope:** internal/store/agents.go
**Topic:** FK integrity / Entity deletion
**Date:** 2026-04-11

## Problem

`DeleteAgent` explicitly lists junction tables to clean up but omits `agent_projects`, leaving orphaned rows in the join table after agent deletion.

## Evidence

`internal/store/agents.go:L200-204`:

```go
refs := []string{"session_agents", "agent_modes", "agent_skills",
    "agent_prompt_templates", "agent_mode_assignments"}
for _, table := range refs {
    _, _ = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE agent_id = ?", table), agent.ID)
}
```

The schema defines `agent_projects` at `internal/store/migrations/001_schema.sql:L72-77`:

```sql
CREATE TABLE IF NOT EXISTS agent_projects (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    project_id TEXT NOT NULL REFERENCES projects(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, project_id)
);
```

`agent_projects` is not in the `refs` slice. After `DeleteAgent`, rows in `agent_projects` referencing the deleted agent's ID persist indefinitely.

Additionally, `session_overrides` (from migration 004) is also missing from the cleanup list. The `session_agent_overrides` table has `agent_id` as part of its primary key (`internal/store/migrations/004_session_agent_overrides.sql`).

## Impact

- `ListProjectAgents` (`internal/store/agent_projects.go:L43-65`) JOINs `agent_profiles` to `agent_projects`. Orphaned `agent_projects` rows with a deleted agent_id will silently vanish from JOIN results (no crash), but the rows accumulate as dead data.
- `ListAgentProjects` for the deleted agent ID would return an empty set (the profiles table row is gone), so the orphan is invisible but permanent.
- `session_agent_overrides` rows for the deleted agent similarly become dead data.
- Under sustained agent churn (e.g., automated sync from nanite-native adapter that creates/deletes agents), this can grow unbounded.

## Recommendation

Add `agent_projects` and `session_agent_overrides` to the `refs` slice:

```go
refs := []string{"session_agents", "agent_modes", "agent_skills",
    "agent_prompt_templates", "agent_mode_assignments",
    "agent_projects", "session_agent_overrides"}
```

## References

- Cross-ref: `docs/audits/2026-04-11-store-and-migrations/01-high-delete-agent-disables-foreign-keys.md` -- same function, FK disable bug.
- `internal/store/agent_projects.go` -- the join table CRUD.
- `internal/store/session_overrides.go` -- session override CRUD.
