# Kill the file-reingest-on-boot pattern, in full — enumerated, not generalized

**Phase:** 1
**Status:** implemented
**Depends on:** `TASKS/phase-0/10-seed-builtin-agent-profiles.md` (implemented — fixes the narrower `source='internal'`-only piece of this same problem; this task finishes the rest), `01-add-roles-table-and-cascade-resolution.md` (needed for the roles negative-verification check, item 4 below). **No longer coupled to `10-data-migrate-nanite-agents-md.md`** — that task is out of scope for Phase 1 (operator decision, 2026-08-18; see that file). Coordinate merge order with `04-add-known-tools-and-agent-tools-fk.md` — both touch `internal/service/ingest.go` (different functions: `04`'s `seedRoleToolsFromIngest` vs. this task's `AutoIngestAgents`/`AutoIngestSkills`), whichever lands first, the other rebases.
**Touches:** `internal/service/ingest.go` (`AutoIngestAgents`, `AutoIngestSkills`), `internal/service/container.go` (boot-time call sites, ~lines 469, 541), `internal/agent/discovery.go` (`Discover`, `discoverDir` — read path stays, the *re-upsert-every-boot* behavior is what changes)

## Context

TASKS.md Phase 1: *"Kill the file-reingest-on-boot pattern generally."* Planning landmine, explicit: *"'kill the file-reingest-on-boot pattern generally' doesn't name which reingest paths. Enumerate them explicitly (agent `.md` reingest, role reingest, boot-profile catalog reingest, whatever else you find) rather than planning against 'generally.'"* This task is that enumeration, verified against real code — not all three landmine-named items turn out to be real, equally-scoped gaps; report accordingly.

### 1. Agent `.md` reingest — real, only partially fixed by Phase 0 #10

`AutoIngestAgents` (`internal/service/ingest.go:59`) is called **unconditionally every boot** from `container.go:469`, for every definition `Discover()` returns regardless of source (`cli`/`project`/`user`/`plugin`/adapter-discovered). Phase 0 item 10 (already read in full) stops the boot-time pass from **overwriting** `source='internal'` rows once seeded — a narrow, real fix for the specific "GUI customization to a builtin agent silently reverted on restart" bug. It does **not** stop the boot-time file-parse-and-upsert pass for `project`/`user`-source agents (the real `.nanite/agents/*.md` corpus, confirmed via `internal/agent/discovery.go:56` — `discoverDir(filepath.Join(opts.WorkingDir, ".nanite", "agents"), "project")`, priority 2). Every one of these still gets re-parsed from disk and re-upserted into `agent_profiles` on every single boot today. **This is the real remaining gap** — the same class of bug Phase 0 #10 fixed for builtins (a DB-side edit silently reverted by the next restart's file-reingest) still exists for every project/user-source agent.

**Update, 2026-08-18**: the 24 current `.nanite/agents/*.md` files stay in place, undisturbed, and are not being data-migrated (`10` is out of scope for Phase 1 — see that file). This task's fix still applies to them exactly as described — nothing here changes because `10` isn't running; if anything, it matters less urgently since none of these agents will be dispatched until Phase 1-5 completes, but the fix is still real, decided, scoped work per `TASKS.md`.

### 2. "Role reingest" — not a real existing mechanism; this task's job is to *not create one*, not to kill an existing one

Investigated directly: there is no separate "role" reingest pattern in the current codebase to kill. `~/.nanite/roles/` (the developer-persona Claude-Code-boot convention, per `GLOSSARY.md`, explicitly **not** part of Nanite's own runtime agent system) is unrelated and out of scope entirely — do not touch it. The new `roles` table (`01-add-roles-table-and-cascade-resolution.md`) has no file-based precedent to reingest from; its own task file already states it must be DB-authoritative from creation, with no reingest path ever built. **This landmine item resolves to: confirm `01`'s `roles` table genuinely never grows a reingest-from-file path — a negative verification, not a cut.**

### 3. Boot-profile catalog reingest — real, but explicitly out of this task's scope (Phase 2's job)

The boot-profile catalog (`bootprofile.Profile`/`Launch` YAML, `internal/bootprofile/loader.go`) is retired as a standalone system in Phase 2, per architecture doc `02-agent-launching.md` and decision log §7. It does re-read from disk (confirmed: `internal/bootprofile/loader.go:51` `LoadCatalog`, `os.ReadDir`/`os.ReadFile` at load time) — but per `TASKS/INDEX.md`'s own phase assignment and the architecture doc's explicit framing ("retire as a standalone system," carrying forward only the `cmd`/`http` dynamic-resolver and the mandatory post-compaction re-read as first-class *launching-time* mechanisms), the actual retirement work belongs to Phase 2's task files, not here. **Do not attempt to fix or retire the boot-profile catalog's reingest as part of this task** — note its existence for completeness (per the landmine's "enumerate explicitly" instruction) and defer the fix to Phase 2.

### 4. Skill reingest — a fourth real instance the landmine's list didn't name, found during research

`AutoIngestSkills` (`internal/service/ingest.go:135`), called unconditionally every boot from `container.go:541`, is the parallel mechanism for skill definitions — same boot-time re-parse-and-upsert pattern as `AutoIngestAgents`, not fixed by any Phase 0 item. Include this in scope; it's the same bug class on a sibling system.

## What to do

1. Change `AutoIngestAgents`/`AutoIngestSkills`'s boot-time behavior so a DB row, once it exists (regardless of source — `internal`, `project`, `user`, `plugin`), is never silently overwritten by a subsequent boot's file-parse pass. The file remains the *import* path (a real, deliberate "pull this file's content into the DB" action — e.g. an explicit CLI/API-triggered re-import), not a standing *sync* path that runs unconditionally on every process start.
2. Preserve the real value `AutoIngestAgents` currently provides on first sight of a genuinely new file (a `.nanite/agents/new-agent.md` a developer just added should still get picked up and create a new `agent_profiles` row) — this task changes "keep re-overwriting forever" to "ingest once, then leave DB-authoritative," not "stop ingesting new files ever."
3. Preserve the unknown-tool-reference and hardcoded-model warnings `AutoIngestAgents` currently emits (`ingest.go:76-99`) — these are real, valuable diagnostics; don't lose them just because the overwrite behavior changes.
4. Confirm `01`'s `roles` table has no reingest path (negative verification, see Context item 2) — add a test or explicit code-review note confirming this, since there's no existing mechanism to remove.
5. Note (don't fix) the boot-profile catalog's reingest pattern for Phase 2, per Context item 3 — a one-line pointer in this task's Work Log is sufficient, not a design document.
6. ~~Sequence with `10-data-migrate-nanite-agents-md.md`~~ — moot, `10` is out of scope for Phase 1. Instead, coordinate merge order with `04` per the Depends-on note above (both touch `internal/service/ingest.go`, different functions).

## Done means

- A DB-side edit to a project/user-source agent (or a skill) survives a full process restart — verified directly: edit an agent via the REST API, restart the service, confirm the edit is still present (not reverted by the next boot's file-parse pass).
- A genuinely new `.nanite/agents/*.md` file added by a developer is still picked up on the next boot and creates a new row (regression check — this task must not silently disable first-ingest).
- The unknown-tool-reference and hardcoded-model-pin warnings still fire correctly.
- `roles` (from `01`) has zero reingest-from-file code path — confirmed by code review/test, documented in this file's Work Log.
- The boot-profile catalog's reingest is explicitly noted as deferred to Phase 2, not silently forgotten or accidentally fixed here.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Tested against a real copy of the backed-up database.

## Work log

**Branch/base note.** This worktree's branch had been created off an earlier
`phase-1-execution` commit (`df71e710`, a strict ancestor with zero divergent
commits of its own) rather than the branch's current tip. Fast-forwarded to
`phase-1-execution`'s tip (`3aa5a302`) before starting — this was the actual
starting commit, and is where `TASKS/phase-1/01`'s `roles` table (needed for
item 4's negative verification) actually exists.

**Correction to the task file's own dependency framing (worker step 7 --
decision vs. rationale; decision executed anyway, in full, per below).**
`10-seed-builtin-agent-profiles.md` is marked `implemented` and its Work Log
describes an `alreadySeededInternal` gate added to `upsertAgentDef`. That
code was never actually merged onto `phase-1-execution` (or any reachable
branch) -- confirmed by `git log -S"alreadySeeded" --all` (only hit: the
tracker-reconciliation commit `cf1ac193`'s prose, not a code diff) and by
reading `internal/service/ingest.go` at this task's actual starting commit,
which still called `st.UpdateAgent` unconditionally for every existing row
regardless of `source`. Logged as its own entry in `TASKS/ESCALATIONS.md`
(2026-08-18, "Item 10: ... status did not match the code"). Not a blocker:
this task's own scope ("regardless of source -- internal, project, user,
plugin") already fully subsumes `10`'s narrower internal-only fix, so the
implementation below closes `10`'s gap too as a side effect of doing the
full job, with no separate change needed.

**1. Agent `.md` reingest -- fixed for every source, not just `project`/`user`.**
Added a `bootPass bool` parameter to `upsertAgentDef` (`internal/service/
ingest.go`). `AutoIngestAgents` (the boot-time bulk pass, called
unconditionally from `container.go` on every process start) calls it with
`bootPass=true`; `IngestAgentDefinition` (the explicit, deliberate reimport
path used by `AgentConfigService.writeManaged`/`SaveManagedAgentProfile`
immediately after a managed agent's file is written -- the mechanism behind
every REST-API agent edit, `handleUpdateAgent` -> `AgentConfigService.Update`
-> `writeManaged` -> `IngestAgentDefinition`) calls it with `bootPass=false`.

Inside `upsertAgentDef`'s `existing != nil` branch: `st.UpdateAgent(profile)`
is now skipped whenever `bootPass && existing.Source == profile.Source` --
i.e. on a boot-time pass, a row that's already been ingested under its
current source is frozen. The one exception is a genuine provenance
transition (`existing.Source != profile.Source`) -- a deliberate one-time
reclassification (the historical `builtin`->`internal` migration flip,
CW-20260512-0111, is the concrete example already covered by an existing
test), not an ordinary repeated boot, so it still syncs once. This
distinction is *why* a single boolean parameter (rather than "just always
freeze once a row exists") was necessary: `IngestAgentDefinition` and
`AutoIngestAgents` share `upsertAgentDef`, and the managed-agent edit flow
(source stays `'user'` across the edit) would otherwise have been frozen
into a no-op by the exact same logic that needs to freeze the boot-time
pass -- confirmed by reading `writeManaged`/`SaveManagedAgentProfile`
directly before deciding on this shape, not by assumption.

Also gated, inside the same freeze, for consistency (found during this
task's own read of `ingest.go`, not named in the task's enumerated list, but
the same bug class in the same touched function): `seedProcedures` (`agent_
procedures.InsertAgentProcedure` does `ON CONFLICT DO UPDATE SET body=...`,
confirmed by reading the store code directly) and `seedRoleToolsFromIngest`
(`agent_known_tools.InsertAgentKnownTool` does `INSERT OR REPLACE`, and --
contrary to `10`'s own unmerged Work Log claim that this path is "wholly
file-derived" with no other writer -- `handleUpdateAgentKnownTool`
(`internal/api/agent_capabilities.go`) is a real, reachable REST endpoint
that calls the same `InsertAgentKnownTool`, so a pinned/reordered tool *was*
real user-editable content a boot-time reseed would have silently
reverted). Both are now gated by a `freshContent` flag (true when the row
was just created, or -- on a boot pass -- when a provenance transition just
completed) so a boot-time reingest doesn't re-stomp a GUI/API customization
to either child table either. Neither `def.Procedures` nor `def.RoleTools`
is exercised in the mixed-batch/frozen-row tests below by name, but the gate
follows the exact same condition already covered for the primary content
sync, and existing tests for both secondary paths (none currently re-ingest
the same slug twice) were unaffected.

The unknown-tool-reference and hardcoded-model-pin warnings (`ingest.go`'s
`unknownDeclaredTools`/`def.Model != ""` checks) are untouched -- they only
read `def`, never gated on `existing`/`bootPass`, and every existing test
covering them (`TestAutoIngestAgents_UnknownToolNameIsLoud`,
`_HardcodedModelIsLoud`, `_NilKnownToolsSkipsValidation`) still passes
unmodified.

**Skills.** `AutoIngestSkills`/`upsertSkillDef` got the same freeze, but
without a `bootPass` parameter: grep confirmed `upsertSkillDef` has exactly
one caller (`AutoIngestSkills`) -- `handleUpdateSkill`'s REST path writes
through `st.UpdateSkill` directly, never through this function -- so there
is no separate "explicit reimport" caller to preserve a resync path for.
`upsertSkillDef` now returns immediately (no-op) once `existing.Source ==
source`, with the same provenance-transition exception (`existing.Source !=
source` still syncs once, including the version bump if content changed).

**2. "Role reingest" -- negative verification, item 4.** Confirmed by code
review: `store.CreateRole`/`UpdateRole` (`internal/store/roles.go`, landed by
task `01`) are called only from `internal/api/roles.go`'s REST handlers --
grepped every other reference across `internal/` and found zero boot-time or
file-parse call site. `roles.go`'s own doc comment already states this
("roles is DB-authoritative from creation onward -- there is no file/YAML
reingest path for this table ... verified again by
08-kill-file-reingest-on-boot-pattern.md") and migration `108_add_roles_
table.sql`'s header makes the same claim -- both written by task `01`'s
worker in anticipation of this task, and both hold up under direct
verification. Also checked `internal/plugin/builtin/adapter-nanite-native/
plugin.go`'s unrelated `Roles`/`roleEntry` types (the `.nanite/config.yaml`/
`~/.nanite/roles/` developer-persona convention, explicitly out of scope per
`GLOSSARY.md`'s Agent entry and this task's own Context section) -- zero
calls to `store.CreateRole`/`UpdateRole` anywhere in that file; it only reads
`.md` files to compose a prompt string, never persists to the DB `roles`
table. Added `TestAutoIngestAgents_RolesTableUntouched`
(`internal/service/ingest_test.go`) as the executable half of this
verification: seeds one role directly, runs both boot-time passes, asserts
the roles table is untouched in either direction.

**3. Boot-profile catalog reingest -- confirmed real, explicitly deferred to
Phase 2, not touched here.** `internal/bootprofile/loader.go`'s
`LoadCatalog` does re-read the catalog YAML from disk (`os.ReadDir`/
`os.ReadFile`), matching the task's Context section. Left entirely alone per
the task's explicit instruction and the architecture doc's Phase 2
retirement framing.

**4. Skill reingest (`AutoIngestSkills`) -- fixed, see above** (this was the
task's own fourth enumerated item, found during its own research rather
than the original landmine list; folded into the main implementation).

**Tests (`internal/service/ingest_test.go`).**
- Renamed/flipped `TestAutoIngestAgents_UpdateOnReingest` ->
  `TestAutoIngestAgents_ReingestDoesNotOverwriteExistingRow` and
  `TestAutoIngestSkills_VersionBumpsOnContentChange` ->
  `_ContentChangeDoesNotOverwriteExistingRow` -- both previously asserted the
  exact always-overwrite behavior this task kills; now assert the frozen
  value survives a boot-time reingest with changed content.
- Added `TestIngestAgentDefinition_ExplicitReimportStillSyncs` -- proves the
  explicit-reimport path is NOT frozen (the other half of the contract).
- Added `TestAutoIngestAgents_DBEditSurvivesBootReingest` -- the literal
  Done-means scenario at the ingest layer: a direct DB-side content mutation
  (standing in for however an edit landed) survives a subsequent
  `AutoIngestAgents` pass with an unchanged file-derived def.
- Added `TestAutoIngestAgents_NewFileStillIngestedAlongsideFrozenRow` --
  proves freezing an existing row doesn't block first-ingest of a genuinely
  new file discovered in the same batch.
- Extended `TestAutoIngestAgents_SourceFlipFromBuiltinToInternal` with a
  third boot pass after the flip, proving the freeze actually engages once
  the row has flipped to `source='internal'` (the original two-pass test
  only proved the flip itself, not that it re-freezes afterward).
- Added `TestAutoIngestSkills_SourceChangeStillSyncsOnce` -- defensive
  coverage for the provenance-transition exception on the skills side,
  which had no prior migration-flip precedent to piggyback a test on.
- Added `TestAutoIngestAgents_RolesTableUntouched` (item 4's negative
  verification, see above).
- All pre-existing `TestAutoIngestAgents_*`/`TestAutoIngestSkills_*` tests
  not named above needed no changes (verified: none of them re-ingest the
  same slug across two `AutoIngestAgents`/`AutoIngestSkills` calls with
  changed content under an unchanged source, so none encoded the old
  always-overwrite behavior in a way this fix breaks).

**Real-backup-DB verification (task step 8).** Copied `~/.local/share/
nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-
132726` (the most recent backup) to a scratch path. Wrote a temporary
`_test.go` (`internal/api/`, deleted after the run -- not a permanent
artifact) that: opened the copied DB via `store.New`; built a real
`service.Container` against it with `WorkingDir` pointed at this repo (so
`Discover()` found the real 24-file `.nanite/agents/*.md` corpus, landing at
33 discovered/ingested agents total including internal+adapter-discovered);
issued a real `PUT /api/agents/blt-analyst-001` REST edit through the actual
`http.ServeMux`; rebuilt the container a second time against the same store
(simulating a full process restart's boot-time ingest pass); confirmed via
`store.GetAgentBySlug` that the REST edit's `system_prompt` survived the
simulated restart rather than being reverted to the real `analyst.md`
file's on-disk content. Passed. Full log excerpt confirms both boot passes
each discovered/ingested exactly 33 agents and 9 skills, and the edited
value is present both immediately after the PUT and after the second
`NewContainer` call.

**Process incident during that verification (self-caught, reverted, logged
in `TASKS/ESCALATIONS.md`).** The real backup DB's `analyst` row carries a
*relative* `source_ref` (`.nanite/agents/analyst.md`). `writeManaged`
resolves that path against the test process's actual OS-level CWD (this
repo's root when running `go test ./internal/api/...`), not the
`WorkingDir` passed into `ContainerConfig` -- so the verification's REST
edit briefly wrote to the real, tracked `.nanite/agents/analyst.md` file.
Caught immediately via `git status`, reverted with `git checkout --
.nanite/agents/analyst.md`, confirmed clean via `git diff --stat HEAD --
.nanite/`. No data lost; documented as a process note for future
similar verification steps.

**Checks.**
- `go build ./cmd/nanite/` -- pass.
- `go vet ./...` -- pass except the pre-existing, unrelated
  `container.go:1189/1209/1260` ("stopReaper"/"stopRuntimeReaper" possible
  context leak) findings -- confirmed present before this task's changes too
  (`git stash` of both changed files, re-ran `go vet ./internal/service/...`,
  same two findings, then popped the stash back).
- `go test ./internal/service/ -run "TestAutoIngest|TestIngestAgentDefinition" -v -count=1`
  -- all 23 tests pass (16 pre-existing/renamed + 7 new).
- `go test ./internal/service/... -count=1` -- pass.
- `go test ./internal/store/... -count=1` -- pass (including `TestRole*`).
- `go test ./... -count=1` -- pass across every package with test files
  (only `[no test files]` entries otherwise, no failures).

No schema change. `TASKS/INDEX.md` intentionally left untouched (Orchestrator
updates it after merge).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
