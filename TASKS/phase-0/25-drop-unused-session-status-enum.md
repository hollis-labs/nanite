# Drop unused `sessions.status` CHECK enum values (`sleeping`/`halted`/`terminated`)

**Phase:** 0
**Status:** not-started
**Depends on:** `26-cut-session-compaction-summary-fields` (schema-ordering reason — see Context)
**Touches:** a new migration file that rebuilds the `sessions` table (SQLite cannot ALTER a CHECK constraint in place); `internal/store/migrations/073_agent_profiles_multi_agent.sql` (read-only reference — the migration that introduced the three unused values and established the rebuild pattern to follow); `internal/store/agents.go` (`SetSessionStatusByAgentID`, lines 938–955 — read-only reference, optional bonus cut, see Context)

## Context

TASKS.md Phase 0 **Cuts** section, item 25 (note: TASKS.md has a duplicate-numbering bug — the **Renames** section separately restarts at "25. PTY naming scrub," an unrelated item covered by a different task file. This task is the Cuts-section occurrence only): "`sessions.status`'s unused CHECK enum values (`sleeping`/`halted`/`terminated`, never written by any code path) — drop from the constraint. The real, actively-used `halted_at`/`halted_reason` column pair stays." Decision log §24 ("Cleanup targets, session lifecycle"): "`sessions.status`'s unused CHECK enum values (`sleeping`/`halted`/`terminated`, never written by any code path — apparently leaked vocabulary from `durable_agent_instances.status`) — drop from the constraint. The actively-used `halted_at`/`halted_reason` column pair (a distinct, real mechanism that happens to also mean 'halted') stays untouched." `docs/engineering/architecture/06-session-lifecycle-and-recovery.md`'s Cleanup section repeats the same finding verbatim.

**Verified origin**: `internal/store/migrations/073_agent_profiles_multi_agent.sql` (lines 30–66) is the migration that widened the constraint. Migration 001's original constraint was `CHECK(status IN ('active','paused','archived'))`. Migration 073's own comment explains why: it rebuilt `sessions` via SQLite's rename→recreate→copy pattern (SQLite has no in-place `ALTER TABLE ... ALTER COLUMN`/CHECK-widening) to add three values it attributes to "FU-28 ... multi-agent foundation" wake/sleep/shutdown lifecycle states: `CHECK(status IN ('active','paused','archived','sleeping','halted','terminated'))`. No migration after 073 touches `sessions.status` — 073 is the current, authoritative shape (confirmed: `grep -l "ALTER TABLE sessions" internal/store/migrations/*.sql` finds nothing newer than 073 that changes it).

**Verified zero writers of the three values**, two independent ways:

1. **Go layer**: `grep -rn "UPDATE sessions" --include="*.go" internal/` finds every UPDATE statement against `sessions.status`. The only one that accepts an arbitrary status string is `SetSessionStatusByAgentID` (`internal/store/agents.go:938-955`), doc-commented "Spike-scope helper for FU-28 wake/sleep/shutdown handlers" — i.e., it looks exactly like the intended writer of `sleeping`/`halted`/`terminated`. `grep -rn "SetSessionStatusByAgentID" --include="*.go" .` returns only its own definition and doc comment — **zero call sites anywhere in the codebase**. Every other UPDATE either hardcodes a literal value already in the narrowed set (`'archived'` in `ArchiveSession`, `internal/store/sessions.go:517`) or passes through `Session.Status`, which is only ever set to `""`→`"active"` default or copied from an existing row (`internal/store/sessions.go:434-435, 924-925`).
2. **API layer, independently**: `internal/api/sessions.go:276-284` (`handleUpdateSession`) explicitly whitelists the PATCH `/api/sessions/{id}` status field: `switch *req.Status { case "active", "paused", "archived": ... default: a.errorResp(w, http.StatusBadRequest, "status must be active, paused, or archived") }`. Even if some future caller tried to set `sleeping`/`halted`/`terminated` through the only mutation REST endpoint, it's rejected before it reaches the store layer.

Both checks agree: the three values are structurally unreachable, not just empirically unused.

**Confirmed `halted_at`/`halted_reason` is a separate, real, actively-used mechanism** (`internal/store/session_halt.go`): `MarkSessionHalted`/`ClearSessionHalt`/`GetSessionHalt` read/write these two columns directly — they are the actual "session is halted" signal consumed by the monitor-loop driver's pre-tick check (`HaltStatus.IsHalted()`). This is unrelated to the `status` enum despite the shared English word "halted" — exactly the "leaked vocabulary" collision the decision log calls out. **This task must not touch `halted_at`/`halted_reason` in any way.**

**Bonus finding, not mandatory for this task**: `SetSessionStatusByAgentID` (`internal/store/agents.go:938-955`) has zero callers and its only plausible purpose (writing the values this task removes) is now gone. It's a legitimate standing-dead-code-policy candidate, and after this task lands, calling it with any of the three removed values would start failing loudly at the DB layer (a CHECK violation) instead of silently succeeding — arguably a feature, not a regression. It is **not** in TASKS.md item 25's literal scope, so this task does not require removing it; flag it to the Orchestrator as an easy follow-on cut if wanted.

**Why this depends on `26-cut-session-compaction-summary-fields`**: narrowing a CHECK constraint requires a full `sessions` table rebuild (rename→recreate→copy, per 073's established pattern — SQLite cannot ALTER a CHECK constraint in place). The rebuild's `CREATE TABLE sessions_new` must enumerate every current column. `26-cut-session-compaction-summary-fields` drops `compaction_summary`/`compacted_at` via a plain `ALTER TABLE ... DROP COLUMN` (order-independent, works regardless of when it runs). If this task's rebuild runs *before* 26's DROP COLUMN, the rebuild must still include `compaction_summary`/`compacted_at` (they'd still exist at that point) and 26 drops them afterward — safe, but only if migration file numbers are assigned in that exact order. If the two land out of order relative to their actual migration-file numbers, the rebuild could silently resurrect columns 26 already dropped, or omit columns that are still live. Sequencing 26 first removes the ambiguity entirely: by the time this task's rebuild is authored, `compaction_summary`/`compacted_at` are already gone, so the rebuild's column list is simply shorter and there's no ordering hazard to reason about.

## What to do

1. Confirm `26-cut-session-compaction-summary-fields` has landed and merged first.
2. Determine the next available migration number: check `internal/store/migrations/` for the current highest-numbered file at implementation time (093 as of this writing, but will have moved). If `09-adopt-goose-migrations` has landed by then, author this migration in whatever format that task established instead of a plain numbered `.sql` file — check its outcome before assuming the current raw-SQL convention still applies.
3. Read the **current** full `sessions` schema at implementation time (`sqlite3 <db> ".schema sessions"` against a real DB, or trace the migration chain) — do not assume the column list below is still accurate; migrations may have landed between this file being written and being implemented.
4. Write the rebuild migration following migration 073's exact pattern (`internal/store/migrations/073_agent_profiles_multi_agent.sql`, lines 44-105): `PRAGMA foreign_keys = OFF;` outside any transaction, `BEGIN;`, `CREATE TABLE IF NOT EXISTS sessions_new (...)` with the narrowed constraint `status TEXT DEFAULT 'active' CHECK(status IN ('active','paused','archived'))`, `INSERT INTO sessions_new SELECT <every column> FROM sessions;`, `DROP TABLE sessions;`, `ALTER TABLE sessions_new RENAME TO sessions;`, recreate the three indexes (`idx_sessions_workspace`, `idx_sessions_pinned`, `idx_sessions_status`), `END;`, `PRAGMA foreign_keys = ON;`. Do **not** touch `halted_at`/`halted_reason` — copy them through unchanged, same as every other column.
5. Do not modify `internal/api/sessions.go`'s existing status whitelist (lines 276-284) — it's already correct and needs no change; note this in the work log so the reviewer doesn't go looking for a missed update.
6. Test against a real copy of the backed-up database (`~/.local/share/nanite/workspaces/default/backups/`), not just an empty fixture, per `EXECUTION-PROCESS.md`'s migration-testing requirement. Confirm any pre-existing rows (none expected, but verify) don't have `status` values outside the new set — if they do, escalate rather than guess how to handle them.

## Done means

- `sessions.status` CHECK constraint is `CHECK(status IN ('active','paused','archived'))` only.
- `sessions.halted_at`/`sessions.halted_reason` are unchanged in type, nullability, and behavior.
- `internal/store/session_halt.go`'s `MarkSessionHalted`/`ClearSessionHalt`/`GetSessionHalt` still work identically (verify with existing tests, none should need changes).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Migration tested against a real backed-up database copy, not just an empty fixture.
- `internal/api/sessions.go`'s status whitelist still only accepts `active`/`paused`/`archived` (unchanged, verified not accidentally touched).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
