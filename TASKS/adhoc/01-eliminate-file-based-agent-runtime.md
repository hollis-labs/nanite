# Eliminate the file-based agent runtime entirely — seeds become plain DB creation, not a parallel Definition system

**Status:** not-started
**Depends on:** none (standalone, outside the Phase 2-9 sequence — see `TASKS/INDEX.md`'s "Ad hoc tasks" section)
**Blocks:** `TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md`
**Touches:** `internal/agent/convert.go`, `internal/agent/*.go` (`Definition`, `Discover`), `internal/service/agent.go` (`agentServiceImpl`), `internal/service/ingest.go` (`upsertAgentDef`, `AutoIngestAgents`), `internal/service/agent_config.go`, `internal/service/agent_permissions.go`, `internal/api/agent_tools.go`, `internal/messaging/validate.go`, `internal/agent/builtin/profiles/*.md` (9 files), `cmd/nanite/main.go` (wherever `discoverAndLoadAgents`-equivalent boot wiring lives)

## Context

**Operator's own words, verbatim (2026-08-19):** *"On `agent.IsFileBasedID(id)`, that needs to be eliminated too, we no longer need that. There will be no file based so this is dead code now too. The seed agents need to get proper UUIDs - they should be created in the NEW way, the seeds just being stored/default config. Anything related to file based agents needs to be eliminated."*

This supersedes an earlier, narrower framing (Orchestrator's own draft, and a research fork's proposal) of "just stamp `id: <uuid>` frontmatter into the builtin profile files so the identity split closes." That narrower fix is **not** what's wanted — the operator wants the file-based agent *concept* gone, not patched around. The 9 builtin profile `.md` files under `internal/agent/builtin/profiles/` become plain seed/default config, consumed once to create real agents through the same creation path a user/API-created agent goes through — not a parallel `agent.Definition`/`IsFileBasedID` runtime system that keeps living alongside the DB-backed model.

### The mechanism as it exists today (confirmed by direct code read, 2026-08-19)

`internal/agent/convert.go`:
```go
const fileIDPrefix = "file-"
func IsFileBasedID(id string) bool { ... }       // true iff id starts with "file-"
func SlugFromFileID(id string) string { ... }
func (d *Definition) CanonicalID() string {
    if id := strings.TrimSpace(d.ID); id != "" { return id }
    return fileIDPrefix + d.Slug                  // unstamped defs fall back to "file-<slug>"
}
```

None of the 9 builtin profile files carry `id:` frontmatter (confirmed — spot-checked `backend.md`, `background-job.md`, `default.md`; only `name`/`slug`/`description`/`icon` and role-specific fields are set), so every one of them resolves to `CanonicalID() == "file-<slug>"` today.

`internal/service/ingest.go`'s `upsertAgentDef` (~line 219-245): on first ingest of an unstamped definition, it explicitly blanks the ID before creating the row —
```go
if agentpkg.IsFileBasedID(profile.ID) {
    profile.ID = ""
}
...
if err := st.CreateAgent(profile); err != nil { ... }
```
— so `CreateAgent` mints a real, random UUID as the DB row's actual primary key. That UUID is *never* used as the runtime identity anywhere else in the system; every other code path keeps addressing the agent as `"file-<slug>"`.

`internal/service/agent.go`'s `agentServiceImpl.Get()`/`GetBySlug()` (~lines 95-119) is the second half of the split: it keeps an in-memory `[]*agent.Definition` (`s.fileDefs`, populated at boot from `agent.Discover()` + the compiled-in builtins) and, for **both** an `IsFileBasedID` lookup **and** a stamped-UUID lookup that happens to match an in-memory def, resolves through `resolveFileProfile(d)` (a pure `Definition → store.AgentProfile` conversion) **before ever consulting the real DB row** via `s.agents.GetAgent(id)`. The comment at line 104-106 states this is deliberate today ("an in-memory def, if loaded, wins... otherwise fall through to DB") — but per the operator's instruction above, this whole in-memory-registry-wins-over-DB behavior for the builtin population is exactly the thing to remove, not preserve.

`internal/service/agent_permissions.go`'s `newFileAgentPermissionResolver` (whole file, ~44 lines) is a `toolclient.PermissionResolver` built *exclusively* for `IsFileBasedID` IDs — gated entirely on `agent.IsFileBasedID(agentID)` at its single call site inside the returned closure. Dead code the moment no agent ID is ever file-based-shaped again.

### Every confirmed non-test call site of the agent-package's file-based machinery (2026-08-19 grep)

```
internal/agent/convert.go:14        func IsFileBasedID           (definition)
internal/agent/convert.go:20        SlugFromFileID's own guard    (definition)
internal/service/agent.go:98        Get() — IsFileBasedID branch
internal/service/agent.go:100-108   Get()/GetBySlug() — findDefBySlug/findDefByID/resolveFileProfile
internal/service/ingest.go:220      upsertAgentDef — identity resolution
internal/service/ingest.go:237      upsertAgentDef — ID-blanking before CreateAgent
internal/service/agent_config.go:109  (read before deciding what changes)
internal/service/agent_config.go:364  (read before deciding what changes)
internal/service/agent_permissions.go:36  newFileAgentPermissionResolver's closure
internal/api/agent_tools.go:158     requireRealAgentToolsTarget (hard-rejects grant/revoke against file-based IDs)
internal/messaging/validate.go:61   (read before deciding what changes — may be a legitimate distinct concern, e.g. validating that a message sender ID is well-formed, not necessarily "permission routing")
```

**Explicitly out of scope, do not touch**: `internal/skill/convert.go`'s own separate `IsFileBasedID`/`SlugFromFileID` — this is a **different subsystem** (skills, not agents) with its own, structurally similar but independent file-based concept. The operator's instruction was specifically about agents. If your investigation finds skills are *also* meant to go away, stop and flag it in `TASKS/ESCALATIONS.md` rather than silently expanding scope — don't guess.

## What to do

This is a structural elimination, not a narrow patch — treat the call-site list above as a starting map, not a complete spec; verify each one yourself and expect to find others.

1. **Confirm the current boot-time seeding flow precisely** before changing anything: trace how `internal/agent/builtin/profiles/*.md` gets from "embedded file" to "row in `agent_profiles`" today (`agent.Discover()`, `cmd/nanite/main.go`'s boot wiring, `AutoIngestAgents`/`upsertAgentDef`). Document this in your Work Log — this session's own `TASKS/INDEX.md` (line 13, "Status correction, 2026-08-18") already flagged one real bug in this exact area before (`AutoIngestAgents` unconditionally overwriting builtin rows on every boot, later addressed by `TASKS/phase-2/05-freeze-internal-agent-profiles-on-reingest.md`) — read that task's Work Log first so you understand what "freeze on reingest" currently means and don't regress it.

2. **Design the new seed path**: the 9 builtin profile files should seed a real agent row through the *same* creation path a user/API-created agent uses — real generated UUID from the start, no `CanonicalID()`/`fileIDPrefix` special-casing, no ID-blanking dance. This likely means: on first boot (or a one-time migration), for each builtin profile with no existing DB row (matched by slug, since there's no stable ID yet to match on), call the standard agent-creation path directly with a freshly generated UUID. After that first creation, the row is just a normal `agent_profiles` row like any other — the `.md` file becomes default/reference config only (for what "should exist out of the box," not a live runtime source of truth), consulted at most for the one-time seed, never again to override an existing row (preserving the intent of `phase-2/05`'s freeze, now for a stronger reason: there's no live definition to fall back to at all).

3. **One-time reconciliation for *this* existing deployment, not just fresh installs.** This deployment's 9 builtin agents already exist as real DB rows under whatever random UUIDs `upsertAgentDef`'s blank-then-`CreateAgent` dance minted for them historically, and those rows likely already have real FK-linked history (sessions, `event_log` rows, reflexes, any `agent_tools` grants). Your fix must **not** orphan that history — do not delete-and-recreate these 9 rows under new IDs. The existing rows, as they already stand, ARE the correct real DB-backed agents once the file-based lookup machinery around them is removed; the actual work here is deleting the *parallel* runtime system that keeps addressing them as `"file-<slug>"`, not re-minting their identity. Confirm this by checking whether anything outside this fix already depends on the literal string `"file-<slug>"` as a stored value anywhere (FK columns, `granted_via`, session records, etc.) — if so, that's a real migration concern to handle explicitly, not paper over.

4. **Remove the file-based runtime machinery**: `agent.IsFileBasedID`, `SlugFromFileID`, `fileIDPrefix`, `CanonicalID()`'s fallback branch (once every agent has a real ID, `CanonicalID()` should just return `d.ID` unconditionally — decide whether the method still needs to exist at all), `agentServiceImpl`'s `fileDefs`/`findDefByID`/`findDefBySlug`/`resolveFileProfile`/the in-memory-wins-over-DB branches in `Get()`/`GetBySlug()`/`List()`, and `newFileAgentPermissionResolver` in full (its removal belongs to `adhoc/02`, since it's tangled up with `tool_permissions` — leave the *function* in place but confirm it becomes unreachable dead code once `IsFileBasedID` always returns false; don't delete it in this task, that's `02`'s job, but don't leave a real, live, reachable file-based code path standing either).

5. Sweep every call site listed above (and any this task's own investigation finds that weren't listed) and collapse each to the DB-only path. For `internal/service/agent_config.go:109`/`:364` and `internal/messaging/validate.go:61`, read them fully first — the task list above deliberately didn't pre-judge what change each needs, since they weren't traced in this session's research pass.

6. Verify `internal/background/service.go:61`'s `const SenderAgentID = "background-job"` (used for envelope `from_agent_id` sender-routing tagging) is unaffected — it's a slug string, not a file-based-ID lookup, so it should be fine, but confirm rather than assume, since it sits adjacent to this exact territory.

7. Live-verify against the real deployed `nanite-api-service`: confirm all 9 builtin agents are present and functional after a clean redeploy (dispatch to `backend`, `researcher`, etc. still routes correctly), confirm a fresh `agent_tools` grant against one of them (e.g. `backend`) now works without the `requireRealAgentToolsTarget` 400 that currently blocks it, confirm `GET /api/agents/{id}/tools` for a builtin agent now reflects real `agent_tools` grants rather than the stale fallback.

## Done means

- `agent.IsFileBasedID` and every runtime consumer of it in agent-domain code (the call-site list above) no longer exist or no longer branch on it — confirm via a full-repo grep showing zero remaining non-test, non-skill-package hits.
- All 9 builtin agents exist as real `agent_profiles` rows with real UUIDs, reachable through the exact same `Get`/`GetBySlug`/tool-selection/tool-execution paths as any other DB-backed agent — no special-casing left.
- This deployment's existing builtin-agent rows and their FK history (sessions, `event_log`, reflexes, any `agent_tools` grants) survive the change intact — not recreated under new IDs.
- `go build ./cmd/nanite/`, `go vet ./...` (no new findings beyond the 2 pre-existing `container.go` ones), `go test ./...` all green.
- Live dogfeed against the real deployed service, per step 7 above, documented in the Work Log.

## Out of scope

- `internal/skill/convert.go`'s parallel file-based skill mechanism — flag if you think it should also go, don't touch it here.
- Removing `tool_permissions`/`CheckPermission`/`PermissionResolver`/the `toolPermissions` DB column or frontmatter field, and collapsing the 4 permission-check surfaces (`SelectForAgent`, `enforceExecutionRulesViaAgentTools`, `handleListAgentTools`, `requireRealAgentToolsTarget`) to be `agent_tools`-only — that's `TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md`, which depends on this task landing first (removing the permission system while any agent could still theoretically resolve as file-based would be unsafe to sequence the other way).
- The broader agent-roster audit (which agents to keep/cull/consolidate) — that's a separate planning task, `TASKS/phase-2/07-audit-agent-roster.md`.
