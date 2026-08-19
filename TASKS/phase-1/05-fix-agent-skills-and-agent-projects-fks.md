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

**Status: escalated, no FK added.** Independent verification (What to do #1) found a real, live write path for both tables — see below. Per this task's own instruction ("If the verification in step 2 fails... STOP — do not add the FK — and report back... This is a real 'genuine unknown', not something to default past"), stopped before touching schema or `internal/store/agents.go`.

**Setup note (unrelated to the schema question, logged for the record):** this worktree's branch (`worktree-agent-ab3913d7cdc9593ca`) had been created from `origin/main` at `df71e710`, not from `phase-1-execution` as the dispatching context stated — `TASKS/phase-1/` didn't exist in the checkout at session start. Confirmed `df71e710` is a strict ancestor of `phase-1-execution` (`505f1f8f`, 73 commits ahead, zero commits unique to my branch), so fast-forwarded (`git merge --ff-only phase-1-execution`) to pick up the missing Phase 1 corpus. No history was rewritten or discarded.

### Verification performed

1. **Row counts, real backed-up DB.** Copied `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726` (the most recent backup) + its `-wal` sidecar to scratchpad, ran `PRAGMA wal_checkpoint(FULL)` to fold the WAL in, then queried: `agent_skills` = 0 rows, `agent_projects` = 0 rows, `agent_profiles` = 28 rows, `agent_known_skills` = 13 rows (confirms the separate live table referenced in Context is untouched and has real data, as expected). The zero-row precondition holds.

2. **Write-path grep, both tables, whole repo (`.go`/`.sql`/`.ts`/`.tsx`, migrations excluded from the "current write path" question, test files excluded).** Two real INSERT paths exist per table, each reachable from a live, mounted HTTP route with no auth/role gate beyond whatever the general API middleware chain applies:
   - **`agent_skills`**: `internal/store/skills.go`'s `AssignSkillToAgent` (`INSERT OR IGNORE INTO agent_skills`), called from `internal/api/skills.go`'s `handleAssignAgentSkill`, mounted at `POST /api/agents/{id}/skills` (`internal/api/api.go:324`). This handler *does* call `a.Services.Agents.Get(ctx, agentID)` first as an existence check — but `AgentService.Get` (`internal/service/agent.go:94`) resolves file-based agents (`agent.IsFileBasedID`/`"file-<slug>"` canonical IDs, `internal/agent/convert.go`) through the **in-memory `fileDefs` slice** (`findDefBySlug`/`findDefByID` → `d.ToProfile()`) *before* ever touching the DB. `AutoIngestAgents`'s own doc comment (`internal/service/ingest.go:33-41`) states plainly: "A per-definition failure leaves that agent visible via file discovery (`agentServiceImpl.List/Get` read `fileDefs` directly) but with no backing `agent_profiles` row... CW-20260815-0009 found exactly this happen." So the existence check in `handleAssignAgentSkill` passes for exactly the class of agent that has no `agent_profiles` row, and the subsequent `AssignSkillToAgent` call would insert `agent_skills.agent_id = "file-<slug>"` with nothing backing it in `agent_profiles`. This is a real, currently-reachable path, not a hypothetical.
   - **`agent_projects`**: `internal/store/agent_projects.go`'s `AddAgentProject` (`INSERT OR IGNORE INTO agent_projects`), called from `internal/api/agents.go`'s `handleAddAgentProject`, mounted at `POST /api/agents/{id}/projects` (`internal/api/api.go:128`). This handler is worse: it performs **no existence check at all** on `agentID` before inserting — any string in the URL path becomes `agent_projects.agent_id` directly, ingest-failure edge case or not.

3. **`DeleteAgent` cleanup slice** (`internal/store/agents.go:391-453`, read for What to do #4 context even though not reached): `agent_skills` already has an explicit defensive cleanup line (`"DELETE FROM agent_skills WHERE agent_id = ?"`, line 413) with a comment explaining the junction tables are FK-less by design because they may reference file-based agents. `agent_projects` has **no** cleanup line in this slice at all — a second, independent piece of evidence that today's code treats `agent_projects.agent_id` as legitimately able to hold non-`agent_profiles`-backed values (nothing cleans it up on agent delete, presumably because nothing currently populates it in practice, but the write path exists regardless).

**Conclusion:** the practical precondition this task requires ("no live orphaned reference exists, and nothing currently inserts one") does **not** hold. Both tables have a real, mounted, reachable write path that can insert an `agent_id` with no backing `agent_profiles` row — `agent_skills` via the documented ingest-failure race, `agent_projects` via a complete absence of any agent-existence check. Adding `agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE` as specified would either start rejecting these live requests outright (a behavior change with no code-path fix accompanying it) or require closing the ingest-failure gap / adding an existence check to `handleAddAgentProject` first — neither of which is in this task's stated scope. Per the task's step 4, stopping here rather than guessing past it. **No migration was written, no schema was changed, `internal/store/agents.go` was not touched, and `agent_known_skills` was not touched** (confirmed untouched — verified only by read, never written to, during this session).

**Baseline sanity check (no code changes made, so this is informational only):** `go build ./cmd/nanite/` passes; `go test ./...` passes across all packages; `go vet ./...` reports two pre-existing findings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible-context-leak) — confirmed pre-existing on the `phase-1-execution` tip (`505f1f8f`) prior to any change made in this session (working tree was clean when `go vet` was run), unrelated to this task and not touched.

**Recommendation for whoever picks this back up:** either (a) scope in the two live-write-path gaps as a precondition sub-task — close the ingest-failure race for `agent_skills` (e.g. `handleAssignAgentSkill` should check the DB row directly, not `AgentService.Get`'s file-def-resolving semantics) and add an equivalent real existence check to `handleAddAgentProject` — before re-attempting the FK, or (b) explicitly accept that file-based/ingest-pending agents lose write access to these two endpoints as an intentional side effect of adding the FK, and get that traded off decided rather than defaulted into.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
