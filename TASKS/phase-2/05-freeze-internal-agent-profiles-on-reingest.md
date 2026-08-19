# Freeze `source="internal"` agent profiles on re-ingest (redo of Phase 0 #10) + close self-tool bypass

**Phase:** 2
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
