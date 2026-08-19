# Kill the file-reingest-on-boot pattern, in full — enumerated, not generalized

**Phase:** 1
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
