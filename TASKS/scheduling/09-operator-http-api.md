# Operator HTTP API — `/api/schedules`

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** implemented
**Depends on:** `02-store-adapter.md` (CRUD against the same `agent_schedules` shape the engine reads).
**Touches:** new `internal/api/schedules.go` (handlers), `internal/api/api.go` (route registration).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Producers" section, item 4: `/api/schedules` CRUD, mirroring Hadron's `/v1/schedules` surface, for operator/UI-driven schedule management outside of agent or reflex control. Check Hadron's own route handlers (`apps/hadron`'s API layer — grep for `schedules` in its route registration) for the shape to mirror before designing this from scratch; this repo's own `internal/api/workflows.go` (`handleListWorkflowRuns`, `handleGetWorkflowRun`, `handleCancelWorkflowRun`, `handleRunWorkflow`) is also a reasonable structural template for a resource-CRUD-plus-one-action API surface already living in this codebase.

**Auth/permission model, explicitly not decided by the design doc — this task makes a concrete, documented, non-final call:** whether `/api/schedules` needs provenance-tier-style gating analogous to reflexes (`10-reflex-action-taxonomy.md`'s Facet 3) or standard operator auth is sufficient. Given no plugin-registered schedule producer exists today (mirroring the same "no concrete plugin need exists today" reasoning `TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md` used for its own provenance gate), standard operator auth — whatever this codebase's existing operator-facing endpoints already use — is the recommended default. Document your call; this is not a security decision requiring further sign-off before landing, matching this repo's own precedent for "acceptable to leave open and let real usage inform the answer" (`TASKS/reflex-taxonomy/05`'s own Context, quoting the operator's identical reasoning for the analogous reflex question).

## What to do

1. `GET /api/schedules` — list, optionally filtered by `agent_id` (mirroring `ListAgentSchedules`'s existing scoping).
2. `GET /api/schedules/{id}` — fetch one.
3. `POST /api/schedules` — create. Validate the same fields `07`'s reflex hook and `08`'s self-tool handler validate (schedule kind, spec, job type/payload, retry policy) — consider whether this validation logic should be a single shared function all three producers call, rather than three independent implementations (the same "duplicated by necessity, not oversight" pattern this repo's other design docs keep flagging — except here, unlike the `render_card`-marker-consumers case, there's no import-cycle reason for these three producers to stay separate; a shared validator is likely the right call, document if you build one or document why not).
4. `PATCH /api/schedules/{id}` — update (status, retry policy, spec) — same immutability questions reflexes' own PATCH handler had to answer (`TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md`'s handling of `provenance_tier` as non-patchable): decide which fields are safely patchable post-creation and which aren't (e.g. should `job_type` be patchable, or does changing it require deleting and recreating the schedule?), document your call.
5. `DELETE /api/schedules/{id}`.
6. Expose `Engine.Status()` (`libs/go-scheduler/engine.go:25-30`) via a coarse liveness endpoint (e.g. `GET /api/schedules/status`), per the design doc's own "still worth exposing... as a coarse liveness signal" note — supplementary to, not a replacement for, `06`'s per-firing telemetry rows.

## Done means

- All five CRUD endpoints work against a real store, with request/response shapes documented (in code comments or this file's Work Log) since no OpenAPI/schema doc is asked for here.
- A regression test proves a `POST` with an invalid `schedule_kind`/malformed spec is rejected with a clear error, matching the same validation `07`/`08` apply (whether via a shared validator or independently — see step 3).
- A regression test proves the status endpoint returns `Engine.Status()`'s real fields.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your auth-model call (Context), your shared-validator call (step 3), and your patchable-fields call (step 4) are documented in this file's Work Log.

## Work log

Implemented against real current state, not the stale field name the task's own boilerplate might otherwise imply — confirmed `internal/service/container.go`'s schedule-engine field is `Engine *gosched.Engine` (not `ScheduleEngine`; a rename is logged as a follow-up in `TASKS/ESCALATIONS.md`'s 2026-08-20 entry, not done here, per this task's own explicit instruction not to do that rename as part of this task) before writing any code that reads it.

**Files touched:**
- `internal/api/schedules.go` (new) — all five CRUD handlers + `handleScheduleEngineStatus`, validators, request/response DTOs, all documented inline.
- `internal/api/api.go` — route registration (`GET/POST /api/schedules`, `GET /api/schedules/status`, `GET/PATCH/DELETE /api/schedules/{id}`).
- `internal/store/agent_schedules.go` — added `ListAllAgentSchedules` (new), the one store-layer addition needed to back the unfiltered case of `GET /api/schedules`; no existing method covers "every agent's schedules in one call" (`ListAgentSchedules` is agent-scoped by design, matching every other real caller of this table). Deliberately not added to the `AgentStateStore` interface — that seam exists for a future per-agent-file backend, and a cross-agent list isn't a per-agent-state concept it needs to reason about.
- `internal/api/schedules_test.go` (new) — regression tests, see below.

**Auth-model call (Context):** standard operator auth, no additional gating. Confirmed by reading `internal/server/server.go` before deciding: every `/api/*` route (this file's handlers included, once registered via `RegisterRoutes` on the same mux) already passes through the server's fixed middleware chain — `recover -> logging -> CORS -> basicAuthMiddleware -> callerIdentityMiddleware -> bodyLimitMiddleware` — applied uniformly at the mux level, not opted into per-route. No narrower "operator-only" tier exists below that already in use by any comparable endpoint (agents/reflexes/durable-agents/workflows all rely on the same uniform chain), and no plugin-registered schedule producer exists today that would need a provenance-tier boundary the way reflexes' Facet 3 does. So: nothing added at the handler level. This matches the task's own recommended default.

**Shared-validator call (step 3):** built one shared internal validator set for this file's own POST/PATCH pair (`validateScheduleKind`, `validateCronSpec`, `validateJobType`, `validateJobPayload`, `validateOnFailPolicy`, `validateScheduleStatus`), but did **not** attempt to share it with `07` (reflex hook) or `08` (self-tool) — confirmed both are still `not-started` in this worktree as of writing (`TASKS/scheduling/07-wire-add-schedule-reflex.md`, `08-agent-self-tool.md`), running in parallel in separate worktrees per this task's own briefing, with no live coordination channel. This is option (a) from the task's own framing: independent implementation now, deliberately factored into small single-field validators (rather than one monolithic block) so consolidating behind a single cross-producer validator later — once all three land and can be diffed side by side — is a mechanical extraction, not a rewrite. Flagged as a real follow-up candidate, not attempted here: the three producers validate the same conceptual fields but through different error-surfacing conventions (JSON error body here vs. a logged hook-failure warning in `07` vs. a tool-result error in `08`), so picking the right shared shape needs to see all three, not guess ahead of them.

`validateCronSpec` calls `robfig/cron/v3`'s own `ParseStandard` directly (the same parser `store.ComputeAgentScheduleNextRun` wraps) rather than hand-rolling a second cron-validity check — `ComputeAgentScheduleNextRun` itself can't be reused for validation alone, since it deliberately falls back to "due now" on a malformed spec rather than surfacing the parse error (correct for its own already-inserted-row use case, wrong for a pre-insert validation gate). This keeps the "don't hand-roll cron-parsing a second time" finding (already flagged once on task `05`) intact — one library call, no custom parsing logic.

**Patchable-fields call (step 4):** `name`, `session_id`, `schedule_spec`, `body`, `priority`, `status`, `expires_at`, `max_retries`, `on_fail`, `job_payload` are patchable in place via `schedulePatchRequest`. `agent_id`, `schedule_kind`, and `job_type` are deliberately **not** represented in that struct at all — an unknown JSON field targeting them is silently ignored by `json.Decode`, matching `TASKS/reflex-taxonomy/05`'s own `provenance_tier`-is-simply-absent-from-the-struct precedent exactly (verified with a regression test that a smuggled `agent_id`/`schedule_kind`/`job_type` in a PATCH body has zero effect). Rationale:
- `agent_id`: changing ownership is delete-and-recreate, not an edit — mirrors `handleDeleteAgentReflex`'s own "cannot patch/delete a different-agent resource through this endpoint" boundary for the adjacent resource.
- `schedule_kind`: cron vs. one_shot changes `next_run`'s entire interpretation and would need to be paired with a `schedule_spec` change atomically to stay coherent; safer to force delete+recreate than risk the two drifting out of sync mid-PATCH.
- `job_type`: `job_payload`'s valid shape is entirely determined by `job_type` (`internal/scheduler/runner_adapter.go`'s four Payload structs are not interchangeable). Allowing `job_type` to change without also replacing `job_payload` in the same request would produce a row that passes this handler's validation but fails only later, at dispatch time inside `RunnerAdapter.Enqueue` — a worse failure mode (silent until the next firing) than rejecting the operation up front.
- `job_payload` IS patchable on its own (unlike `job_type`) — a same-type payload edit (e.g. tweaking a `command_run`'s args) is a legitimate in-place change.
- `next_run`/`id`/`fired_count`/`last_fired_at`/`created_at`/`created_by` are engine/bookkeeping-owned and not exposed for direct PATCH. `next_run` is instead recomputed as a side effect whenever a patch could invalidate the previously-computed value: `schedule_spec` changing, or `status` transitioning into `active` from a non-active state (reactivating a paused/expired row with a stale next_run would otherwise misfire immediately or never fire again).

`PATCH` is implemented by loading the current row, applying only the patched fields onto a full copy, and calling `store.InsertAgentSchedule` (an `INSERT OR REPLACE` upsert keyed on `id`) — the same call `managed_durable_configs.go` already uses to update an existing row on resync, so this reuses an already-exercised upsert path rather than introducing a second one.

**Response/request shapes:** documented inline in `internal/api/schedules.go` next to each handler/type (no OpenAPI doc, per Done-means). Summary: `POST`/`PATCH` responses and `GET` (single/list) all return the real `store.AgentSchedule` row (its existing `json` tags); `POST`/`PATCH` request bodies are the new `scheduleCreateRequest`/`schedulePatchRequest` DTOs; `GET /api/schedules/status` returns `{configured, running, last_tick_at, dispatches, worker_errors}` — `configured=false` (all other fields zero-valued) when `Services.Engine` is nil (every test container's default, and any real process before `main.go`'s post-hoc wiring runs), `configured=true` with `gosched.Engine.Status()`'s real field values otherwise.

**Regression tests** (`internal/api/schedules_test.go`), all passing:
- `TestSchedulesAPI_CreateGetPatchDeleteLifecycle` — full CRUD round-trip against a real store, including the immutable-field-smuggling-via-PATCH proof.
- `TestSchedulesAPI_ListFiltersByAgentID` — `agent_id` filter scoping vs. unfiltered cross-agent list.
- `TestSchedulesAPI_CreateRejectsUnknownAgent` — FK-adjacent validation (agent must resolve via `Services.Agents.Get`).
- `TestSchedulesAPI_CreateRejectsInvalidScheduleKind` — **required regression test** (Done-means bullet 2, invalid `schedule_kind` half).
- `TestSchedulesAPI_CreateRejectsMalformedCronSpec` — **required regression test** (Done-means bullet 2, malformed spec half).
- `TestSchedulesAPI_CreateRejectsMalformedJobPayload` — invalid-JSON `job_payload` rejected at create time.
- `TestSchedulesAPI_PatchUnknownID404s` / `TestSchedulesAPI_DeleteUnknownID404s` — not-found paths.
- `TestSchedulesAPI_StatusEndpoint` — **required regression test** (Done-means bullet 3): proves the unconfigured (`Engine` nil) case, then wires a real `gosched.Engine` over the actual production adapter types (`internal/scheduler.StoreAdapter`/`RunnerAdapter`, no fakes), calls `TickNow` (avoids depending on go-scheduler's hardcoded unexported 1s tick interval / a real-time sleep), and confirms `last_tick_at`/`dispatches`/`worker_errors` surface through the endpoint, then `Start()`/`Stop()`s the engine to prove `running` is live, not a static value.

**Build/test status:** `go build ./cmd/nanite/` clean. `go vet ./internal/api/... ./internal/store/...` clean. `go vet ./...` reports the same two pre-existing `internal/service/container.go` findings (`stopReaper`/`stopRuntimeReaper` possibly-unused-on-some-paths) present on `main` before this task's changes (confirmed via `git stash` + re-run) — not introduced by this task, not touched by it. `go test ./...` passes in full (`internal/api` `ok`, `internal/store` `ok`, `internal/scheduler` `ok`, whole-repo exit 0).

## Review notes

<!-- Reviewer fills in. -->
