# Goal evidence schema — `goal_evidence` thin pointer table

**Phase:** 1 — Schema & storage foundation (`TASKS/loops`)
**Status:** not-started
**Depends on:** `01-goals-schema.md` (`goal_id` FK)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/goals.go` or a
new `internal/store/goal_evidence.go` (Go types, CRUD).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s Goal-evidence model — a **thin
pointer table, not a duplicate content store**, quoted directly: *"It points into
`workflow_run_steps.verify_json`, a gate's resolution record, or an `event_log` row — the
same 'derived/observability, not new persistent state' discipline Teams applied to routing
provenance."* Goal satisfaction (`goal_met = acceptance_criteria_satisfied AND
constraints_satisfied AND invariants_preserved AND required_evidence_present`, design doc
§7) is computed by walking these pointers, not by trusting a bare status flag — task `07`'s
continuation-policy `decide()` function is the real consumer of this walk.

**This planning session's own decision on the design doc's open "free-text evidence"
question** (see this batch's README, "What this session decided..."): **no** — every row
points at something structured. `ref_table`/`ref_id` are both required (`NOT NULL`), never
a free-text-only row. A human's written acceptance note is captured as a real `event_log`
row first (`ref_table='event_log'`, `ref_id` = that row's ID) and pointed at from here —
`event_log` already exists as an append-only durable log (used by compaction/recovery
events, reflex firings) and is the natural home for a one-off text note, not a new nullable
free-text column on this table.

**Precedent template for this table's shape** — `internal/store/migrations/132_team_authority_grants.sql`
(the most recent "normalized sub-table pointing at a parent definition row" migration):
plain `CREATE TABLE`, one composite index, no rebuild dance.

## What to do

1. **New migration** — re-verify the actual next-available migration number at dispatch
   time (`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -8`); this task
   provisionally claims `139` (after task `01`'s `138`) — **re-check, both cross-batch and
   against `01`'s own actually-landed number if it merged first.** Illustrative DDL:
   ```sql
   CREATE TABLE IF NOT EXISTS goal_evidence (
       id               TEXT PRIMARY KEY,
       goal_id          TEXT NOT NULL REFERENCES goals(id),
       loop_run_id      TEXT,
       iteration_number INTEGER,
       evidence_type    TEXT NOT NULL
                        CHECK (evidence_type IN ('test_suite','verify_result','gate_approval',
                               'human_acceptance','artifact')),
       ref_table        TEXT NOT NULL,
       ref_id           TEXT NOT NULL,
       result           TEXT,
       summary          TEXT,
       recorded_at      TEXT NOT NULL DEFAULT (datetime('now'))
   );
   CREATE INDEX idx_goal_evidence_goal ON goal_evidence(goal_id, evidence_type);
   CREATE INDEX idx_goal_evidence_loop_run ON goal_evidence(loop_run_id, iteration_number);
   ```
   `loop_run_id` is deliberately **not** a hard FK at this table's creation time (task `03`
   builds `loop_runs` in parallel with this task per the README's Wave 2 grouping — both
   depend on `01`, not on each other). If `03` lands first in practice, add the FK
   constraint (`REFERENCES loop_runs(id)`) here; if this task lands first, leave it a plain
   nullable `TEXT` and note the gap for whichever of `03`/`04` lands second to tighten, or
   accept it permanently loose (Goal evidence can exist independent of any `LoopRun` — a
   human-authored acceptance note against a `DEFINED` goal with no loop launched yet is a
   real, valid row with `loop_run_id IS NULL`). Document your actual call.

2. **Go types + store CRUD** — `GoalEvidence` struct mirroring the columns; `RecordGoalEvidence`,
   `ListGoalEvidence(goalID, filters...)` (at minimum: filter by `loop_run_id`, filter by
   `evidence_type`), `DeleteGoalEvidence` (rarely used — evidence is append-only in practice,
   but keep it symmetric with every other CRUD set in this store package). No `Update` —
   evidence rows are immutable once recorded, matching the append-only "one row per firing"
   discipline the design doc's own `loop_run_iterations` illustrative shape uses (task `04`).
   Go-layer `validateGoalEvidence`: `evidence_type` enum membership, `ref_table`/`ref_id`
   both non-empty (enforces the "always structured, never free-text-only" decision above).

3. **A real, callable evidence-walk helper belongs here, not deferred to task `07`** — a
   `EvidenceSatisfiesGoal(ctx, goalID string) (bool, []GoalEvidence, error)`-shaped function
   (exact signature is your call) that walks recorded evidence and reports whether
   `acceptance_criteria_satisfied AND constraints_satisfied AND invariants_preserved AND
   required_evidence_present` holds, per the design doc's §7 formula — this task builds the
   *query*, task `07` is the one that *calls* it inside `decide()`. Keep the four-clause
   evaluation logic in this package (`internal/store` or wherever `goal_evidence.go` lands)
   since it's a pure read over this table plus `goals`' own `acceptance_criteria_json`/
   `constraints_json`/`invariants_json` — no engine dependency needed to compute it.

## Done means

- Migration applies cleanly against a real backup copy of the database (isolated scratch
  path, per `EXECUTION-PROCESS.md`).
- `GoalEvidence` CRUD round-trips correctly, including a row with `loop_run_id IS NULL`
  (goal-only evidence, no loop yet) and a row with both `loop_run_id`/`iteration_number` set.
- `validateGoalEvidence` rejects a row missing `ref_table` or `ref_id`.
- `EvidenceSatisfiesGoal` (or equivalent) is unit-tested against at least: zero evidence
  (not satisfied), partial evidence covering only some acceptance criteria (not satisfied),
  and full coverage of all four clauses (satisfied) — using a real `Goal` row's
  `acceptance_criteria_json`/etc., not a hardcoded stub.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
