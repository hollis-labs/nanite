# Cut legacy rename-artifact tables (`agent_messages_legacy_089`, `todos_legacy_d1`)

**Phase:** 0
**Status:** not-started
**Depends on:** `09-adopt-goose-migrations`
**Touches:** `internal/store/migrations/090_agent_messages_subagent_result_kind.sql` (renumbered from `089_...` by `09-adopt-goose-migrations`'s duplicate-`051`-prefix resolution — the migration's content and the `agent_messages_legacy_089` table name it produces are unchanged, only the file's version number shifted; see that task's Work Log), `internal/store/migrations/043_scope_project_reminders_pins_todos.sql` (unaffected by the renumber), a new migration (goose-format, per `09-adopt-goose-migrations`'s adopted mechanism) that drops `agent_messages_legacy_089` and `todos_legacy_d1`; no Go code references either table (verified — see Context)

## Context

TASKS.md Phase 0 item 19: "`agent_messages_legacy_089`/`todos_legacy_d1` — after #9 (goose) lands." Decision log §16 ("Confirmed dead, cut"): "`agent_messages_legacy_089`/`todos_legacy_d1` — safely droppable once goose (§13) replaces the swallowed-error idempotency mechanism that's been keeping them around as a side effect." `docs/engineering/architecture/05-storage-and-migrations.md`'s "Confirmed dead, cut" section repeats the same finding.

**Verified origin of both tables** — they are rename artifacts from SQLite's "no in-place `ALTER TABLE ... ALTER COLUMN`/CHECK-widening" limitation, produced by the rename→recreate→copy migration pattern:

- `agent_messages_legacy_089` is created by `internal/store/migrations/090_agent_messages_subagent_result_kind.sql` (renumbered from `089_...sql`, see the Touches line above), which widens `agent_messages.kind`'s CHECK constraint to add `'subagent_result'`. Line 27: `ALTER TABLE agent_messages RENAME TO agent_messages_legacy_089;`, followed by `CREATE TABLE IF NOT EXISTS agent_messages (...)` with the widened CHECK, then `INSERT OR IGNORE INTO agent_messages ... SELECT ... FROM agent_messages_legacy_089`. The migration's own comment block is explicit: "`agent_messages_legacy_089` is intentionally NOT dropped, so subsequent runs find it for the rename-swallow + INSERT-OR-IGNORE no-op path."
- `todos_legacy_d1` is created by `internal/store/migrations/043_scope_project_reminders_pins_todos.sql`, which swaps the `todos.scope` CHECK enum (drops `workspace`, adds `turn`) and adds `project_id`. Line 64: `ALTER TABLE todos RENAME TO todos_legacy_d1;`, same rename→recreate→copy shape, same "intentionally NOT dropped ... small fixed cost preserved only to make this migration safely re-runnable" comment.

Both migrations' comments spell out **why** the legacy table is kept: with no `schema_migrations` ledger (the exact gap `09-adopt-goose-migrations` closes — see decision log §13, `docs/engineering/architecture/05-storage-and-migrations.md`), every migration file re-executes in full on every boot. The `RENAME TO` statement's second-boot failure ("already another table") is swallowed by the migration runner (`internal/store/store.go`, the `migrate:skip-if-column-exists` mechanism), which only works because the legacy table still exists to rename onto. Decision log §13 flags this exact structural pattern — "the identical structural pattern in `043`/`089` remains unguarded" — as the still-live instance of the failure class that caused a prior production crash-loop (`e2273f8`) elsewhere. `09-adopt-goose-migrations` replaces the swallowed-error idempotency mechanism with goose's real version ledger, which is what makes these two legacy tables droppable: once migrations only run once (tracked in `goose_db_version` or equivalent), the rename-swallow trick this code depends on is no longer how re-run safety is achieved, and the legacy tables lose their only reason to exist.

**Confirmed zero live consumers**: `grep -rn "agent_messages_legacy_089\|todos_legacy_d1" --include="*.go" .` returns no matches anywhere in the Go codebase — no store method, no query, no test reads or writes either table. They are pure byproducts of the migration mechanism itself, not application data.

**Hard dependency on `09-adopt-goose-migrations` — why this must not run first**: dropping these tables while the swallowed-rename idempotency mechanism is still the live migration model would break `089`/`043` on the very next boot after this change lands (elsewhere, not on this repo checkout, but on any environment where migrations still re-run from scratch) — the `RENAME TO agent_messages_legacy_089` / `RENAME TO todos_legacy_d1` statements in those two migration files would then attempt to rename onto tables that no longer exist to fail-and-swallow against, changing their error shape and potentially surfacing as a real failure instead of the currently-swallowed "already another table" case. This task must land strictly after goose is adopted and 089/043 (or their goose-migrated equivalents) are re-expressed as one-time, ledger-tracked migrations that no longer depend on the legacy table surviving.

## What to do

1. Confirm `09-adopt-goose-migrations` has landed and merged first — do not start this task otherwise.
2. Under the new goose-based migration mechanism, add a migration that drops both `agent_messages_legacy_089` and `todos_legacy_d1` (`DROP TABLE IF EXISTS <name>` is the safe form, consistent with existing drop precedent in this codebase, e.g. `internal/store/migrations/018_rename_a2a_messages.sql`'s `DROP TABLE IF EXISTS a2a_messages`).
3. Check whether `09-adopt-goose-migrations`'s own work re-expresses the historical migration set (including `043`/`089`) as goose-format `.sql` files with `-- +goose Up`/`-- +goose Down` annotations, or whether it layers goose on top of the existing `internal/store/migrations/*.sql` files unchanged. Either way, this task's new drop-migration must run *after* 089/043 in migration order (by whatever ordering goose uses — versioned filenames) so the legacy tables exist to be dropped, and must not be a no-op replay hazard under goose's real up/down tracking.
4. If goose's adoption changed how `089`/`043` themselves are expressed (e.g. collapsed to a single migration since there is no more production data to preserve via the rename-copy dance), coordinate with what `09-adopt-goose-migrations` actually did rather than assuming the current file shapes described in Context are still literally present — re-verify before writing the drop migration.
5. Verify no Go code references either table before landing (re-run the grep in Context — should still be empty).
6. Test against a real copy of the backed-up database (`~/.local/share/nanite/workspaces/default/backups/`), not just an empty fixture, per `EXECUTION-PROCESS.md`'s migration-testing requirement — confirm the drop succeeds cleanly on a database that actually has both legacy tables populated (from real historical migration runs) and that the live `agent_messages`/`todos` tables are untouched.

## Done means

- `agent_messages_legacy_089` and `todos_legacy_d1` no longer exist after migrations run on a fresh boot.
- The live `agent_messages` and `todos` tables are unaffected — same schema, same row counts, before and after.
- Migration is idempotent under goose's real ledger (runs once, tracked, not swallowed-error-safe by accident).
- Tested against a real backed-up database copy, not just an empty fixture.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- No Go code references either legacy table (re-confirmed after landing).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
