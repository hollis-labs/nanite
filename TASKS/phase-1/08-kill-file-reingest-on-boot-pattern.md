# Kill the file-reingest-on-boot pattern, in full — files are not agent/skill storage or config, only seeding

**Phase:** 1
**Status:** in-progress (reopened, 2026-08-18 — Round 1's fix was real but insufficient; see banner below)
**Depends on:** `01-add-roles-table-and-cascade-resolution.md` (roles negative-verification, already done). No longer coupled to `10-data-migrate-nanite-agents-md.md` (out of scope for Phase 1) or `04-add-known-tools-and-agent-tools-fk.md` (already merged; if this task's Round 2 touches `internal/service/ingest.go` again, diff against `04`'s `seedRoleToolsFromIngest` changes before editing, don't blind-overwrite).
**Touches:** `internal/agent/discovery.go` (`Discover`, `DiscoverOptions`), `internal/skill/discovery.go` (`Discover`, `DiscoverOptions`), `internal/service/container.go` (both `Discover` call sites — currently ~lines 399, 543), `internal/service/ingest.go` (`AutoIngestAgents`/`AutoIngestSkills` — Round 1 already landed the overwrite-freeze here, keep it; may need adjusting once fewer sources reach it), `TASKS/phase-1/12-fix-agent-service-get-drops-new-db-only-columns.md`'s own scope (read, don't re-do — see the coordination note in "What to do")

## ⚠️ Reopened, 2026-08-18 — Round 1 was a real, correctly-implemented fix for the wrong scope

Round 1 (Work Log below, already merged into `phase-1-execution`) fixed the *overwrite* problem: a DB row, once ingested, is no longer silently reverted by a later boot's file-parse pass. That fix is real, tested, and stays. But it left automatic **first-ingest** of project/user/plugin `.md` files as a permanent, ongoing mechanism — treating file-drop as a legitimate way to create an agent or skill going forward. That's not what the architecture actually says, and the operator has now said directly: *"The only file based agents should be from seeding. [...] no debt carries forward."*

**The exact text this task under-scoped, re-verified:**
- `docs/engineering/architecture/01-agent-construction.md`, "What's cut": *"**Files as agent storage**, except builtin/seed content."* No carve-out for project/user `.nanite/agents/*.md` — the exception is builtin/seed only.
- `docs/architecture-decision-log-2026-08-17.md` §6's settled summary: *"files dropped except builtin/seed, DB-authoritative with no re-ingest-on-boot."* Not "no destructive re-ingest" — no re-ingest-on-boot, period.

Round 1's own task-file text (before this rewrite) explicitly said *"this task changes 'keep re-overwriting forever' to 'ingest once, then leave DB-authoritative,' not 'stop ingesting new files ever'"* — that sentence is the error. Ingesting new files, ever, automatically, on boot, for non-seed sources, is exactly what's supposed to stop.

**Scope confirmed with the operator (2026-08-18):** applies to skills too, same treatment as agents — same bug class, same fix, no special-casing.

## Context — what's actually in scope, verified against real code (not the same enumeration as Round 1)

### Agent discovery tiers (`internal/agent/discovery.go`'s `Discover`), by priority

1. **CLI `--agent` flag (single file, `source="cli"`)** — an explicit, per-invocation override the caller names deliberately at launch time, not an ongoing background scan of a directory. Plausibly *not* "agent storage" in the sense the architecture doc means — but verify, don't assume: check whether a `source="cli"` definition ever gets persisted into `agent_profiles` via `upsertAgentDef`/`AutoIngestAgents` the same as the other tiers, or whether it's purely ephemeral (used only for that one process's in-memory resolution, never written to the DB). If it does persist, decide whether that's itself a "config via file" case needing the same cut, or a legitimate one-shot exception, and document the decision — don't silently leave it as-is without checking.
2. **`.nanite/agents/` (project, `source="project"`)** — the real sprawl case. **Cut**: stop scanning this directory at boot.
3. **`~/.nanite/agents/` (user, `source="user"`)** — same pattern, personal-scope. **Cut.**
4. **`plugins/*/agents/*.md` (plugin, `source="plugin"`, via `discoverPluginAgents`)** — loose `.md`-file scanning under a plugin's own directory. **Cut** — this is not the same thing as `registers.agent_profiles[]` (a plugin manifest's declarative registration field, Phase 5's `10-wire-registers-agent-profiles.md`, a structurally different mechanism this task does not touch or affect). Confirm via grep whether any real plugin currently ships files under `plugins/*/agents/` (as of this writing, none do — `find . -path '*/plugins/*/agents'` returns nothing — so this is very likely dead-in-practice already; cut the scan regardless, per the architecture doc's flat statement, not because it's dead).
5. **Adapter-discovered tier** — already cut for external formats by Phase 0 `16-cut-external-agent-import.md`. The `nanite-native` adapter's own discovery (reading `.nanite/config.yaml`'s `agents:` block — a different format, explicitly kept by task `16`) is unaffected by this task; do not touch it.

### Skill discovery tiers (`internal/skill/discovery.go`'s `Discover`), by priority

1. **`.nanite/skills/` (project)** — **cut**, same reasoning as agents.
2. **`~/.nanite/skills/` (user)** — **cut.**
3. **`.claude/skills/` (Claude Code ecosystem format, `source="claude"`)** — this is skills' analogue of the external-format-adapter tier Phase 0 `16` cut for agents (no equivalent task exists for skills — nothing in Phase 0 or Phase 1's plan named it explicitly). Per the operator's "same treatment as agents, no debt carries forward" — **cut this too**; don't leave an un-cut external-ecosystem-format tier for skills just because no task happened to name it yet. If you find a reason this specific tier is genuinely different (e.g., something else depends on it that isn't true for the agent side), stop and say so in the Work Log rather than guessing past it — but verify first, don't assume parity with agents means automatic exemption either way.
4. **`plugins/*/skills/*.md` (plugin)** — **cut**, same reasoning as the agent plugin tier.

### What stays, unaffected by this task

- **Builtin/seed agents** (`internal/agent/builtin/profiles/`, compiled-in via Go embed) — the one real exception the architecture doc names. Round 1's overwrite-freeze already handles these correctly (seed once, never re-overwritten). No change here.
- **Builtin skills** (`internal/skill/builtin/`, compiled-in via Go embed, confirmed to exist — same shape as agent builtins). Same treatment, no change needed.
- **Plugin-manifest agent registration** (`registers.agent_profiles[]`, Phase 5 `10-wire-registers-agent-profiles.md`) — a declarative manifest field, not a scanned `.md` directory. Out of scope for this task, not affected by cutting `plugins/*/agents/*.md` scanning.
- **The `nanite-native` adapter's `.nanite/config.yaml` `agents:` block** — a different, already-decided-to-stay mechanism (Phase 0 `16`). Not touched.
- **Boot-profile catalog reingest** (`internal/bootprofile/loader.go`) — still explicitly Phase 2's job, per Round 1's own (still-correct) reasoning. Not touched here.
- **The ~33 agent rows and ~9 skill rows already ingested into the DB** from prior boots' file discovery — these stay exactly as they are, as ordinary DB rows (`source='project'`/`'user'` preserved as a historical provenance tag). No deletion, no migration into `roles`/`agents` (that's `10`'s job, explicitly out of scope). They simply stop being subject to any further file-based re-discovery once this task lands — a file changing on disk after this task ships has zero effect on the DB going forward, by design.
- **The underlying `.nanite/agents/*.md`/`.nanite/skills/*.md` files themselves** — not deleted, not touched. They just stop being read automatically. Per the operator: *"Don't worry about generating a cached copy right now, YAGNI. If we need them again, that's when we'll add that."* — no export/backup/snapshot mechanism needed as part of this task.

### Coordination with `12-fix-agent-service-get-drops-new-db-only-columns.md`

That task (already implemented, not yet merged as of this writing) fixed `AgentService.Get`/`GetBySlug`/`List` to overlay DB-only columns (`role_id`/`model_id`/`runtime_kind`/`consumer_id`/`activation_mode`/`class`/`default_state`) onto the file-derived view for any agent still resolved via an in-memory `Definition`. This task's cut **shrinks** the population that fix matters for (most of the ~33 project/user-source agents will no longer have a matching in-memory `Definition` at all post-cut, since their backing files stop being discovered — they'll correctly fall through to the real DB read path, `s.agents.GetAgent(id)`, on their own). It does **not** make `12`'s fix wrong or redundant: builtin-seeded agents are *both* file-defined (compiled-in) *and* DB-seeded, so they'll still resolve via the in-memory-def path and still need `12`'s overlay. Land both; don't try to make one obsolete the other.

## What to do

1. Verify the `source="cli"` single-file tier's actual persistence behavior (see Context item 1) and decide/document whether it needs any change. Default to leaving it alone unless you find it's silently writing a persisted row the same way the cut tiers do.
2. In `internal/agent/discovery.go`: remove (not merely stop-calling-with-empty-args — actually remove, per this project's standing dead-code policy) the project/user/plugin discovery tiers (`discoverDir` calls for `"project"`/`"user"` sources, `discoverPluginAgents` and its call site) from `Discover()`. Remove the now-dead `discoverPluginAgents` function itself if nothing else calls it. Update `DiscoverOptions`' doc comments to reflect the smaller real tier set.
3. Do the same for `internal/skill/discovery.go` — remove the project/user/`.claude/skills/`/plugin tiers and `discoverPluginSkills`.
4. Update `internal/service/container.go`'s two `Discover` call sites accordingly — remove now-meaningless `WorkingDir`/`PluginsDir`/`HomeDir` wiring for the removed tiers if `DiscoverOptions` itself shrinks; if the struct still carries fields for the CLI-flag tier or other still-live options, keep those.
5. Confirm `AutoIngestAgents`/`AutoIngestSkills` (Round 1's fix) still make sense against the smaller input set `Discover()` now returns — they should, since they operate on whatever `[]*Definition` they're handed, but re-run the full test suite to confirm nothing assumed the removed tiers existed.
6. Update every test that currently exercises the removed tiers (`internal/agent/discovery_test.go`, `internal/skill/discovery_test.go`, and Round 1's own `ingest_test.go` additions if any assumed project/user auto-discovery) — remove tests for removed behavior, don't leave them red or silently skipped.
7. Real-backup-DB verification: boot against a copy of the real backup, confirm the ~33 previously-discovered agents remain exactly as they are in the DB (no data loss, no re-ingestion), and confirm a brand-new `.nanite/agents/new-test-agent.md` file dropped in before boot is **not** picked up (the inverse of Round 1's regression check — this is now the correct, intended behavior).
8. Live-verify against a real running scratch instance: boot, confirm the log no longer reports discovering/ingesting project/user-source `.md` files (only builtin + whatever the CLI-flag/nanite-native-adapter tiers still contribute), confirm the existing DB rows for previously-discovered agents are still fully intact and servable via the API.
9. Update this file's own Work Log with a clear "Round 2" section — do not delete Round 1's Work Log, it's an accurate record of real, still-correct work; just make clear Round 2 is what completes the task.

## Done means

- `Discover()` (agents and skills) no longer scans `.nanite/agents/`, `~/.nanite/agents/`, `plugins/*/agents/*.md`, `.nanite/skills/`, `~/.nanite/skills/`, `.claude/skills/`, or `plugins/*/skills/*.md` at boot or ever, by code inspection (the scanning code itself is removed, not merely unreached).
- A fresh boot against a copy of the real backup DB shows the previously-ingested ~33 agents/~9 skills fully present and correct in the DB, with **zero** new file-discovery log lines for the cut tiers.
- A new `.md` file dropped into any of the cut directories before boot produces **no** new `agent_profiles`/`skills` row — verified live, not just by code review.
- Builtin/seed agents and skills are unaffected — still seed correctly, still frozen against re-overwrite (Round 1's fix, unchanged).
- `registers.agent_profiles[]` (Phase 5, not yet built) and the `nanite-native` adapter's `.nanite/config.yaml` tier are confirmed untouched by this change.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including updated/removed tests for the cut tiers.

## Work log — Round 1 (2026-08-18, superseded in scope, kept for the record — see banner)

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
  new file discovered in the same batch. **(Note, Round 2: this exact
  behavior — first-ingest of a new file — is what Round 2 removes. This
  test's assumption is now wrong and must be removed or rewritten as part
  of Round 2, not left passing against behavior that no longer exists.)**
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

## Work log — Round 2 (fill in here)
<Worker fills this in: what was actually done for the full cut, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
