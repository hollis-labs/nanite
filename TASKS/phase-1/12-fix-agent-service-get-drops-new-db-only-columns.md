# Fix `AgentService.Get`/`GetBySlug`/`List` silently dropping `role_id`/`model_id`/`runtime_kind`/`consumer_id` for file-discovered agents

**Phase:** 1
**Status:** not-started
**Depends on:** `02-add-agents-composition-columns.md`, `03-add-consumers-table.md` (the columns this task's bug affects)
**Touches:** `internal/service/agent.go` (`agentServiceImpl.Get`/`GetBySlug`/`List`), `internal/agent/convert.go` (`Definition.ToProfile`)

## Context

Found during the Phase 1 Wave 2 live-dogfeed validation checkpoint (2026-08-18), not by any worker's own test suite — this is exactly the class of bug `docs/engineering/standards/testing.md` warns a green test suite alone won't catch, since it only reproduces through the full HTTP round-trip, not a direct store-layer call.

**The bug, verified directly:** `internal/service/agent.go`'s `agentServiceImpl.Get` (used by `handleGetAgent`, `handleUpdateAgent`, and everything else that resolves an agent by ID through the service layer) checks for an in-memory `agent.Definition` first (`findDefByID`/`findDefBySlug`) and, if found, returns `d.ToProfile()` — **never touching the DB row at all** for that call. `List` does the same thing (file-based defs take priority, DB agents are only appended for slugs not already covered by a def). `internal/agent/convert.go`'s `Definition.ToProfile()` has explicit handling for `ActivationMode` (`if d.ActivationMode != "" { p.ActivationMode = d.ActivationMode }`) but **zero handling** for `RoleID`, `ModelID`, `RuntimeKind` (added by Phase 1 `02`) or `ConsumerID` (added by Phase 1 `03`) — confirmed by grep, no occurrence of any of those four field names anywhere in `convert.go`.

This isn't a hypothetical — reproduced live: PUT an agent's `activation_mode` via `PUT /api/agents/{id}`, confirm the DB row directly via `sqlite3` shows the write landed correctly (`runtime_kind='api'`, backfilled by `02`'s migration), then `GET /api/agents/{id}` and see `"runtime_kind":""` — silently wrong, no error. Every one of the ~33 file-discovered agents in this project (`Source="project"`/`"user"`, the overwhelming majority of real agents, per `.nanite/agents/*.md`) hits this path. Only genuinely API-created agents with no matching in-memory `Definition` (rare — `Source="user"` agents created purely via `POST /api/agents` after boot, never present as a file) fall through to the real DB read (`s.agents.GetAgent(id)`) and see correct values.

**Why this matters now, not later:** `09-build-assignment-ui-api.md` (not yet started) is meant to build a picker UI that reads/writes exactly these fields (`role_id` selection, `consumer_id` tagging, `model_id`, `runtime_kind` display) — if this task doesn't land first, that UI will appear broken (values silently reverting to empty on every page load) for the overwhelming majority of real agents, and whoever builds/tests `09` will burn time debugging what looks like a UI bug but is actually this read-path bug one layer down. Separately, Phase 2's `01-wire-runtime-kind-routing.md` is explicitly building real CLI-vs-API routing decisions on `runtime_kind` — if Phase 2 reads it through this same `AgentService.Get`/`List` path without this fix landing first, routing would silently misbehave for every file-discovered agent, a much higher-stakes live bug than a UI cosmetic issue.

**Root cause is structural, not a missing field mapping.** `role_id`/`model_id`/`runtime_kind`/`consumer_id` have no YAML frontmatter representation at all — they are pure DB-only columns (per architecture doc `01-agent-construction.md` and this phase's own tasks `02`/`03`). `Definition.ToProfile()` can only ever return their zero-value for a file-backed agent, structurally, no matter what field-mapping code is added to it — there is nothing in the parsed file to map from. The actual fix is that `agentServiceImpl.Get`/`GetBySlug`/`List`'s in-memory-def-first strategy needs to stop being a full bypass of the DB for these specific columns: for a file-backed agent that also has a real `agent_profiles` row (which, post `08`, all of them do once ingested), the returned profile must carry the *DB's* values for `role_id`/`model_id`/`runtime_kind`/`consumer_id`/`activation_mode` (and any other future DB-only field), with the file's content winning only for fields the file actually declares (system_prompt, tools, etc. — the current `ToProfile()` shape). This is the same "DB is authoritative, file is not" principle task `08` already established for the boot-time ingest path — this task is the analogous fix for the *read* path.

## What to do

1. Confirm the full list of DB-only columns with no file/frontmatter representation that this bug affects — at minimum `role_id`, `model_id`, `runtime_kind` (`02`), `consumer_id` (`03`); check `activation_mode`/`class`/`default_state` too even though `ActivationMode` has *some* handling today (verify it's actually correct and not itself silently dropping a DB-set value that differs from the file's declared one — the current code takes the file's value if non-empty, which may itself be wrong once the DB is the authoritative source per `08`; decide and document whether `ActivationMode` needs the same DB-wins treatment as the newer fields, and if so, why the current partial handling wasn't already flagged before now).
2. Fix `agentServiceImpl.Get`/`GetBySlug`/`List`: for any file-backed def that resolves to an existing `agent_profiles` DB row, merge the DB row's values for every DB-only field into the returned profile — the file-derived `Definition.ToProfile()` result should not simply be returned as-is once a DB row exists. Decide the cleanest implementation shape (e.g., `ToProfile()` returns the file-only view, then `Get` overlays `s.agents.GetAgent(id)`'s DB-only fields on top when a row exists) rather than guessing — this touches the same file/DB precedence question `08` already worked through, so read that task's Work Log first for the established convention.
3. Handle the "file-backed but no DB row yet" case correctly (a genuinely new file not yet ingested) — these DB-only fields should default sensibly (empty/zero), not error.
4. Add a real regression test that reproduces the exact bug found here: create/seed a file-backed agent with a real DB row carrying non-default `role_id`/`model_id`/`runtime_kind`/`consumer_id`, call `Get`/`GetBySlug`/`List` through the service layer (not `store.GetAgent` directly), and assert the DB's values come through — this is the specific case every existing test missed.
5. Re-verify live (not just via `go test`) against a real running instance: `PUT` a change, confirm via direct DB query it landed, then `GET` the same agent via the API and confirm the DB's value — not the file's — is what comes back.

## Done means

- `GET /api/agents/{id}`, `GET /api/agents` (list), and `GET /api/agents?slug=...`-equivalent lookups for a file-backed agent with a real DB row correctly reflect that row's `role_id`/`model_id`/`runtime_kind`/`consumer_id` (and `activation_mode`/`class`/`default_state` if item 1 finds they need the same treatment) — verified live against a running instance, not just a unit test.
- A file-backed agent with no DB row yet still resolves sensibly (no crash, no wrong data).
- A real regression test exists that would have caught this exact bug (reads through the service layer, not the store layer directly).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
