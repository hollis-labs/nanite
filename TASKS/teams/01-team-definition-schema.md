# Team definition schema — `teams` table, Go types, store CRUD

**Phase:** 1 — Schema & storage foundation (`TASKS/teams`)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/store/migrations/` (new migration — see numbering note below), `internal/store/teams.go` (new — `Team` struct, `TeamSlotDefinition` struct, CRUD), `docs/engineering/GLOSSARY.md` (new entries: Team, Team Slot, TeamRun — see disambiguation requirement below).

## Context

Implements the storage half of `docs/engineering/architecture/15-teams.md`'s "Team" concept — a saved, named, reusable organizational shape (slots, authority, routing, phase sequence). The design doc's own "Runtime overrides follow the existing cascade" section is explicit that this must be persistable and callable by name: *"A caller (Loom, or any Nanite consumer) should be able to launch against a saved Team by name with invocation-time overrides (`engineers.max: 3`) without creating a new persistent Team definition per call, matching how `WorkflowLaunchRequest.Params` already works today."* That requires a durable `teams` row to launch "against" — this task builds it. Nothing in the design doc's own text names this table explicitly (its "one real new table" claim is about `team_run_members`, a *run's* resolved members — see task `02`) — this is a planning-session addition, filling a gap the design doc's own scope assumes exists but never assigns.

**The illustrative Team shape to store** (`15-teams.md`'s SME example, quoted in full there):
```
slots: { architect: {...}, orchestrator: {...}, engineer: {...}, reviewer: {...} }
authority: { orchestrator.may_spawn: [...], engineer.may_message: [...], reviewer.may_not_review: self }
routing: { architecture_question -> architect, otherwise -> orchestrator }
```
plus a phase/gate sequence (the doc's "Illustrative shape" compiled `WorkflowDefinition` example: `scope_work` (flex) → `review_gate` (gate) → `address_feedback` (flex) → `merge_gate` (gate)).

**Forward-compat requirement, from the design doc's own "What this session did not decide" list**, not optional: *"Implementation should keep the compile-Team-phase-sequence-into-WorkflowDefinition step as one clean function/module boundary with the phase sequence treated as its own addressable sub-structure within the Team definition, even though authored together for v1 — so a later split is 'accept a phase sequence from a second source' rather than a rewrite of the compiler itself."* Concretely: the phase/gate sequence must live in its own column/JSON sub-structure, not commingled into `slots`. Task `07` (the compiler) depends on this separation existing.

**A real, unaddressed naming-collision risk, found during this planning session's research (not mentioned by `15-teams.md`, whose own "Naming decisions made this session" section checked "Scope"/"Action"/"Coordination" for exactly this failure mode but never checked "Slot"):** `internal/context/slot.go` already defines `SlotOrder` — the Context Broker's own "slot" concept, documented in this project's `CLAUDE.md` as one of six load-bearing invariants ("universal slot at position 0") and enforced by `internal/service/slot_invariants_test.go`. This is a completely different, already-established, already-load-bearing meaning of "slot" than a Team Slot (an organizational role like "engineer" or "reviewer"). Per `GLOSSARY.md`'s own stated purpose (this codebase has a documented history of exactly this failure mode), this must be resolved before any Team Slot code lands, not after.

**Authority/routing/`team_run_members` shapes are not this task's job to finalize** — `authority_json`/`routing_json` exist here only as a storage placeholder; task `04` owns the real authority-grant shape (and may migrate `authority_json` into its own normalized table if it makes that call — coordinate, don't duplicate). Task `09` owns the real routing-rule shape. This task's Go types for those two fields should be minimally typed (or left as `json.RawMessage`) rather than guessing their final shape.

## What to do

1. **New migration** — confirm the actual next-available migration number at dispatch time (`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -5`; `127_schedule_runs_and_retry_policy.sql` was latest as of this planning session, 2026-08-20 — every task in this batch provisionally claims `128` onward in sequence, but **this is a real cross-batch collision risk**, same as the scheduling/harness-reactive-self-tools batches hit each other — whichever of this batch's migration-adding tasks is dispatched, or whichever other in-flight batch lands first, must re-list the directory and renumber). Illustrative DDL:
   ```sql
   CREATE TABLE teams (
       id              TEXT PRIMARY KEY,
       name            TEXT NOT NULL,
       description     TEXT,
       slots_json      TEXT NOT NULL DEFAULT '[]',
       authority_json  TEXT NOT NULL DEFAULT '[]',
       routing_json    TEXT NOT NULL DEFAULT '[]',
       phases_json     TEXT NOT NULL DEFAULT '[]',
       created_at      TEXT NOT NULL DEFAULT (datetime('now')),
       updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
       created_by      TEXT
   );
   CREATE UNIQUE INDEX idx_teams_name ON teams(name);
   ```
   Document your own call if you deviate — e.g. normalizing `authority_json`/`routing_json` into real tables now instead of later, if you judge the shape is already settled enough. If you do, coordinate with tasks `04`/`09` rather than shipping a column those tasks then have to remove.

2. **Go types + store CRUD**, `internal/store/teams.go`:
   - `Team` struct mirroring the columns above.
   - `TeamSlotDefinition` struct, decoded from `slots_json`, modeled directly on the SME example: `Name string`, `RoleSlug string`, `Resolution string` (`"durable"|"fresh"` — see task `08`'s Context for the corrected meaning of "durable" here, which is **not** a literal read of `agent_profiles.durable`), `AgentID *string` (nullable — the design doc's `agent_id: nanite-architect` field, present only for durable slots), `ActivationMode string` (matches `agent_profiles.activation_mode`'s real three values: `singleton|fresh-per-wake|concurrent`), `Required bool`, `Min int`, `Max int`.
   - Leave `authority_json`/`routing_json`/`phases_json` as `json.RawMessage` or minimally-typed placeholders — tasks `04`, `09`, `07` define and consume their real shapes without needing this task revisited.
   - `CreateTeam`, `GetTeam(id)`, `GetTeamByName(name)`, `ListTeams`, `UpdateTeam`, `DeleteTeam`.

3. **Naming disambiguation, required, not optional:** always spell out **"Team Slot"** in new Go identifiers, table/column names, and docs — never a bare `Slot`/`slot` type or column name. (`slots_json` as a field name is fine — it's descriptive prose, not a type — but the Go struct is `TeamSlotDefinition`, not `Slot`.) Add `Team`, `Team Slot`, and `TeamRun` entries to `docs/engineering/GLOSSARY.md`, explicitly cross-referencing `internal/context/slot.go`'s `SlotOrder`/`internal/context/INVARIANTS.md` and stating that "slot" alone is ambiguous in this codebase going forward — always qualify.

## Done means

- Migration applies cleanly against a real backup copy of the database (per `EXECUTION-PROCESS.md`'s schema-migration testing requirement), not just an empty fixture.
- `Team` CRUD round-trips correctly in a regression test, including all four JSON sub-structure columns and a `TeamSlotDefinition` with every field populated.
- `docs/engineering/GLOSSARY.md` has real entries for Team / Team Slot / TeamRun, including the Context-Broker-`Slot` disambiguation.
- This task is storage-only — no compiler, launcher, or authority-enforcement logic is wired here (that's tasks `07`/`08`/`04`). `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass with no dangling reference.

## Work log

**Migration numbering.** Re-verified immediately before writing the file, per this task's own instruction: `ls internal/store/migrations/ | sort -t_ -k1 -n | tail -5` showed `127_schedule_runs_and_retry_policy.sql` as latest in both the shared checkout and this worker's own worktree — no collision, used `128` as pre-assigned. New file: `internal/store/migrations/128_teams.sql`.

**What was built:**
- `internal/store/migrations/128_teams.sql` — `CREATE TABLE teams` matching the task's illustrative DDL exactly (no column changes), plus `CREATE UNIQUE INDEX idx_teams_name ON teams(name)`. Brand-new table, so a plain transactional Up/Down (no `NO TRANSACTION`/rename-recreate-copy dance) — matches migration 126's precedent, not 124/127's (those rebuilt an *existing* table with rows to preserve).
- `internal/store/teams.go` — `Team` struct mirroring the table columns; `TeamSlotDefinition` struct (`Name`, `RoleSlug`, `Resolution`, `AgentID *string`, `ActivationMode`, `Required`, `Min`, `Max`) decoded from `slots_json` via `Team.Slots()`/`Team.SetSlots()`; `CreateTeam`, `GetTeam`, `GetTeamByName`, `ListTeams`, `UpdateTeam`, `DeleteTeam`, all `ctx`-taking (matches the most recent CRUD convention in this package — `agent_schedules.go`/`selftool_reactions.go` — over the older non-ctx `roles.go` style). `ErrTeamNotFound` sentinel, same pattern as `ErrAgentScheduleNotFound`.
- `internal/store/teams_test.go` — regression tests: full CRUD round-trip including all four JSON sub-structure columns and a `TeamSlotDefinition` with every field populated (both a durable slot with `AgentID` set and a fresh/elastic slot with `AgentID` nil, `Min`/`Max` distinct) per "Done means"; `idx_teams_name` UNIQUE-constraint enforcement; JSON-column defaults (`"[]"`) on an otherwise-empty create; `ErrTeamNotFound` on unknown id/name for every read/update/delete path; `SetSlots` validation rejection (bad `Resolution`, bad `ActivationMode`, inverted `Min`/`Max`, empty slot `Name`) both via the typed helper and via a directly-assigned raw `SlotsJSON` string routed through `CreateTeam`'s own validation call.
- `docs/engineering/GLOSSARY.md` — added **Team**, **Team Slot**, **TeamRun** entries (placed after **Scope**, before **Consumer**, since Team is topically the Role→Agent pattern one level up). The **Team Slot** entry explicitly cross-references `internal/context/slot.go`'s `SlotOrder` and `internal/context/INVARIANTS.md` ("universal slot at position 0" invariant) and states plainly that "slot" alone is ambiguous in this codebase going forward and must always be qualified (**Team Slot** vs. Context Broker slot/`SlotOrder`). Per the task's own "What to do" item 3, only these three entries were added — no new standalone glossary entry was added for the pre-existing Context Broker slot concept itself (out of this task's stated scope; the cross-reference from the new entries is where the task asked the disambiguation to live).

**Deviations from the task's illustrative DDL/types, both explicitly offered as options by the task file itself:**
1. `authority_json`/`routing_json`/`phases_json` are Go `string` fields, not `json.RawMessage`. The task's own "What to do" section offered both ("Leave ... as `json.RawMessage` or minimally-typed placeholders") — chose the plain-`string` placeholder to match this package's existing convention for every other JSON-blob column (`roles.go`'s `DefaultTools`/`DefaultSkills`/`DefaultPermissions`, `agent_schedules.go`'s `JobPayload`, `selftool_reactions.go`'s `Config`) rather than introducing a differently-scanned type for this one table. No functional difference for tasks `04`/`07`/`09` — they still read/write raw JSON text at this column.
2. Added `validateTeamSlots` (Go-layer enum/consistency validation for `TeamSlotDefinition.Resolution` ∈ {`durable`,`fresh`}, `.ActivationMode` ∈ {`singleton`,`fresh-per-wake`,`concurrent`}, `Min ≤ Max`, non-empty `Name`) — not explicitly requested by the task, but mirrors the same Go-layer-validation-over-JSON-blob-column pattern `agents.go`'s `validateAgentMultiAgentFields`/`roles.go`'s `validateRoleFields` already use for the exact same two enums (`activation_mode` is literally the same value set `agent_profiles.activation_mode` uses, referenced not redefined, per the design doc). Kept intentionally permissive: empty-string values pass (a Team definition mid-authoring, or a slot that hasn't set these yet, isn't rejected), and this validation only runs when `slots_json` is non-empty — not required as part of "Done means" but a small, low-risk addition that catches an obviously-malformed Team Slot before it round-trips silently.
3. `UpdateTeam` is a full mutable-column replace (name, description, all four JSON columns), not a narrow single-field update — the task's CRUD list just says "UpdateTeam" with no further specification; mirrored `roles.go`'s `UpdateRole` breadth (the closer structural analog — a definition table with several mutable columns) rather than `agent_schedules.go`'s narrow single-purpose updaters (`UpdateAgentScheduleStatus`, which exists alongside several other narrow mutators for a table with many independently-evolving runtime columns — `teams` has no such runtime-state columns yet).
4. `Team.ID`/`Role.ID`-style UUID generation: `CreateTeam` generates via `uuid.New().String()` if `t.ID` is empty, same convention as `roles.go`'s `CreateRole` and `selftool_reactions.go`'s `InsertSelftoolReaction` (per migration 126's own doc comment, "this codebase's own insert-time-ID-generation convention").

**Migration testing against a real backup DB copy (per `EXECUTION-PROCESS.md`).** Copied `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726` to an isolated, absolute scratch path (never operated on the real backup file in place). That backup pre-dates the goose ledger entirely (no `goose_db_version` table — a pre-goose database) and pre-dates migration 127 (`agent_schedules` present, `schedule_runs` absent), so opening it via `store.New()` exercised the full pending chain (95 through 128, including 127's `agent_schedules` rename-recreate-copy rebuild and then this task's 128) against real production data in one pass, not migration 128 in isolation. Verified via a throwaway `_test.go` (deleted immediately after, never committed) that: `store.New()` succeeded; `teams` table was queryable and empty; a full `CreateTeam`/`GetTeam`/`DeleteTeam` round-trip succeeded; the pre-existing `agent_schedules` row (1 row, matching migration 127's own Work Log finding of exactly one production row) survived the full migration chain intact. Independently confirmed via `sqlite3` directly against the migrated scratch copy: `.schema teams` matches the migration's DDL exactly, `idx_teams_name` exists, `goose_db_version` max is `128`. Scratch DB directory removed after verification (`rm -rf`).

**Baseline checks.** `go build ./cmd/nanite/` — clean. `go vet ./...` — clean *for this task's own files*; pre-existing, unrelated vet findings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible-context-leak warnings) predate this task (last touched by an unrelated scheduling-batch commit, confirmed via `git log -- internal/service/container.go`; this task never touched that file) — `go vet ./internal/store/...` is clean. `go test ./...` — all packages pass, including `internal/store`.

**Not built here, per this task's own scope boundary** (Phase 2/4 tasks' job): no compiler, launcher, HTTP CRUD API, or authority-enforcement logic. `Team`/`TeamSlotDefinition` are unreferenced by any other package as of this task — expected; nothing wires this table up to runtime yet.

**Nothing escalated.** No ambiguity in the task's own instruction, no zero-coverage gap in `docs/engineering/*`, no conflict with another active task found during implementation.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
