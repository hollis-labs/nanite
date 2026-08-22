# Handoff — Skills batch (`TASKS/skills/`)

For the next Orchestrator/session picking up Skills-adjacent work. You have zero shared context
with this batch — everything you need to independently verify it actually landed is below. Do not
trust the status column in `TASKS/INDEX.md` alone; see "INDEX.md discrepancy" near the bottom
before you rely on it for anything.

## Bottom line

All 12 tasks (`TASKS/skills/01` through `12`) are implemented, validated, and reviewed. Every task
file's own `Status:` line in its Review notes reads `reviewed`. `TASKS/ESCALATIONS.md`'s final
entry on this batch (2026-08-22, "Task `12` fix re-reviewed PASS, task closed — the 12-task Skills
batch is now fully complete") confirms closure at the batch level. This is a real, substantially
complete build of `docs/engineering/architecture/20-skills.md` — not a partial or stubbed pass.

## What shipped, by pipeline stage

1. **Clean-slate cut (task `01`).** Deleted the entire pre-existing, broken skills mechanism:
   `skill.Discover`/`DiscoverOptions` (file-based discovery, already a stub), `AutoIngestSkills`/
   `upsertSkillDef`, the 8 embedded builtin skills (`internal/skill/builtin/`) and their DB seed
   path, `mcp.Manager.AutoDiscover`'s skill-row-creation side effect (tool discovery itself is
   untouched), the dead `` !`cmd` `` marker execution logic (`internal/skill/context.go`, no real
   callers), the orphaned `BrokerHints` field, and the `skill_create`/`skill_update` self-tools
   (structurally incompatible with "skills are authored packages only" — no way to represent a
   real `SKILL.md` + `scripts/`/`references/`/`assets/` package via flat CRUD fields).
2. **DB index + vendored-content-store foundation (tasks `02`, `03`).** `skills` table rebuilt as
   a pure index (migration `136`): `ID, Name, Slug, Description, Category, Icon, InputSchema,
   SourceTier, ContentHash, Version, Enabled, DeclaredDependencies, InstalledAt, UpdatedAt` — no
   `Prompt`, `ToolBindings`, `IsBuiltin`, `Settings`, or `ModeIDs`. `agent_known_skills` extended
   in place (migration `137`) with four new grant-state columns
   (`approved_content_hash`, `granted_at`, `granted_by`, `capabilities_granted`); the separate,
   confirmed-zero-rows `agent_skills` join table was dropped in the same migration.
   `internal/skillvendor` is a brand-new, content-addressed filesystem store for a whole package
   tree (`SKILL.md` + `scripts/`/`references/`/`assets/`), address format `skl-vendor-<hash[:16]>`,
   immutable once written, with real corruption/disk-loss detection.
3. **Explicit, single-target install/sync (tasks `04`, `05`).** `internal/skillinstall.Installer`
   parses a real `SKILL.md` package (`scripts:`/`references:`/`assets:`/`parameters:`/
   `dependencies:` frontmatter), validates it, vendors it, and upserts the index row —
   `NotInstalled → Parsing → Validating → Vendoring → Indexing → Ready`, no partial state on
   failure. Install-time cycle/recursion-limit detection (task `07`, folded into this pipeline —
   see below) rejects a dependency graph that would cycle or exceed depth 5. Exposed via
   `POST /api/skills/install`, `POST /api/skills/{slug}/sync`, and `nanite skill install|sync`.
4. **Materialization pipeline (tasks `06`, `07`, `08`).** `internal/skill/resolver.go`'s
   `ResolveSkillParameters` binds a skill's declared parameters to caller-supplied static args or
   an existing `agent_context_resolvers` row (static always wins); `ResolveDependencyAddresses`
   does one-level dependency-to-vendored-address lookup. `internal/skill/compose.go`'s
   `MaterializeSkill` gives `Context: inline|fork` real semantics — `inline` recursively splices a
   nested skill's materialized content into the parent's output; `fork` delegates to a real
   `subagent.Service.Spawn`/`.Status` call and folds back only the terminal result. `internal/
   skill/exec.go` rebuilds the `` !`cmd` `` marker with real markdown-fence-awareness (the old
   marker's core flaw) plus a mid-line-positioning check matching the real Agent-Skills-spec rule,
   and adds explicit, named `scripts/` file execution — both route through a `GatedExecutor`
   interface, never a direct `exec.Command`.
5. **Sandbox + capability gate (task `09`).** `internal/skill/gate.go`'s `Gate` is the sole
   production implementation of `GatedExecutor`. It looks up the agent's `agent_known_skills` grant
   row, refuses outright if none exists or it's never been approved, refuses with a distinct
   re-approval-required error if the grant's `approved_content_hash` no longer matches the skill's
   current `ContentHash`, and otherwise composes a `go-sandbox` `sandbox.Profile` from the grant's
   `capabilities_granted` JSON and calls `sandbox.Apply` for real. The inherited process environment
   is unconditionally secret-filtered before every execution, independent of any grant (see bug #7
   below).
6. **Two delivery adapters (tasks `10`, `11`) sharing one vendored source.** CLI-hosted agents
   (Claude, OpenCode) get a granted, currently-approved skill's files planted directly into their
   native skill location (`.claude/skills/<slug>/`, `skills/<slug>/` + `.opencode/skills/<slug>/`)
   at boot and re-planted every turn without a session restart — no Nanite-specific rendering,
   nothing added to the system prompt. Codex has no native skill mechanism at all (confirmed, not
   guessed) and gets no planting. API-direct agents get a new `skill_get` self-tool — not
   `skill_invoke` — that runs the full Resolver → Materializer → Gate pipeline and returns
   materialized content as a tool result, gated by the same `Gate.Authorize` check.
7. **Remaining REST surface (task `12`).** `GET /api/skills`/`{slug}`, `POST`/`GET`/`DELETE
   /api/agents/{id}/skills/{slug}/grant` (the real assign/revoke-with-approval action, a new
   parallel route family — deliberately not folded into the pre-existing known-skills CRUD, to
   avoid letting a plain Panel edit forge or wipe grant state), `POST /api/skills/{slug}/preview`
   (runs the same gated pipeline `skill_get` does — preview does **not** bypass the grant check),
   and `DELETE /api/skills/{slug}` (real uninstall: vendored copy deleted before the index row,
   never the reverse). `skill_delete`'s self-tool body was rewired to call the identical
   `skillinstall.Uninstaller` the REST endpoint uses.

## Real corrections/decisions made during planning and execution

- **`skill_get`, not `skill_invoke`.** The architecture doc's own placeholder phrasing suggested
  `skill_invoke`; the batch's planning pass renamed it to match `docs/tool-naming-convention.md`'s
  `get`-verb precedent and the already-renamed bare `skill_*` self-tool family (`docs/
  tool-naming-audit.md`). See `TASKS/ESCALATIONS.md`'s 2026-08-21 entry, "Skills planning:
  `skill_create`/`skill_update` self-tools cut... new content-retrieval tool named `skill_get`."
- **`policy.Engine`/`policy.Store` dormancy.** `docs/engineering/architecture/20-skills.md` assumed
  skill capability enforcement would "plug into the same `policy.Engine`/`policy.Store` shape the
  host already runs" (per `16-agent-host.md`). Planning-session research found `wrapper.Config.
  Policy` is never set anywhere in Nanite — that mechanism is accurate as a library description but
  dormant in production, with nothing live to plug into. **Resolution (task `09`)**: narrow, direct
  capability enforcement at the skill execution call site itself, gated on task `02`'s
  `agent_known_skills` columns — the same shape `TASKS/plugin-system/06` independently arrived at
  for RPC-proxy enforcement, for the identical reason. No code is shared with the dormant
  `policy` package; only its decision-mode vocabulary (observe/nudge/rewrite/block/approval) is
  loosely mirrored for future compatibility. See `TASKS/ESCALATIONS.md`'s 2026-08-21 entry, "Skills
  planning: `policy.Engine`/`policy.Store` confirmed dormant in Nanite."
- **`agent_known_skills` reuse, not a third table.** `20-skills.md` left open whether the new
  grant/telemetry table should reuse `agent_known_skills` or build a new one. Research confirmed
  `agent_known_skills` is live (REST CRUD, two frontend surfaces) and already cited by
  `13-memory-and-knowledge-tools.md` §4a as the reference catalog+attachment pattern. **Resolution
  (task `02`)**: extend it in place with four additive, nullable grant-state columns; drop the
  separate, confirmed-zero-rows `agent_skills` table outright. See `TASKS/ESCALATIONS.md`'s
  2026-08-21 entry, "Skills planning: `agent_known_skills` reused and extended as the grant/
  attachment table, not replaced."

## Concrete verification steps — confirm this actually landed before trusting it

Run these yourself; don't take the task files' word for it.

```bash
# Migrations exist and are the current head-adjacent pair
ls internal/store/migrations/ | grep -E '^13[4-9]'
#   ... 136_skills_index_redesign.sql
#   ... 137_agent_known_skills_grant_state_and_drop_agent_skills.sql

# Core packages exist
ls internal/skillvendor/          # address.go, store.go, errors.go, doc.go
ls internal/skillinstall/         # install.go, dependency_graph.go, uninstall.go, validate.go
ls internal/skill/                # resolver.go, compose.go, exec.go, gate.go, load.go

# Key functions exist with the signatures the pipeline depends on
grep -n 'func NewGate\|func (g \*Gate) ExecuteGated\|func (g \*Gate) Authorize' internal/skill/gate.go
grep -n 'func MaterializeSkill' internal/skill/compose.go
grep -n 'func ResolveSkillParameters\|func ResolveDependencyAddresses' internal/skill/resolver.go
grep -n 'func FindInlineMarkers\|func ResolveInlineMarkers\|func ExecuteScript' internal/skill/exec.go

# Delivery adapters
ls internal/runtime/agent/skill_plant.go
ls internal/selftools/self_tools_skill_get.go

# REST + CLI surface
grep -n 'skills/install\|skills/{slug}/sync\|skills/{slug}/grant\|skills/{slug}/preview' internal/api/api.go
ls cmd/nanite/skill_cmd.go

# The cut is real — should return zero hits
grep -rn 'skill.Discover\b\|AutoIngestSkills\|upsertSkillDef\|skillbuiltin\.' internal/
grep -rn '"skill_create"\|"skill_update"' internal/selftools/

# Full test suite for the batch's own packages
go build ./cmd/nanite/
go test ./internal/skill/... ./internal/skillinstall/... ./internal/skillvendor/... \
  ./internal/api/... ./internal/selftools/... ./internal/runtime/agent/... -race -count=1
```

`docs/engineering/GLOSSARY.md` should also have real entries for **Skill**, **Skill catalog**,
**Skill attachment**, **Skill vendor store**, **Skill Resolver**, **Skill Materializer**, and
**Skill capability gate** — check these exist and cross-reference each other before assuming the
vocabulary is settled; they were landed incrementally across tasks `02`/`03`/`06`/`07`/`09`.

## Bugs found and fixed during review (nine total — know these before building on top of this code)

Every one of these was found by a fresh reviewer with no shared context with the worker, fixed in
one round, and independently re-reviewed PASS (several via direct mutation testing). If you're
extending any of these files, read the specific task's Work Log / "Fix required" section before
assuming the current shape is arbitrary — each fix closes a real, reproduced defect.

1. **Task `02` — grant/removal collision.** Collapsing `agent_skills` into `agent_known_skills`
   meant the pre-existing bare "assign a skill to an agent" write path and the new "grant with
   approval" write path silently collided on the same `(agent_id, skill_name)` row: a
   `409 Conflict` on a legitimate first known-skill grant, and an unconditional `DELETE` that could
   destroy real grant/telemetry data set by the other path. Fixed with
   `AgentKnownSkill.IsBareAssignment()` — a shared "is this row bare" test both write paths now
   consult before conflicting/deleting.
2. **Task `04` — permanent `file-<slug>` ID collision.** `ToStoreSkill()`'s deterministic
   `file-<slug>` ID (built for a different, always-inert caller) leaked into the real install
   pipeline's `CreateSkill` call, giving every installed skill a permanent primary key matching the
   retired file-based-skill sentinel — making it un-updatable/un-deletable via the live REST API.
   Fixed by clearing the ID before a genuinely new install so the store's own UUID fallback fires.
3. **Task `05` — zero test coverage on new, security-relevant logic.** The install/sync REST
   handlers' 422-vs-500 error classification and the sync slug-mismatch guard (preventing a caller
   from mutating a different skill's row) had zero automated tests despite behaving correctly.
   Fixed with `httptest`-based coverage against the real handlers.
4. **Task `06` — doc/code dedup mismatch.** `MissingSkillParameterError.Names`'s doc comment
   claimed a deduplicated list; the code only sorted. Not reachable via the one real production
   path at the time, but a real contract violation for any direct caller. Fixed with a real dedupe
   step plus a regression test.
5. **Task `07` — `fork` composition unreachable in production.** `runFork` treated an
   approval-gated subagent spawn (`StatusRequested`/`StatusApproved` — the default outcome under
   `SubagentApprovalRequired=true`, the documented production default) as an opaque internal
   failure rather than mirroring the sibling `subagent_spawn` self-tool's own "this is not a
   failure" handling. Every `fork`-composed skill would have failed on first contact for any
   non-`TrustTrusted` role. Fixed with a typed `ForkPendingApprovalError` carrying the run ID/
   envelope instance ID.
6. **Task `08` — marker regex ignored the character preceding `!`.** The real Agent-Skills-spec
   rule is that `!` only starts a marker at line-start or after whitespace; a documentation-adjacent
   string like `` KEY=!`cmd` `` would have executed. Fixed with a preceding-character check matching
   the spec verbatim.
7. **Task `09` — unfiltered secret leak (the batch's most serious finding).** `Gate.run` never set
   `cmd.Env`, so a sandboxed skill's `` !`env` `` marker got the full, unfiltered host process
   environment back verbatim in model-visible output — unconditionally, no elevated capability grant
   needed. Fixed with an unconditional secret-filtering floor on the inherited environment,
   independent of any grant. The re-review mutation-tested this by disabling the fix and confirming
   a real, live secret token leaked before restoring it.
8. **Task `10` — path traversal via an unvalidated skill slug.** `Skill.Slug` has no format
   validation anywhere in the codebase; a slug like `"../.."` would cancel the boot-dir destination
   prefix entirely via `path.Clean`, silently overwriting the agent's own `CLAUDE.md`/system-prompt
   file with zero error. Fixed with `skillDestPrefixSafe`, which rejects any slug whose cleaned
   destination doesn't literally match its own uncleaned prefix concatenation. The re-review fuzzed
   ~25 adversarial slugs to confirm the invariant holds generally, not just for the two reproduction
   cases.
9. **Task `12` — two minor accuracy findings.** A doc comment misattributed a security claim to
   the wrong `ESCALATIONS.md` entries and overstated a categorical claim about caller-identity
   access control (the actual claim is narrower and correct: no comparable agent-mutating endpoint
   in this codebase gates on caller identity, only on which agent record may be mutated); and a real
   typed-nil guard (`(*skillvendor.Store)(nil)`) had no test that would catch its accidental removal.
   Both fixed in one round.

## Follow-up candidates explicitly filed, not fixed — genuinely open for a future session

- **Pre-existing typed-nil hazard in task `11`'s fork-composition wiring.** `self_tools_skill_get.go`'s
  `MaterializerDeps{Subagent: st.Subagent}` has the same typed-nil shape task `12`'s reviewer fixed
  elsewhere, but it's production-inert (`Container.Subagent` is always concretely constructed at
  boot) and was left untouched as out of task `12`'s scope. Low priority, but a real gap if a future
  refactor ever makes `Subagent` optionally nil.
- **Preview cannot materialize `fork`-composed skills.** `handlePreviewSkill` (task `12`) never
  populates `MaterializeInput.ParentSessionID` — there is no live chat session for a REST-only
  preview call to derive one from. This fails cleanly (a real, non-panicking error), not silently,
  but a `fork`-composed skill can never be previewed via `POST /api/skills/{slug}/preview` today.
  Fixing this is a genuine design decision (what would "preview forks a subagent" even mean outside
  a live turn?), not a small patch — a real, separate follow-up if authoring tooling on this
  endpoint is ever built out.
- **Pre-existing, unrelated flaky race in `chat_boot_drive.go`.** Surfaced during task `10`'s
  re-review verification, not introduced by this batch: a `send on closed channel` panic in
  `driveBootSession`'s background `SendInput`-failure-handling goroutine (a TOCTOU race against a
  concurrent channel close), confirmed via `git blame` to predate this batch by months (commit
  `7a0e37936`, 2026-05-19). Not a Skills-batch defect — flagged here because it was found while
  verifying Skills-batch code, and whoever next owns `chat_boot_drive.go` should know about it.
- **Two known, out-of-scope gaps confirmed but not fixed, per this batch's own explicit scope
  fences (not oversights):** `ui/src/components/settings/SkillsBrowser.tsx` (the admin
  catalog-browsing page, distinct from the Wizard/Panel surfaces this batch protects) will genuinely
  crash on render against the new API response shape — it reads `skill.settings`/`tool_bindings`/
  `is_builtin`/`prompt`, all four removed by task `02`. Frontend work is out of scope for this whole
  batch; whoever picks up the frontend stream needs to fix this page, not just extend it.
  Separately, `default_provider`/`runtime_kind` and the skill grant-state columns
  (`approved_content_hash`/`capabilities_granted`) had no REST-settable path at all until task `12`
  landed (`12` closes the grant-state gap; `default_provider`/`runtime_kind` on `CreateAgentRequest`/
  `UpdateAgentRequest` remains a real, separate, unrelated REST-surface gap discovered during task
  `10`'s dogfeed).
- **Now-fully-dead skill-slash-command scaffolding** (found during task `01`'s review, not part of
  this batch's own scope): `internal/service/skill.go`'s `RegisterSkillCommands`/
  `SkillCommandRegistrar`, `internal/chat/commands.go`'s `RegisterSkillCommand`, and the
  `IsFileBasedID`-guarded branches in `SkillService.Get/GetBySlug/Update/Delete` are now 100% dead
  code (the 8 builtin skills that were the last live producer of file-based skill defs are gone).
  The backend half is fair game for a small cleanup task in a future batch. **The frontend half
  (`ChatComposer.tsx`'s `action === "skill"` handling) is explicitly out of scope for any backend
  batch** per this project's standing "no frontend work in a backend phase" rule — it needs its own
  frontend-scoped follow-up.
- **`fork_role`/subagent-spawn privilege observation (task `11`'s review, not a task-11 bug).** Any
  agent holding `skill_get` plus a grant on a `fork`-composed skill can trigger a real
  `subagent.Service.Spawn`, independent of whether `subagent_spawn` is separately on that agent's
  own tool allowlist — an inherited property of `fork` composition's design (task `07`), not
  something task `11` introduced. Worth a look if a future task tightens per-tool capability
  allowlisting.

## Discipline note, not code: `git stash` was run twice mid-batch (tasks `03`, `08`)

Both were self-reported, both confirmed no data lost via independent `git stash list`/`git status`
checks. The second occurrence (task `08`) was treated differently: rather than re-logging the same
warning a third time, the rule was added directly into `.claude/agents/worker.md` — the file every
worker session actually receives as its system prompt — since a per-dispatch reminder had already
failed to prevent a repeat. If you're dispatching workers into this codebase going forward, this
should already be closed; if you see a third occurrence, that's worth escalating as a process gap,
not just logging again.

## INDEX.md discrepancy — check before trusting it

**As of this writing, the working-tree copy of `TASKS/INDEX.md` does not reflect the batch's actual
completion state, and it disagrees with `TASKS/INDEX.md` as committed on `main`.**

- The task files themselves (`TASKS/skills/01`-`12`, all committed and identical to `HEAD`) all
  show `Status: reviewed` in their Review notes.
- `TASKS/ESCALATIONS.md`'s closing entry (committed, 2026-08-22) states the batch is fully complete.
- `main` commit `a6453902` ("TASKS/INDEX.md: Skills batch fully complete — all 12 tasks reviewed")
  already updated the Skills section correctly — tasks `09`-`12` as `reviewed`, and a summary
  paragraph stating all 8 waves are implemented/validated/reviewed. This commit **is** an ancestor
  of the current `HEAD`.
- **But the current working tree has an uncommitted, unstaged modification to `TASKS/INDEX.md`
  (`git diff HEAD -- TASKS/INDEX.md`) that reverts exactly that update** — back to task `09` =
  `validated`, tasks `10`-`12` = `not-started`, and the summary paragraph back to "Planned
  2026-08-21, not yet dispatched." This is not a real regression in the underlying work — every
  other signal (task files, escalations, git history, and direct code inspection above) confirms
  the batch is genuinely done — but it means **the file on disk right now is wrong**, and if it gets
  committed as-is, it will silently reintroduce stale status into tracked history.
- This looks like accidental collateral from unrelated, concurrent uncommitted edits in the same
  working tree (`TASKS/audit-remediation/00-revalidate-baseline/02-refresh-tool-baseline-at-frozen-
  head.md` and `TASKS/audit-remediation/README.md` are also modified in this same working tree,
  suggesting a different batch's session touched this shared file), not a deliberate decision by
  anyone — but don't guess; check `git diff HEAD -- TASKS/INDEX.md` yourself and reconcile it
  (most likely `git checkout HEAD -- TASKS/INDEX.md` for the Skills section specifically, merged
  with whatever legitimate audit-remediation changes are also pending) before committing anything
  that touches this file.
