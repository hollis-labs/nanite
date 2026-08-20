# Operator HTTP API — `/api/schedules`

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** not-started
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

Not started.

## Review notes

<!-- Reviewer fills in. -->
