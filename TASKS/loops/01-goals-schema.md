# Goals schema — `goals` table, Go types, store CRUD

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
