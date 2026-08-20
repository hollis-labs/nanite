# `team_run_members` — the one real new table the design doc names explicitly

**Phase:** 1 — Schema & storage foundation (`TASKS/teams`)
**Status:** implemented
**Depends on:** none directly (independent table; parallel-safe with `01`/`03`/`04`/`05` — different file/table surface, coordinate migration numbering only, see `01`'s numbering note)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/team_run_members.go` (new).

## Context

`docs/engineering/architecture/15-teams.md`'s "Slot resolution and the one genuinely new persistence table" section is explicit and singular about this: *"The one real new table this design needs is the resolution record itself — `team_run_members` (`workflow_run_id`, `slot_name`, `agent_id`, `session_id`, `resolved_at`, `status`), the same shape as the existing `session_agents` junction table, scoped to a run instead of a session."* Its "New mechanism vs. reuse" ledger confirms: everything else a naive reading might turn into new tables (`team_run_routes`/`_messages`/`_tasks`/`_decisions`) should **not** be built — those are filtered views over `agent_messages`/`event_log`/`agent_reflexes` firings, not new state (see task `09`'s Context for the provenance-trace mechanics this implies).

**Correction to the "same shape as `session_agents`" claim, found during this planning session's research — real, not cosmetic.** `session_agents` (`internal/store/migrations/001_schema.sql:108-114`) is: `session_id TEXT NOT NULL REFERENCES sessions(id)`, `agent_id TEXT NOT NULL` (comment: "no FK: agents may be file-based" — **this comment is itself now stale**, post the file-based-agent-elimination work tracked at `TASKS/INDEX.md`'s `adhoc/01-eliminate-file-based-agent-runtime` — every agent is a uniformly-addressed real `agent_profiles` row now; do not copy the stale no-FK rationale into this new table's schema without re-deciding it fresh), `mode TEXT DEFAULT 'default'`, `joined_at`, `is_primary BOOLEAN DEFAULT FALSE`, `PRIMARY KEY (session_id, agent_id)`. It has **no `status` column and no run-scoping** — its PK is `(session_id, agent_id)`, not `workflow_run_id`-anything. `team_run_members` is a same-*family* shape (a junction table binding an agent+session to a context), not a literal column copy — build it to the design doc's own explicit five/six-column list above, not to `session_agents`'s actual columns.

**Explicitly not this task's job to resolve** (the design doc flags this itself, deliberately, as unresolved): *"`team_run_members` is one-to-one only for singleton slots. A slot with `min`/`max` > 1 ... makes `@engineer` ambiguous — route to one, broadcast to all, or address a specific concrete member — and the doc does not resolve this."* This task only needs to store **N rows per `(workflow_run_id, slot_name)`** correctly — no uniqueness constraint should force a multi-member slot into one row. Task `09` makes and documents the addressing-default call (see that task's Context) against whatever this table allows.

## What to do

1. New migration (confirm the actual next-available number at dispatch time — see `01`'s numbering note; this batch provisionally sequences its migrations after `01`'s). Illustrative DDL, per the design doc's own explicit column list:
   ```sql
   CREATE TABLE team_run_members (
       id                TEXT PRIMARY KEY,
       workflow_run_id   TEXT NOT NULL REFERENCES workflow_runs(id),
       slot_name         TEXT NOT NULL,
       agent_id          TEXT NOT NULL REFERENCES agent_profiles(id),
       session_id        TEXT NOT NULL REFERENCES sessions(id),
       resolved_at       TEXT NOT NULL DEFAULT (datetime('now')),
       status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','failed','replaced','stopped'))
   );
   CREATE INDEX idx_team_run_members_run_slot ON team_run_members(workflow_run_id, slot_name);
   ```
   Deliberately **no unique constraint** on `(workflow_run_id, slot_name)` — this is what allows a `concurrent`-activation-mode slot (design doc's `engineer: min:1, max:4`) to resolve to multiple rows. Document your `status` vocabulary if you deviate from the four above — `08`'s slot-resolution logic and `09`'s routing-failure handling both read this column and need to agree with whatever you land on (check whether either task is already in flight before diverging).

2. **Go types + store CRUD**, `internal/store/team_run_members.go`:
   - `TeamRunMember` struct mirroring the columns above.
   - `InsertTeamRunMember`, `ListTeamRunMembersByRun(runID string) ([]TeamRunMember, error)`, `ListTeamRunMembersBySlot(runID, slotName string) ([]TeamRunMember, error)`, `UpdateTeamRunMemberStatus(id, status string) error`.

## Done means

- Migration applies cleanly against a real backup copy of the database, not just an empty fixture.
- Regression test: insert 3 `team_run_members` rows for one `(workflow_run_id, slot_name)` pair (simulating a resolved `concurrent`, `max:4`-capable slot with 3 live members) and confirm `ListTeamRunMembersBySlot` returns all 3, correctly distinguished from a different slot's rows in the same run.
- This task is storage-only — no slot-resolution logic is wired here (that's task `08`). `go build`/`vet`/`test` clean.

## Work log

Implemented as specified, with the migration numbering, ID-generation, and
context-plumbing conventions of the surrounding codebase rather than the
task's illustrative signatures taken literally. Summary:

- **Migration number used: `129`** (`internal/store/migrations/129_team_run_members.sql`).
  Re-verified via `ls internal/store/migrations/ | sort -t_ -k1 -n | tail -10`
  immediately before writing the file — `127_schedule_runs_and_retry_policy.sql`
  was still the latest on disk, so `129` (as pre-assigned by the dispatch —
  `128` reserved for task `01`, `130` for `03`, `131` for `05`) was free and
  used as assigned. No renumbering needed.
- **DDL**: matches the task's illustrative DDL exactly — same five/six
  columns, same `status` CHECK vocabulary (`active`/`failed`/`replaced`/
  `stopped`, no deviation), same deliberate absence of a unique constraint
  on `(workflow_run_id, slot_name)`, same `idx_team_run_members_run_slot`
  index. Verified the three FK targets (`workflow_runs`, `agent_profiles`,
  `sessions`) all exist and predate migration 129 (`001_schema.sql` for the
  latter two, `051_agent_workflows.sql` for `workflow_runs`), and that this
  codebase runs with `PRAGMA foreign_keys=1` (confirmed via
  `internal/store/consumers_test.go`'s own comment and `store.go`'s
  `sqlitekit.WriterOptions()`), so the FK declarations are real, enforced
  constraints, not decorative. Used a plain `+goose Up`/`+goose Down`
  (`DROP TABLE IF EXISTS team_run_members`) — no `PRAGMA foreign_keys =
  OFF` / rename-recreate-copy dance, since this is a brand-new table with
  no existing rows to migrate forward (same precedent as
  `126_selftool_reactions.sql`, not `127`'s rebuild-in-place pattern).
- **Go types + CRUD** (`internal/store/team_run_members.go`): `TeamRunMember`
  struct mirrors the six columns 1:1. Implemented all four functions the
  task names — `InsertTeamRunMember`, `ListTeamRunMembersByRun`,
  `ListTeamRunMembersBySlot`, `UpdateTeamRunMemberStatus` — plus one small
  addition not named by the task, `GetTeamRunMember(ctx, id)`, a natural
  single-row read-back complement `UpdateTeamRunMemberStatus`'s own
  regression test needs and a cheap thing to have for task `08`/`09`'s
  future use; flagging it here since it wasn't explicitly asked for.
  **Deviation from the task's illustrative Go signatures**: every function
  takes a leading `context.Context` and the two `List*`/`Insert`/`Update`
  signatures use `(ctx, ...)` rather than the task's bare
  `ListTeamRunMembersByRun(runID string) ([]TeamRunMember, error)` shape —
  this matches the actual, current convention in every sibling store file
  in this batch's neighborhood (`internal/store/schedule_runs.go`,
  `internal/store/selftool_reactions.go`, both from the immediately
  preceding two batches), not the older non-context style some earlier
  files in the package still carry (e.g. `workflow_runs.go`). `InsertTeamRunMember`
  returns `(*TeamRunMember, error)` (not a bare `error`) so the caller gets
  back the generated `ID`/defaulted `Status`/DB-computed `ResolvedAt`
  without a second round-trip — same pattern `CreateScheduleRun` and
  `InsertTeamRunMember`'s own read-back-after-insert already use elsewhere
  in this package. `UpdateTeamRunMemberStatus` validates `status` against
  the CHECK-constraint vocabulary before writing (returns a typed Go error
  for an invalid value instead of surfacing a raw sqlite constraint
  violation) and returns `ErrTeamRunMemberNotFound` (not a silent no-op)
  when `id` doesn't match any row — again, matching `schedule_runs.go`'s
  own `RecordScheduleRunAttempt` precedent, the closest existing sibling to
  this table's own status-update shape.
- ID generation uses `uuid.New().String()` (not a prefixed ULID) when the
  caller leaves `ID` empty — matches the majority convention in this
  package (`agents.go`'s `CreateAgent`, `artifacts.go`'s `CreateArtifact`,
  `selftool_reactions.go`'s `InsertSelftoolReaction`), not `schedule_runs.go`'s
  `"sr-" + ulid.Make().String()` outlier.
- **Regression test** (`internal/store/team_run_members_test.go`):
  `TestTeamRunMember_ListBySlot_ConcurrentSlotMultipleMembers` is the exact
  scenario "Done means" asks for — 3 rows inserted for one
  `(workflow_run_id, "engineer")` pair (3 distinct agents/sessions, same
  run, same slot), plus a 4th row for a different slot (`"reviewer"`) in
  the same run, then asserts `ListTeamRunMembersBySlot(run, "engineer")`
  returns exactly those 3 (by ID) and `ListTeamRunMembersBySlot(run,
  "reviewer")` returns exactly the 1 reviewer row, not a mix. Three
  supporting tests round out the CRUD surface:
  `TestTeamRunMember_InsertAndListByRun`, `TestTeamRunMember_UpdateStatus`
  (covers both the not-found and invalid-status-value error paths), and
  `TestTeamRunMember_InsertValidation` (missing required-field rejection).
  Added a `makeTestWorkflowRun` test helper (mirrors the existing
  `makeTestAgent`/`makeTestSession` helpers) since no such helper existed
  yet in the package.
- **Schema migration tested against a real backup DB copy, not just an
  empty fixture**, per `EXECUTION-PROCESS.md`'s requirement. Copied
  `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
  to an isolated scratch path (under this session's scratchpad directory,
  never the real backup file in place, never a CWD-relative path) and ran
  a throwaway verification program (`cmd/tmp-migration-check-129/main.go`,
  deleted immediately after use — not part of the shipped diff) that:
  called `store.New(ctx, <absolute scratch path>)` against that copy
  (confirming migration 129 — and every migration up to it — applies
  cleanly on top of the real, pre-goose-ledger production schema, going
  through this codebase's `isPreGooseDatabase`/`seedLegacyLedger` cutover
  path, not a synthetic fresh-DB path); confirmed `team_run_members`
  exists; pulled a **real** `agent_profiles.id`/`sessions.id` row already
  present in that backup (rather than inserting synthetic rows into other
  tables of a real backup copy) to satisfy the two real FK constraints;
  inserted 3 `team_run_members` rows against a synthetic-but-real-insert-path
  `workflow_runs` row (via the actual `CreateWorkflowRun`, not raw SQL);
  confirmed `ListTeamRunMembersBySlot` returned all 3 with no unique-
  constraint collision; and round-tripped `UpdateTeamRunMemberStatus`. All
  steps passed (`VERIFICATION PASSED`). Ran `git status --short`
  immediately after (per `EXECUTION-PROCESS.md`'s explicit instruction to
  do so right after any such verification, not just before a final
  commit) — clean, only this task's three new files present, no stray
  write landed on any real tracked file or the backup itself.
- **Baseline checks**: `go build ./cmd/nanite/` — clean. `go vet
  ./internal/store/...` — clean (`go vet ./...` repo-wide reports two
  pre-existing findings in `internal/service/container.go`, both possible-
  context-leak warnings unrelated to and untouched by this task — confirmed
  via `git log --oneline -1 -- internal/service/container.go`, last touched
  by a prior scheduling-batch commit, not this change). `go test ./...`
  (full repo, `-count=1`) — clean, no `FAIL` anywhere in output.
- **No `docs/engineering/GLOSSARY.md` changes made by this task** — the
  "Team"/"Team Slot"/"TeamRun" glossary entries (including the
  `internal/context/slot.go` `SlotOrder` disambiguation) are explicitly
  task `01`'s `Touches` responsibility, not this task's; this task's own
  `Touches` list names only the migration and `team_run_members.go`. New
  Go identifiers introduced here (`SlotName` field, not a bare `Slot`) are
  already disambiguated in spelling, consistent with the naming concern
  task `01`'s context raises, without this task needing to own the
  glossary edit itself.
- No escalations. No deviation from the task's DDL/status vocabulary/table
  shape.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
