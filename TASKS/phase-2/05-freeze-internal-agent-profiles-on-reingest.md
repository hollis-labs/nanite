# Freeze `source="internal"` agent profiles on re-ingest (redo of Phase 0 #10) + close self-tool bypass

**Phase:** 2
**Status:** implemented
**Depends on:** none directly (self-contained fix); coordinate timing with the Phase 1→main merge landing first — check whether that merge's task `08-kill-file-reingest-on-boot-pattern` already introduces an equivalent guard as a side effect before assuming a full re-implementation is needed
**Touches:** `internal/service/ingest.go` (`upsertAgentDef`), `internal/mcp/self_tools_transport.go` (`callUpdateAgent`, `callCreateAgent`)

## Context

`TASKS/phase-0/10-seed-builtin-agent-profiles.md`'s own Work Log claims this bug was already fixed — it describes an `alreadySeededInternal` guard added to `upsertAgentDef`, new regression tests (`TestAutoIngestAgents_InternalProfileNotReoverwritten`, etc.), and a status of `implemented`. **That claim is fiction relative to `main`** — direct grep of `internal/service/ingest.go` on 2026-08-19 found no such guard anywhere in the file (`alreadySeededInternal`, `SourceInternal`: zero matches). The real commit implementing that description never actually landed on `main` — this is the same "paperwork reconciliation without the real merge" failure pattern documented for other tasks in `PHASE-0-1-AUDIT-FOLLOWUPS.md`. Do not trust that Work Log; re-verify current behavior directly and re-implement from scratch.

The underlying bug, as originally described: `container.go`'s boot sequence appends `builtin.InternalProfiles()` (every embedded builtin profile, stamped `Source="internal"`) to file-discovered agent defs, then calls `AutoIngestAgents` unconditionally on every boot. `upsertAgentDef` currently calls `st.UpdateAgent(profile)` unconditionally whenever an existing `agent_profiles` row is found by ID/slug, regardless of source — overwriting `system_prompt`, `description`, `tools`, `tool_permissions`, `mcp_servers`, `settings`, and every other content column from the compiled file every single boot. Any customization made to a builtin agent's row survives only until the next restart.

There's also a second, related, more severe gap logged in `TASKS/ESCALATIONS.md` (2026-08-18, "Item 10: editability-gate reality check found a real bypass") but never fixed: `internal/mcp/self_tools_transport.go`'s `callUpdateAgent`/`callCreateAgent` (bound to the `agent_update`/`agent_create` self-tools) call `st.Store.GetAgent(id)`/`st.Store.UpdateAgent(a)` directly with **zero `ManageClass.Editable()` gating** — unlike every other mutating agent endpoint (`internal/api/agents.go`'s `handleUpdateAgent`/`handleDeleteAgent`, `internal/api/agent_capabilities.go`'s `requireMutableAgent`, all ~14 known-tool/known-skill/procedure/knowledge-seed mutation handlers). Any agent session with the `agent_update` self-tool available can update a `source="internal"` profile's content directly, bypassing the editability gate entirely — and before this fix, the next boot's `AutoIngestAgents` would silently revert that edit anyway, masking the bypass.

## What to do

1. Verify current `ingest.go` behavior directly against `main` before implementing anything — don't trust the stale task-10 Work Log.
2. Implement (or, if the Phase 1 merge's `08-kill-file-reingest-on-boot-pattern` already provides an equivalent mechanism, confirm and reuse it) a guard in `upsertAgentDef`: when an existing row is found and its `Source == "internal"`, skip the content-overwriting `UpdateAgent` call — treat the first successful `CreateAgent` as the one-time seed.
3. Check the secondary seed paths `seedProcedures`/`seedRoleSkills` for the same non-idempotent-overwrite problem (both use upserts that re-stomp on every boot per the original investigation) and gate them the same way if still true; `seedRoleToolsFromIngest` was found already-safe (`INSERT OR REPLACE` + a documented no-op `BumpActivation`) — verify that's still accurate before leaving it ungated.
4. Gate `callUpdateAgent`/`callCreateAgent` in `self_tools_transport.go` against `ManageClass.Editable()`, matching the `requireMutableAgent` pattern already used everywhere else.
5. Preserve the trust-tier reconciliation (`ingest.go`'s unconditional `UPDATE agent_profiles SET default_trust_tier = ?`) unless a specific reason to gate it turns up — it's a narrower, source-driven field, not user-editable content.
6. Tests: a direct DB mutation to a `source="internal"` profile survives a subsequent `AutoIngestAgents` re-run with an unchanged file-derived def; a genuinely new internal profile still gets created normally in the same batch; the self-tools reject a `source="internal"` target the same way the REST API does.

## Done means

- A `source="internal"` agent profile's content fields are written once, on first ingest, and never overwritten by a subsequent boot's re-run of `AutoIngestAgents` for that same row.
- A genuinely new internal profile (new file under `internal/agent/builtin/profiles/*.md`, no existing row) still seeds normally on first boot.
- `agent_update`/`agent_create` self-tools reject mutating a `source="internal"` profile, same as the REST API.
- Work Log records whether the Phase 1→main merge's `08` already covered part of this, and what (if anything) still needed a fresh fix.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including `internal/service/...` and `internal/mcp/...`.

## Work log

**Verification against `main` before touching anything (per task step 1 and this
task's own Context warning).** This worktree's HEAD (`9b1b6e22`, "Merge Phase 2
#03: mandatory post-compaction project CLAUDE.md/AGENTS.md re-read") *is* `main`
(`git merge-base --is-ancestor HEAD main` and a direct SHA compare both confirm
identity, not just ancestry). Confirmed the task's own Context claim directly:
`grep -rn "alreadySeededInternal\|SourceInternal" internal/service/ingest.go`
returns zero matches — that specific guard, as task 10's Work Log described it,
never landed. But that is not the end of the story: reading `internal/service/
ingest.go` in full on this HEAD shows both bundled fixes this task asks for are
**already present, under different names, landed by two other tasks**:

1. **The `upsertAgentDef` freeze-on-reingest guard (this task's item 1/2).**
   `TASKS/phase-1/08-kill-file-reingest-on-boot-pattern.md` (commit `448269d6`,
   confirmed an ancestor of `main` via `git merge-base --is-ancestor`) added a
   `bootPass bool` parameter to `upsertAgentDef` and the guard
   `if !(bootPass && existing.Source == profile.Source) { UpdateAgent(...) }`
   — i.e. on a boot-time pass, any row already ingested under its current
   source (not just `source="internal"` — `08`'s own scope was deliberately
   broader, "regardless of source") is frozen against content overwrite,
   except for a genuine one-time provenance transition
   (`existing.Source != profile.Source`, e.g. the historical
   `builtin`→`internal` flip). `source="internal"` is a strict subset of what
   this already covers. Per this task's own step 2 instruction ("if the
   Phase 1 merge's `08` already provides an equivalent mechanism, confirm and
   reuse it") — confirmed, reused, no re-implementation needed or attempted.

2. **The `self_tools_transport.go` `callUpdateAgent`/`callCreateAgent`
   editability-gate bypass (this task's item 4, ESCALATIONS.md Item 10).**
   A separate task, `TASKS/phase-0/34-gate-agent-update-create-editable-check.md`
   (commit `67a1e1bf`, also confirmed an ancestor of `main`), closed exactly
   this gap: added an `AgentClassifier` interface + `classifyAgent` helper to
   `SelfToolsTransport`, wired to the same `AgentConfigService` instance the
   REST layer (`handleUpdateAgent`/`requireMutableAgent`) uses, and gated both
   `callUpdateAgent` (before the `UpdateAgent` write) and `callCreateAgent`
   (on a slug-collision with a non-editable existing profile, before the
   `CreateAgent` write) on `class.Editable()`. Read both functions directly in
   `internal/mcp/self_tools_transport.go` (current lines ~696-736 and
   ~635-671) and confirmed the gate is live, not vestigial: `agentNotEditableError`
   returns the REST-matching rejection message shape, and the gate runs before
   any store write in both functions.

3. **Secondary seed paths (`seedProcedures`/`seedRoleToolsFromIngest`, this
   task's item 3).** Task `08`'s Round 1 Work Log documents finding, during
   its own investigation, that `seedRoleToolsFromIngest`'s underlying store
   call (`InsertAgentKnownTool`, `INSERT OR REPLACE`) is reachable from a real
   REST endpoint (`handleUpdateAgentKnownTool`) — contradicting task 10's
   original "wholly file-derived, no other writer" framing that this task's
   own step 3 repeats ("`seedRoleToolsFromIngest` was found already-safe...
   verify that's still accurate before leaving it ungated"). That framing is
   stale: current `ingest.go` (lines ~272-303) gates *both* `seedProcedures`
   and `seedRoleToolsFromIngest` behind a `freshContent` flag (true only when
   the row was just created, or a provenance transition just completed) —
   i.e. it is **not** left ungated; it's already gated, more conservatively
   than this task's own text anticipated. Verified this is a real, load-
   bearing gate by reading the code directly, not by trusting either Work
   Log's prose. Also checked the `BumpActivation` reference in this task's
   step 3 text: confirmed `store.BumpActivation` exists and is a documented
   no-op (FU-14, 2026-05-20, `internal/store/agent_known_tools_test.go`), but
   it has **zero non-test call sites** anywhere in `internal/` — it is not
   called from `seedRoleToolsFromIngest` (which calls `InsertAgentKnownTool`
   and `GrantAgentTool` only) or from anywhere else in production code today.
   Not in this task's scope to clean up (task 10's original text conflated
   this with a different, no-longer-accurate description of
   `seedRoleToolsFromIngest`'s internals); noted here for the record only.
   No `seedRoleSkills` function exists anywhere in the codebase (grepped
   `internal/service/*.go` for `func seed`) — this task's step 3 mention of
   it is imprecise language for the same two functions already covered above,
   not a reference to a third, distinct function.

**Per worker step 7 (decision vs. rationale) and this task's own step 2**, this
is not grounds to stop or narrow scope: the *decision* (freeze `source="internal"`
content after first seed; close the self-tool editability bypass) still stands
and is fully implemented — it just landed via two other tasks' commits before
this task file was worked, exactly the scenario the task's own Context section
anticipated and instructed me to check for. No source code changes were needed
for either of the two bundled fixes.

**Test coverage against this task's literal "Done means"/step 6 criteria** —
checked what already exists rather than assuming coverage from the two fixes'
own (broader-scoped) tests:
- *"A `source="internal"` agent profile's content fields are written once...
  never overwritten by a subsequent boot's re-run."* Covered generically by
  `TestAutoIngestAgents_ReingestDoesNotOverwriteExistingRow` (source="user")
  and `TestAutoIngestAgents_DBEditSurvivesBootReingest` (source="project", a
  direct DB-side SQL mutation survives reingest) and specifically for the
  internal→internal freeze-after-flip case by
  `TestAutoIngestAgents_SourceFlipFromBuiltinToInternal`'s third pass (proves
  a row already at `source="internal"` freezes against a further boot pass
  with changed `def` content). None of the existing tests combined "direct
  DB-side mutation" + "source=internal" + "unchanged file-derived def" in one
  test exactly as this task's step 6 literally states, so added
  `TestAutoIngestAgents_DirectDBMutationToInternalProfileSurvivesBootReingest`
  (`internal/service/ingest_test.go`) to close that literal gap — same shape
  as `TestAutoIngestAgents_DBEditSurvivesBootReingest` but targeted at
  `source="internal"` specifically, the class this task exists to protect.
- *"A genuinely new internal profile still gets created normally in the same
  batch."* Already covered by
  `TestAutoIngestAgents_NewInternalDefStillIngestedAlongsideFrozenRow`
  (`internal/service/ingest_test.go`, task `08` Round 2's rewrite of the
  original project-sourced version) — proves a new `source="internal"` def
  alongside an already-frozen `source="internal"` row both ingest correctly
  in one `AutoIngestAgents` call. No new test needed.
- *"The self-tools reject a `source="internal"` target the same way the REST
  API does."* Already covered by
  `TestSelfToolsTransport_UpdateAgent_RejectsNonEditableClasses` and
  `TestSelfToolsTransport_CreateAgent_RejectsSlugCollisionWithNonEditableClasses`
  (`internal/mcp/self_tools_agent_editable_test.go`, task `34`'s tests) — both
  include an explicit `source="internal"` case (`ManageClassInternal`) among
  the three non-editable classes, asserting rejection with the expected
  "embedded internal harness profile" message and that the write never
  reaches the store. No new test needed.

**Trust-tier reconciliation (task step 5).** Confirmed
`ingest.go`'s unconditional `UPDATE agent_profiles SET default_trust_tier = ?`
(outside the `bootPass`/`freshContent` gates) is unchanged and still runs on
every ingest pass regardless of freeze state — left alone per this task's own
explicit instruction, no reason found to gate it.

**Checks.**
- `go build ./cmd/nanite/` — pass.
- `go vet ./...` — pass except the same pre-existing, unrelated
  `internal/service/container.go` `stopReaper`/`stopRuntimeReaper` "possible
  context leak" findings already documented in `TASKS/phase-1/08`'s Work Log
  (line numbers shifted slightly, same functions/same file) — re-confirmed
  pre-existing via `git stash` against this task's one-file diff and re-running
  `go vet ./internal/service/...` against clean HEAD (identical findings),
  then restored the stash immediately (single-worktree, no concurrent-session
  collision risk observed via `git status --short` before and after).
- `go test ./internal/service/... ./internal/mcp/... -count=1` — pass,
  including the new test.
- `go test ./... -count=1` — pass across every package with test files, zero
  failures.

No schema change; no real-backup-DB verification step performed for this task
specifically (no new write-path code was added — the only change is one new
regression test exercising the existing, already-verified freeze logic via an
in-memory SQLite store, same pattern every other `ingest_test.go` test uses).
Tasks `08` and `34` each already performed their own real-backup-DB and/or
live-scratch-server verification of the underlying mechanisms this task
confirms, documented in their own Work Logs.

`TASKS/INDEX.md` intentionally left untouched (Orchestrator convention, workers
don't edit it).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
