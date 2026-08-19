# Kill the file-reingest-on-boot pattern, in full — files are not agent/skill storage or config, only seeding

**Phase:** 1
**Status:** implemented
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

## Work log — Round 2 (2026-08-18)

**Starting state / branch note.** This worktree's branch (`worktree-agent-
a56ce396d515a71db`) had been created off an earlier `phase-1-execution`
commit (`df71e710`, a strict ancestor with zero divergent commits of its
own — same shape as Round 1's own branch-note) rather than the branch's
current tip. Fast-forwarded to `phase-1-execution`'s tip (`f7821c02`, "Phase
1 #08: reopen for a full redo") before starting — that commit, with the
Reopened banner visible, is where this Round 2 work actually began.

**1. CLI `--agent` flag tier (Context item 1) — investigated, left in place.**
Exhaustively grepped every real call site of `agent.Discover` /
`DiscoverOptions.CLIAgentPath` across `cmd/nanite/` and
`internal/service/container.go`. Finding: **`CLIAgentPath` is never set to a
non-empty value anywhere in production code** — it is 100% dead/unreachable
today, stronger than the task's own framing ("ephemeral, in-memory only")
anticipated. The `--agent` flags that do exist (`chat_cmd.go`,
`message_cmd.go`) take an **agent ID/slug** for selecting an existing
agent for a session/message — a completely different mechanism, never
wired to `CLIAgentPath`. Mechanically, *if* it were ever populated, the
resulting `Definition` (`Source: "cli"`) would flow into the same
`agentDefs` list passed to `AutoIngestAgents`/`upsertAgentDef` as every
other tier — i.e. it is capable of persisting a row exactly like the cut
tiers, if reached. But since it is never reached, the answer to the task's
literal test ("does it currently silently write a persisted row?") is no.
Per the task's own explicit default ("leave it alone unless you find it's
silently writing a persisted row the same way the cut tiers do") —
**left the CLI tier's code in place unchanged** (both the `CLIAgentPath`
field and its branch in `Discover()`), documented the finding directly in
`DiscoverOptions`' doc comment and the field's own comment so a future
reader doesn't have to re-derive it. Did not remove it despite it being
genuinely dead code, since the task's instruction here is explicit and takes
priority over my own inclination to apply the project's dead-code policy
more aggressively — noting this as a deliberate, documented deviation from
what I'd have done absent that instruction, not an oversight.

**2. `internal/agent/discovery.go` — project/user/plugin tiers removed in
full.** Removed the `discoverDir(..., "project")` and `discoverDir(...,
"user")` call sites, the `discoverPluginAgents` call site, and the
`discoverPluginAgents` function itself (nothing else called it). Removed
`DiscoverOptions.HomeDir` and `DiscoverOptions.PluginsDir` (no longer read
by anything). `discoverDir` itself is **retained** — not dead, since
`discovery_test.go`'s `testDirAdapter` (used to simulate adapter-based
discovery tiers in tests, e.g. `.agentrc/agents/`, `.claude/agents/`) still
calls it directly. `DiscoverOptions.WorkingDir` and `.Adapters` are kept —
still read by the live CLI tier context and the adapter-registry tier
(nanite-native + the already-no-op external-format adapters, Phase 0 task
16). Updated the type's doc comment to state plainly what's cut and why.

**3. `internal/skill/discovery.go` — every tier removed in full, including
`.claude/skills/`.** Investigated the task's own caveat (stop and report if
`.claude/skills/` turns out to need special treatment) before cutting:
grepped every reference to `.claude/skills` and `source == "claude"` across
`internal/`. Found three unrelated things that must NOT be confused with
the discovery tier being cut, and confirmed none of them depend on it
surviving:
  - `internal/skillbroker/broker.go`'s `sourceBias` — a generic ranking
    tiebreaker keyed on whatever `source` string a skill row already
    carries (works identically for historical `source='claude'` rows
    already in the DB; doesn't care whether new ones can still be
    discovered).
  - `internal/bootprofile/profile.go`'s `skill_index` requirement type — a
    genuinely separate mechanism (the boot-profile catalog compiler,
    `internal/bootprofile/*`, explicitly out of scope per this task's own
    "What stays" section / Phase 2) with its own resolver, unrelated to
    `internal/skill/discovery.go`.
  - `internal/agentvalidation/validation.go`'s `validSources` enum — a
    generic historical/possible-value validator for whatever `source` an
    `agent_profiles` row can carry (edit-time validation), not live
    discovery.
  No genuine dependent found. **Cut `.claude/skills/` in full, matching
  agents**, per the operator's "no debt carries forward" directive and the
  task's own instruction to treat it the same way absent a real reason not
  to. Since skills have no CLI-flag or adapter-registry tier to preserve
  (unlike agents), and `discoverDir`/`discoverPluginSkills` ended up with
  **zero remaining callers** (confirmed via grep, including test files),
  removed both outright — not left in place as unreachable code. `Discover`
  now always returns `nil, nil`; `DiscoverOptions` is an empty struct kept
  only so the container.go call site and a future non-file discovery source
  have an obvious attachment point, per the task's phrasing that both
  `Discover` call sites should remain.

**4. `internal/service/container.go` — both call sites updated.** Agent
call site: dropped `PluginsDir: "plugins"` (field no longer exists),
`WorkingDir`/`Adapters` kept. Skill call site: now calls
`skill.Discover(skill.DiscoverOptions{})` with no fields (always returns
empty). Updated the surrounding comments to explain why. Also had to fix a
**third real call site not in the task's enumerated list**,
`cmd/nanite/message_cmd.go`'s `newMessagingServiceForCLI` (used by the CLI
`message` subcommands) — it also passed the now-removed `PluginsDir` field
and would not have compiled otherwise. Fixed the same way as
container.go's agent call site.

**5. `AutoIngestAgents`/`AutoIngestSkills` — confirmed correct against the
smaller input, no code change needed.** Both remain fully source-agnostic:
they ingest whatever `[]*Definition` slice they're handed, with the
Source-driven trust-tier/freeze logic (Round 1's fix) applying uniformly
regardless of where the caller obtained the list. Verified by full test
suite pass (item 6/9 below) — nothing assumed the removed tiers still fed
these functions except the one test named in the task (item 6).

**6. Tests updated for every removed tier.**
- `internal/agent/discovery_test.go`: removed `TestDiscover_PriorityOrder`
  (renamed to `TestDiscover_AdapterPriorityOrder`, project-vs-adapter case
  dropped since project is cut) and `TestDiscover_PluginAgents` (plugin tier
  cut). Added `TestDiscover_ProjectUserPluginTiersRemoved` — the explicit
  negative-verification test: drops a file into each of the three cut
  directories (`.nanite/agents/`, `~/.nanite/agents/`,
  `plugins/*/agents/`) before calling `Discover()`, asserts zero results.
  Added `TestDiscover_CLIAgentWinsOverAdapter` — proves the CLI tier's
  priority-1 position (still real, per item 1) is unaffected by the cut.
  `TestDiscover_SkipsInvalidFiles`/`TestDiscover_SlugDedup` rewritten to
  exercise the adapter tier (via `testDirAdapter`) instead of the removed
  project tier, since the underlying `discoverDir` skip/dedup behavior they
  test is otherwise unchanged and worth keeping coverage for.
- `internal/agent/loader_test.go`: `TestDropAndLoad_EndToEnd` (asserted a
  project-tier file drop WAS discovered) rewritten as
  `TestDropAndLoad_ProjectTierNoLongerDiscovered` (asserts it is NOT) —
  same drop flow, inverted assertion, per the task's step 7 instruction to
  invert Round 1's regression check.
- `internal/skill/discovery_test.go`: replaced entirely with
  `TestDiscover_AlwaysEmpty` (`Discover(DiscoverOptions{})` always returns
  nothing — `DiscoverOptions` has no fields left to point it at anything).
- `internal/skill/loader_test.go`: `TestDropAndLoad_EndToEnd` (asserted a
  ~/.nanite/skills/ drop WAS discovered, Source="user") rewritten as
  `TestDropAndLoad_UserTierNoLongerDiscovered` (asserts NOT discovered),
  same inversion as the agent side. `TestEnsureHomeDirs_*` and
  `TestWriteUserSkillFile_RejectsTraversalSlugs` untouched — unrelated to
  discovery (directory creation and the managed-write path, respectively).
- `internal/service/ingest_test.go`: rewrote
  `TestAutoIngestAgents_NewFileStillIngestedAlongsideFrozenRow` (flagged by
  Round 1's own Work Log) into
  `TestAutoIngestAgents_NewInternalDefStillIngestedAlongsideFrozenRow`.
  `AutoIngestAgents` itself is still source-agnostic and the underlying
  mechanic ("a new def in the same batch as a frozen row still ingests") is
  still real and load-bearing — just no longer reachable via
  `Source: "project"` (since `Discover()` never produces that anymore), but
  fully reachable via `Source: "internal"` (a new builtin/internal profile
  shipping in a later release, alongside already-frozen ones from a prior
  release). Rewrote the test to use "internal" instead of "project" so it
  keeps proving a real, still-true fact about `AutoIngestAgents` rather than
  a now-false one about file discovery. No other `ingest_test.go` test
  needed changes — all remaining `Source: "project"/"user"/"plugin"` usages
  in that file call `AutoIngestAgents`/`AutoIngestSkills` directly with
  hand-built `[]*Definition` slices (never through `Discover()`), and they
  test genuinely still-live logic (the trust-tier assignment `upsertAgentDef`
  applies to whatever `Source` a def carries, reachable today via the
  still-live `IngestAgentDefinition` explicit-reimport path used by managed
  agent edits) — none of them claim anything false about boot-time file
  discovery.
- `internal/api/loom_curator_wake_test.go` — **a real, unenumerated
  breakage found by running the full suite, not named in the task's own
  list.** `newTestAPIWithLoomCurator`'s doc comment explicitly documented
  and asserted the exact pattern this task kills: "a plain file drop +
  restart is sufficient to produce a real, wakeable durable_agent_instances
  row." It worked by copying the repo's real
  `.nanite/agents/loom-curator.md` into a temp `WorkingDir` and relying on
  boot-time project-tier discovery + `AutoIngestAgents` to create the
  `agent_profiles` row before `SyncManagedDurableAgentConfigs` (a separate,
  untouched mechanism that reads `.nanite/durable-agents/*.yaml` and
  resolves the agent by slug) could provision the durable-agent instance.
  With the project tier cut, that row never appears, and 4 tests failed
  (`TestLoomCuratorInstanceSeededFromFileDrop`,
  `TestLoomCuratorWake_FEPayloadShape`, `TestLoomCuratorWake_DeliversRealTurn`,
  `TestLoomCuratorScheduleSeededFromFileDrop`). This is real, currently-live
  Loom Curator functionality (Loom is a real `consumer_id`, per
  `docs/engineering/architecture/01-agent-construction.md`), so I traced it
  rather than just patching the test blind: in production, Loom Curator's
  `agent_profiles` row was already ingested under the old (pre-Round-2)
  mechanism and — per Round 1's still-in-force overwrite-freeze — stays
  exactly as-is on every future boot regardless of whether the file is ever
  scanned again; the only scenario this cut actually breaks is provisioning
  the row for the very first time in a genuinely fresh DB, which is exactly
  what this task's architecture says should no longer happen via automatic
  file-drop-on-boot for a non-seed agent. Fixed the test helper to
  reproduce "the row already exists" the way a real fresh environment now
  must produce it going forward — via `service.IngestAgentDefinition` (the
  one explicit, deliberate reimport path this task preserves, the same one
  the managed-agent-edit API route uses) — instead of relying on the cut
  automatic path. All downstream assertions (SyncManagedDurableAgentConfigs,
  the wake endpoint, real-turn delivery, schedule seeding) are unchanged and
  still pass, proving that machinery is unaffected by this task; only the
  *setup*, not the *behavior under test*, changed. Renamed
  `TestLoomCuratorInstanceSeededFromFileDrop` →
  `TestLoomCuratorInstanceSeededFromDurableConfigDrop` and
  `TestLoomCuratorScheduleSeededFromFileDrop` →
  `TestLoomCuratorScheduleSeededFromDurableConfigDrop` to name what's
  actually still being dropped-and-discovered (the durable-agent YAML
  config, an untouched mechanism) versus what no longer is (the agent
  profile itself). This is real product-functionality impact from doing
  the task as specified, exactly the "if the correction means the job is
  bigger than it looked... do the full job" case — done in full, not
  escalated, since it was fully within my ability to fix without touching
  any out-of-scope mechanism, and doesn't leave anything worse off for a
  genuinely fresh production environment than the architecture already
  says it should be.

**7. Real-backup-DB verification.** Copied
`~/.local/share/nanite/workspaces/default/backups/
main.db.pre-execution-backup-20260818-132726` to a scratch path. The backup
carries 28 `agent_profiles` rows (9 `internal` + 19 `project` — not "~33";
that figure in Round 1's own report came from a *live* `Discover()` run
against the repo's current on-disk corpus, not from the backup DB's actual
row count at the time it was taken; used the real, verified number here)
and 821 `skills` rows (820 `builtin` + 1 `user`). Wrote a temporary
`_test.go` (`internal/service/`, deleted after the run) that: opened the
copied DB via `store.New`; built a real `service.Container` against it with
`WorkingDir` pointed at this repo's actual root (so the real, currently
24-file `.nanite/agents/*.md` corpus is on disk during boot, same as a real
deploy); captured every pre-boot row's `system_prompt` by slug; called
`NewContainer` a second time (simulating a restart); asserted every
pre-existing slug is still present with an unchanged `system_prompt`, and
that 5 slugs with real on-disk `.md` files but no matching DB row as of the
backup (`frontend`, `plugin-dev-tasks`, `plugin-dev`, `reviewer-backend`,
`reviewer-frontend` — a genuine, pre-existing gap between disk and DB the
OLD code would have ingested on this very boot) are still absent afterward;
also asserted the `skills` table's total row count is unchanged (821).
**Note on total agent count**: it legitimately grows from 28 to 35 on this
boot — not a regression. The +7 comes entirely from the still-live,
out-of-scope nanite-native adapter tier (Phase 0 task 16, unaffected by
this task), which reads *this repo's own* `.nanite/config.yaml` `agents:`
block (7 real entries: `nanite-backend`, `nanite-frontend`,
`nanite-plugin-dev`, `nanite-planner`, `nanite-reviewer`,
`nanite-reviewer-backend`, `nanite-reviewer-frontend` — this very
engineering process's own worker sub-agent definitions), stamped
`Source: "nanite"`, a structurally different mechanism from the cut
project/user/plugin `.md`-directory scans. Verified this is legitimate and
unrelated by first-principles code reading of `adapter-nanite-native/
plugin.go`'s `Discover` (reads `.nanite/config.yaml`, not
`.nanite/agents/*.md`) before accepting it rather than assuming. A second
temporary test dropped a brand-new `.md` file into a fresh scratch project
dir (not this repo's tracked directory, to avoid Round 1's documented
"accidentally wrote to a tracked file" incident) before boot and confirmed
it produces no `agent_profiles` row. Both temporary tests passed; the file
was deleted after the run (not a permanent artifact, per Round 1's own
precedent) — confirmed via `git status` that no stray file was left behind
and that no tracked file (including `.nanite/agents/loom-curator.md`) was
modified.

**8. Live-verify against a real running scratch instance.** Checked port
availability first (`lsof -ti:<port>`) — several low ports were already in
use on this machine; used `18095` after confirming it was free. Built a
scratch binary (`go build -o <scratchdir>/nanite-scratch ./cmd/nanite/`),
copied the same real backup DB into a scratch directory with no
`.nanite/config.yaml`/`.nanite/agents/` (a clean environment, isolating the
project-tier claim from the nanite-native adapter's own separate, kept
behavior seen in step 7), and ran `nanite-scratch serve --port 18095 --db
<scratch>/main.db` from that directory as CWD (so `WorkingDir` defaults to
it). First boot: log shows `discovered file-based agents count=9` (exactly
the 9 internal/builtin profiles, zero from any cut tier) and `discovered
file-based skills count=8` (builtin only); `GET /api/agents` returned
exactly the pre-existing 28 rows (9 internal + 19 project), including
`loom-curator`, servable in full via `GET /api/agents/<id>` (200). Only
ERROR-level log line across the entire boot was a pre-existing, unrelated
warning about the internal `system-architect` profile declaring unknown
tool names (`unknownDeclaredTools`, untouched by this task). Stopped the
server, dropped a new `.nanite/agents/new-test-agent.md` file into the
scratch directory, rebooted: log again shows `discovered file-based agents
count=9` (unchanged), and `GET /api/agents` confirmed `new-test-agent` is
absent (still 28 total). Stopped the server (confirmed via `lsof -ti:<port>`
returning nothing after `kill`) and deleted every scratch file (binary, DB,
logs, dropped `.md` file) — confirmed via directory listing that only
pre-existing, unrelated scratch artifacts from an earlier, different
session remain in the shared scratchpad.

**9. Checks.**
- `go build ./cmd/nanite/` — pass.
- `go vet ./...` — pass except the same pre-existing, unrelated
  `container.go:1214/1234` ("stopReaper"/"stopRuntimeReaper" possible
  context leak) findings Round 1 already confirmed predate any of this
  task's changes (`git stash` of every changed file, re-ran
  `go vet ./internal/service/...`, identical findings, restored the stash).
- `go test ./internal/agent/... ./internal/skill/...` — pass.
- `go test ./internal/service/ -run "TestAutoIngest|TestIngestAgentDefinition" -v -count=1`
  — all 23 tests pass (22 pre-existing/renamed + 1 rewritten).
- `go test ./internal/api/... -run TestLoomCurator -v -count=1` — all 5
  Loom Curator tests pass (4 fixed + `TestLoomCuratorWake_MissingFragmentID`
  unaffected).
- `go test ./... -count=1` — pass across every package with test files, zero
  failures.

No schema change. `TASKS/INDEX.md` intentionally left untouched (Orchestrator
updates it after merge).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
