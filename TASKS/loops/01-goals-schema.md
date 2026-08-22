# Goals schema — `goals` table, Go types, store CRUD

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/store/migrations/` (new migration — see numbering note below),
`internal/store/goals.go` (new — `Goal` struct, CRUD), `docs/engineering/GLOSSARY.md` (no
new entries needed — **Goal** already landed this session, see Context).

## Context

Implements the storage half of `docs/engineering/architecture/21-loops.md`'s Decision 2:
Goal is a first-class entity with a real `goals` row and the full lifecycle the design doc
describes (§15) — `DRAFT | DEFINED | ACTIVE | BLOCKED | SATISFIED | FAILED | CANCELLED |
SUPERSEDED` — from day one, not deferred. The design doc's own reasoning, quoted directly:
*"a Goal outliving any single LoopRun (survives an abandon-and-restart, survives a REPLAN,
potentially spans a Team's flex phases and a Loop's iterations under one shared target
state) is load-bearing for how this is meant to be used, not a nice-to-have."* This is a
deliberate divergence from this repo's usual "start narrow, add a table later" bias — an
explicit operator call, not this planning session's own judgment to relitigate.

**`docs/engineering/GLOSSARY.md` already has a Goal entry** (added by the design-alignment
session itself, 2026-08-21) — read it, don't duplicate it:
> "a first-class, persisted target state (`goals` table) with its own lifecycle (`draft` →
> `defined` → `active` → `blocked`/`satisfied`/`failed`/`cancelled`/`superseded`),
> independent of any one execution. A Goal can exist before a Loop launches against it and
> can outlive a single `LoopRun`... See `architecture/21-loops.md`, Decision 2."

**A `loop_runs` row always has exactly one `goal_id` — a `goals` row does not require a
`loop_runs` row.** A goal can exist `DRAFT`/`DEFINED` before any loop launches against it
(authored by a planning session, an architect agent, or an operator). `parent_goal_id`
supports the design doc's §16 decomposition (one goal recomputing its own subgoals as
barriers are discovered) — the column exists here; no engine walks it yet (see this batch's
README, "What this batch does NOT do").

**`LoopLaunchRequest` accepts either an existing `goal_id` or an inline goal spec** (task
`10`), which the launcher upserts into `goals` first — so Ralph-shaped ergonomics don't
require a separate authoring step for the common case, even though storage is always
first-class underneath. Not this task's job to build; just keep the schema shape compatible
(every field the design doc's illustrative `goal:` YAML names must be a real column or a
real JSON sub-structure column).

## What to do

1. **New migration** — confirm the actual next-available migration number at dispatch time
   (`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -8` — `134_agent_profiles_protocol_transport.sql`
   was latest as of this planning session; `TASKS/plugin-system` claims `135`, `TASKS/skills`
   claims `136`-`137`; this batch provisionally claims `138` for this task — **real
   cross-batch collision risk, re-verify at dispatch time**). Illustrative DDL, following
   `internal/store/migrations/128_teams.sql`'s exact "brand-new definition table" shape
   (plain transactional Up/Down, no `PRAGMA foreign_keys`/rebuild dance):
   ```sql
   CREATE TABLE IF NOT EXISTS goals (
       id                       TEXT PRIMARY KEY,
       parent_goal_id           TEXT REFERENCES goals(id),
       intent                   TEXT NOT NULL,
       desired_state_json       TEXT NOT NULL DEFAULT '[]',
       constraints_json         TEXT NOT NULL DEFAULT '[]',
       acceptance_criteria_json TEXT NOT NULL DEFAULT '[]',
       invariants_json          TEXT NOT NULL DEFAULT '[]',
       priority                 TEXT,
       scope                    TEXT,
       status                   TEXT NOT NULL DEFAULT 'draft'
                                CHECK (status IN ('draft','defined','active','blocked',
                                       'satisfied','failed','cancelled','superseded')),
       owner                    TEXT,
       source                   TEXT,
       created_at               TEXT NOT NULL DEFAULT (datetime('now')),
       activated_at             TEXT,
       completed_at             TEXT
   );
   CREATE INDEX idx_goals_parent ON goals(parent_goal_id);
   CREATE INDEX idx_goals_status ON goals(status);
   ```
   Document your own call if you deviate.

2. **Go types + store CRUD**, `internal/store/goals.go`, matching the current settled
   convention (confirmed live this session against `internal/store/teams.go` and
   `internal/store/agent_schedules.go`): ctx-taking functions, insert-time
   `uuid.New().String()` ID generation if `ID` is empty, JSON-blob columns as plain Go
   `string` with typed accessor pairs (not `json.RawMessage`) plus a Go-layer validator for
   the `status` enum (mirrors `internal/store/teams.go`'s `validateTeamSlots` pattern):
   - `Goal` struct mirroring the columns above.
   - Typed accessors for the four JSON sub-structures (`DesiredState()`/`SetDesiredState()`,
     `Constraints()`/`SetConstraints()`, `AcceptanceCriteria()`/`SetAcceptanceCriteria()`,
     `Invariants()`/`SetInvariants()`) — each a `[]string` unless the design doc's own
     illustrative shape (§ "Illustrative shape" in `21-loops.md`) implies otherwise; the
     doc's example shows plain string lists for all four, keep them that shape unless a
     later task's real consumption needs more structure.
   - `CreateGoal`, `GetGoal(id)`, `ListGoals(filter ...)` (at minimum: filter by `status`,
     filter by `parent_goal_id`), `UpdateGoal` (full mutable-column replace, mirrors
     `teams.go`'s `UpdateTeam` breadth — `goals` has no independently-evolving runtime
     columns beyond `status`/`activated_at`/`completed_at`, which get their own narrow
     updater), `UpdateGoalStatus(id, status, timestamp fields as applicable)` (narrow,
     mirrors `agent_schedules.go`'s `UpdateAgentScheduleStatus` — sets `activated_at` when
     transitioning into `active`, `completed_at` when transitioning into a terminal status),
     `DeleteGoal`.
   - `ErrGoalNotFound` sentinel, same pattern as `ErrTeamNotFound`/`ErrAgentScheduleNotFound`.
   - Go-layer `validateGoalStatus` (enum check) and a transition guard is optional for this
     task — a bare enum-membership check is sufficient; real transition-legality
     enforcement (e.g. rejecting `draft` → `satisfied` directly) is task `08`'s job once the
     engine is the thing driving transitions, not this storage layer's.

## Done means

- Migration applies cleanly against a real backup copy of the database (per
  `EXECUTION-PROCESS.md`'s schema-migration testing requirement — copy to an isolated
  scratch path, never operate on the real backup in place), not just an empty fixture.
- `Goal` CRUD round-trips correctly in a regression test, including all four JSON
  sub-structure columns and a `parent_goal_id` self-reference.
- `ErrGoalNotFound` returned on every read/update/delete against an unknown ID.
- This task is storage-only — no engine, launcher, or evidence-walk logic is wired here
  (that's tasks `07`/`08`/`10`). `go build ./cmd/nanite/`, `go vet ./...`,
  `go test ./...` pass with no dangling reference.

## Work log

**Migration number.** Re-verified at dispatch time per the assignment's own instruction:
`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -8` showed `134_agent_profiles_protocol_transport.sql`
as the highest number actually present in this worktree (the `worktree-loops-batch`/loops-only
worktree never received the `plugin-system`/`skills` batches' migrations, which are isolated in
their own worktrees) — so the task file's provisional `138` did not apply here. Used **135**
(later renumbered to **138** to resolve a real collision with the `skills` batch's landed
`136`/`137` — see `TASKS/ESCALATIONS.md`):
`internal/store/migrations/138_goals.sql`.

**What was built:**
- `internal/store/migrations/138_goals.sql` — `CREATE TABLE goals` exactly per the task's
  illustrative DDL (self-referencing `parent_goal_id` FK, four JSON sub-structure columns each
  defaulting to `'[]'`, `status` CHECK enum, `idx_goals_parent`/`idx_goals_status` indexes), plain
  transactional Up/Down matching `128_teams.sql`'s "brand-new definition table" shape (no
  `PRAGMA foreign_keys`/rebuild dance — nothing to preserve). Added `IF NOT EXISTS` on the two
  indexes (128_teams.sql's own precedent for its unique index) though the task's illustrative DDL
  didn't include it — harmless, and consistent with the rest of the file already using
  `IF NOT EXISTS` on the table itself.
- `internal/store/goals.go` — `Goal` struct mirroring every column; `GoalStatus*` constants plus
  `validateGoalStatus` (Go-layer enum check, same "validation over a JSON/CHECK-backed column"
  approach `teams.go`'s `validateTeamSlots`/`agent_schedules.go`'s status switch already use);
  typed `[]string` accessor pairs for all four JSON sub-structure columns
  (`DesiredState`/`SetDesiredState`, `Constraints`/`SetConstraints`,
  `AcceptanceCriteria`/`SetAcceptanceCriteria`, `Invariants`/`SetInvariants`) sharing one
  decode/encode helper pair since all four are the identical shape; `ErrGoalNotFound` sentinel;
  `CreateGoal` (insert-time `uuid.New()` ID gen if empty, JSON-column `'[]'` defaults replicated
  Go-side per `CreateTeam`'s own documented reason — every column is passed explicitly so SQLite's
  DDL `DEFAULT` never actually fires); `GetGoal`; `ListGoals(ctx, GoalFilter{Status, ParentGoalID})`
  (both filters from the task's "at minimum" list, `AND`-composed, ordered by `created_at ASC`);
  `UpdateGoal` (full replace of every mutable "definition" column — `parent_goal_id`, `intent`,
  the four JSON columns, `priority`, `scope`, `owner`, `source` — deliberately *not*
  `status`/`activated_at`/`completed_at`, which are the "independently-evolving runtime columns"
  the task called out as needing their own narrow updater); `UpdateGoalStatus(ctx, id, status)`
  (narrow — validates the enum, sets `activated_at` via `COALESCE(activated_at, now)` on transition
  into `active` so a redundant call never clobbers the first activation time, sets `completed_at`
  the same COALESCE way on transition into any of the four terminal statuses
  `satisfied`/`failed`/`cancelled`/`superseded` — `blocked` is treated as paused-not-terminal and
  does not set `completed_at`, matching the design doc's Decision 2 framing of `blocked` as a
  resumable state, not an end state); `DeleteGoal`.
- `internal/store/goals_test.go` — `TestGoal_RoundTrip` (full CRUD including all four JSON
  columns, a `parent_goal_id` self-reference, `UpdateGoal`'s definition-only replace confirmed not
  to touch `status`, `UpdateGoalStatus`'s `activated_at`/`completed_at` side effects including the
  "redundant call doesn't clobber" case and the "blocked doesn't set completed_at" case),
  `TestGoal_ParentFKEnforced` (unknown `parent_goal_id` rejected at insert; deleting a
  still-referenced parent rejected — this codebase runs `PRAGMA foreign_keys=1`, confirmed live
  rather than assumed), `TestGoal_DefaultsAndNotFound` (JSON `'[]'` defaults;
  `ErrGoalNotFound` on every read/update/status-update/delete against an unknown ID, per Done
  means), `TestGoal_StatusValidation` (Go-layer rejection), `TestGoal_StatusCheckConstraint`
  (DB-level CHECK rejection via a raw INSERT bypassing the Go layer entirely — mirrors this
  package's own `TestAgentSchedule_ScheduleKindCheckRejectsRetiredValues` "confirm the DB, not
  just the Go layer, enforces this" discipline).
- `internal/store/migration138_goals_backup_test.go` —
  `TestRealBackupGoalsMigrationAppliesCleanly`, matching this package's established real-backup-test
  convention exactly (`migration_130_workflow_run_steps_flex_kind_test.go`'s
  `TestRealBackupWorkflowRunStepsSurviveFlexKindMigration`: copies the real backup file into
  `t.TempDir()`, never opens it in place; skips rather than fails when the backup isn't present on
  the running machine). Confirmed against a real copy of
  `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
  (343 pre-existing `sessions` rows, confirming a real populated schema, not an empty fixture) that
  migration 138 applies cleanly alongside every other live table, and that full Goal CRUD
  (including the `parent_goal_id` FK and `UpdateGoalStatus`'s `activated_at` side effect) works on
  top of the resulting schema.

**Deviations from the task file, both documented above and repeated here for visibility:**
1. Migration number `135` at dispatch time (since renumbered to `138`; see the Work Log's
   "Migration number" note), not the task's provisional `138` — expected and instructed
   cross-batch-numbering re-verification, not a real deviation.
2. `UpdateGoal` excludes `status`/`activated_at`/`completed_at` from its replace set (routed
   through `UpdateGoalStatus` instead) — this is a literal reading of the task's own parenthetical
   ("goals has no independently-evolving runtime columns beyond status/activated_at/completed_at,
   which get their own narrow updater"), not an invented narrowing.
3. Added `IF NOT EXISTS` to the two new indexes in the migration (task's illustrative DDL omitted
   it) — cosmetic, matches `128_teams.sql`'s own index precedent, no behavior change.

**Incident during this task, fully resolved, no data lost:** while confirming `go vet ./...`'s two
pre-existing `internal/service/container.go` failures were unrelated to this change, I ran
`git stash` (reported "No local changes to save" — this task's new files are untracked, so nothing
of mine was stashed) followed by `git stash pop`, which popped an old, unrelated, pre-existing
stash entry already sitting in this worktree (`stash@{0}: "wip: parallel phase-0/2-9 reorg
(pre-merge stash for phase-1-execution merge)"`, a `main`-branch WIP with no relationship to this
task or this worktree's `HEAD`). That partially applied (three-way merge conflicts in
`TASKS/INDEX.md`, `docs/engineering/architecture/09-plugin-system.md`, and a rename/delete
conflict on `TASKS/phase-7/01-rename-pty-naming-scrub.md`, plus one stray untracked file). Because
the pop could not fully complete (an unrelated untracked-file restore conflict), git did not drop
the stash entry — confirmed via `git stash list` before and after that it was still present and
unchanged. Recovery: `git checkout HEAD -- <the three conflicted paths>` to discard the merge
conflict markers and restore each to its pre-pop `HEAD` content, `rm` the one stray untracked file
the pop had introduced, then confirmed `git diff HEAD --stat` was empty and `git stash list` still
showed all 8 entries including the untouched `stash@{0}`. Verified via `git status --short`
afterward that the worktree was back to exactly its pre-incident state (only this task's three new
files untracked). No data lost; the pre-existing stash entry was never touched beyond git's own
failed, no-op pop attempt. Lesson for future sessions in this worktree: don't reach for
`git stash`/`git stash pop` to "protect" untracked new files before a diagnostic command — they're
untracked and unaffected by any of the commands run here regardless; `git stash` was unnecessary in
the first place.

**Baseline checks:** `go build ./cmd/nanite/` passes. `go vet ./...` passes for everything this task
touched (`go vet ./internal/store/...` is clean); the two pre-existing failures in
`internal/service/container.go` (`stopRuntimeReaper`/`stopReaper` possible-context-leak lints) are
unrelated to this task — confirmed this file was never touched by this task's changes, and those
same two failures are present on a byte-for-byte-identical `internal/service/container.go` outside
this task's diff. `go test ./...` passes in full (every package `ok`, exit code 0) — `internal/store`
specifically at 29.073s including all six new Goal tests. Migration tested against a real, isolated
scratch copy of a production backup (never the real backup file in place), per EXECUTION-PROCESS.md.

## Review notes

**PASS.** Fresh review (no shared context with the implementer), covering: task file + Work
Log, `docs/engineering/architecture/21-loops.md` Decision 2 and its "Illustrative schema"
section, `docs/engineering/GLOSSARY.md`'s Goal/Loop/LoopRun entries, the full diff
(`internal/store/migrations/138_goals.sql`, `internal/store/goals.go`,
`internal/store/goals_test.go`, `internal/store/migration138_goals_backup_test.go`), and a
convention comparison against `internal/store/teams.go`/`internal/store/agent_schedules.go`.

- Schema matches the design doc's illustrative `goals(...)` shape column-for-column: all
  four JSON sub-structure columns, `parent_goal_id` self-reference, the exact 8-value status
  CHECK enum, `priority`/`scope`/`owner`/`source` free-text fields, timestamp triad.
- `goals.go`'s CRUD conventions (ctx-taking functions, insert-time `uuid.New()`, JSON-blob
  columns as plain `string` with typed `[]string` accessor pairs, `nullIfEmpty` reuse from
  `sessions.go`, `ErrGoalNotFound` sentinel, `UpdateGoal`/`UpdateGoalStatus` split) match
  `teams.go`/`agent_schedules.go` precedent exactly, as claimed.
- `UpdateGoalStatus`'s `COALESCE`-based one-time `activated_at`/`completed_at` writes and the
  `blocked`-is-not-terminal exclusion are both correct and tested (redundant-call and
  blocked-doesn't-complete cases both covered in `goals_test.go`).
- `TestGoal_ParentFKEnforced` and `TestGoal_StatusCheckConstraint` correctly verify DB-level
  (not just Go-level) enforcement, per this package's own established discipline.
- `migration138_goals_backup_test.go` genuinely exercises a real backup copy (never in
  place), confirms it's a real populated schema (343 sessions rows), and round-trips full
  Goal CRUD including the FK and status side effects on top of it.
- Migration-number collision with task `06` (both independently landed on `135` in parallel
  worktrees) is accurately documented in this file's Work Log and
  `TASKS/ESCALATIONS.md`; resolution (task `06` renumbered to `136`, both since renumbered
  again to `138`/`139` respectively to resolve the later cross-batch collision with `skills`)
  is correctly reflected in the merged tree.
- `go build ./cmd/nanite/`, `go vet ./internal/store/...`, and `go test ./...` all verified
  green independently (full suite, not just `internal/store`). The two `go vet` findings in
  `internal/service/container.go` are confirmed pre-existing/unrelated (that file isn't
  touched by this task's diff).

No issues found in this task's own diff. (A stale "migration 135" doc-comment reference was
found in `internal/agentworkflow/types.go`, added by task `06`'s commit, not this task's —
reported separately against `TASKS/loops/06-stepkindloop-schema.md`.)
