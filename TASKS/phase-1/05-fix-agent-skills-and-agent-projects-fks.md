# Fix `agent_skills`/`agent_projects` missing `agent_id` FKs

**Phase:** 1
**Status:** not-started
**Depends on:** none hard — `10-data-migrate-nanite-agents-md.md` is out of scope for Phase 1 (operator decision, 2026-08-18; see that file), so this task no longer waits on it. Independently verify the zero-row precondition below before adding the constraint (see Context and What to do #1) — this verification is now the primary safety check, not a fallback.
**Touches:** new migration (`agent_skills.agent_id`, `agent_projects.agent_id` — add real FK constraint; SQLite rename-recreate-copy pattern), `internal/store/skills.go`/`internal/store/projects.go` or wherever these tables' CRUD lives

## Context

Architecture doc `01-agent-construction.md`: *"Real relational references, not free-text strings... `agent_skills` — same FK pattern, against the real skills catalog."* Decision log §4: *"Tool/skill/model references become real foreign keys against live catalogs instead of free-text strings."*

### Both tables, exact current state

`agent_skills` (`internal/store/migrations/001_schema.sql:217-222`): `agent_id TEXT NOT NULL` with an explicit comment `-- no FK: agents may be file-based`, `skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE` (this half is already correct), `config TEXT`. Zero rows workspace-wide (confirmed, matches decision log).

`agent_projects` (`001_schema.sql:73-78`): identical shape — `agent_id TEXT NOT NULL` (`-- no FK: agents may be file-based`), `project_id TEXT NOT NULL REFERENCES projects(id)` (already correct). Architecture doc calls this "the existing precedent" for Scope.

**The reason both are missing the FK**: today, an agent can be purely file-discovered (`Source="project"`/`"user"` per `internal/agent/discovery.go`) with no backing `agent_profiles` row at all until `AutoIngestAgents` successfully ingests it — a real, documented failure mode (`ingest.go`'s own comment: *"A per-definition failure leaves that agent visible via file discovery... but with no backing `agent_profiles` row"*). Adding `agent_id TEXT NOT NULL REFERENCES agent_profiles(id)` while file-based agents without a DB row can still exist would either reject valid rows or require every file-discovered agent to have guaranteed DB backing first.

**Update, 2026-08-18**: this task no longer waits on `10-data-migrate-nanite-agents-md.md` — that task is out of scope for Phase 1 (operator decision: no legacy `.md` files are being migrated; none of the 24 current agents will be run until Phase 1-5 completes). The ingest-failure race above is orthogonal to that migration either way — it was never `10`'s job to close it, and cutting `10` doesn't make it worse. Since both tables are confirmed zero-row today and nothing in the current codebase writes to either, the practical precondition for this task ("no live orphaned reference exists, and nothing currently inserts one") already holds. Do the direct verification in What to do #1 anyway — don't take this note's word for it.

### `agent_skills` vs. `agent_known_skills` — do not confuse (same collision shape as `known_tools`/`agent_known_tools`, see `04`)

`agent_known_skills` (a real, differently-named table — verify exact migration during implementation, referenced via `internal/api/agent_capabilities.go`'s live REST CRUD and `AgentCapabilitiesPanel.tsx`'s GUI, `agent_id` **already** FK'd to `agent_profiles`) is a separate, currently-live per-agent roster/pinning table, explicitly confirmed out-of-scope and untouched by Phase 0's `17-cut-role-skills-legacy.md`. **This task's `agent_skills` is a different table** — the FK-based skill-grant join this architecture describes, currently zero rows, currently missing its `agent_id` FK. Do not touch `agent_known_skills` as part of this task.

## What to do

1. Independently verify the precondition directly against the real backed-up database: confirm `agent_skills` and `agent_projects` are still zero-row, and grep for every current write path into either table to confirm none can insert a row for an `agent_id` lacking a backing `agent_profiles` row. Do not assume this from the note above — check it.
2. Add `agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE` to both `agent_skills` and `agent_projects`, via the SQLite rename-recreate-copy migration pattern (CHECK/FK constraints can't be `ALTER`ed in place) — write this as a real goose migration per Phase 0 #9's adopted convention, not the pre-goose raw-SQL style.
3. Confirm both tables' existing zero-row state means this migration is a pure schema change with no data to preserve/reconcile — verify against the real backed-up database, don't assume.
4. Update `internal/store/agents.go`'s `DeleteAgent` cleanups slice to cascade-clean (or confirm `ON DELETE CASCADE` makes an explicit cleanup line unnecessary) for both tables — check whether explicit cleanup lines exist today despite the missing FK (they may, as defensive code) and reconcile.

## Done means

- `agent_skills.agent_id` and `agent_projects.agent_id` both carry a real, enforced FK to `agent_profiles(id)`.
- No file-based, DB-row-less agent can be referenced by either table (verified by attempting to insert a row for a non-existent agent and confirming the FK rejects it).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Tested against a real copy of the backed-up database.
- `agent_known_skills` is confirmed untouched (regression check).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
