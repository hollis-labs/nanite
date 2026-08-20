# Team definition CRUD API

**Phase:** 4 — Definition & launch API surface (`TASKS/teams`)
**Status:** not-started
**Depends on:** `01`
**Touches:** `internal/api/teams.go` (new).

## Context

Standard REST CRUD over task `01`'s `teams` store, following this codebase's own existing precedent (e.g. `/api/agents/{id}/...`, and the scheduling batch's `/api/schedules` — see `TASKS/scheduling/09-operator-http-api.md` for the closest recent analog, including its own explicit framing: *"makes a concrete, documented call but it isn't a locked security decision"* for the auth/permission model — same posture applies here, this task is not expected to resolve Nanite's broader operator-auth model, only to not leave this endpoint unguarded relative to comparable existing endpoints).

`teams.slots_json`/`authority_json`/`routing_json`/`phases_json` are free-form JSON columns (task `01`) — this is the first real write surface for them from outside a Go test, so it's the right place to enforce that malformed JSON is rejected at write time rather than surfacing later as a runtime decode failure deep in the compiler (task `07`) or launcher (task `08`). This mirrors `validateReflexDefinition`'s existing precedent (`internal/api/reflexes.go:297-314`) of rejecting malformed `action_spec` JSON at the API boundary, not downstream.

## What to do

1. `POST /api/teams`, `GET /api/teams`, `GET /api/teams/{id}`, `PATCH /api/teams/{id}`, `DELETE /api/teams/{id}` — thin handlers over task `01`'s store CRUD.
2. Validate that `slots_json` decodes into `[]TeamSlotDefinition` (task `01`'s type) at write time; validate `authority_json`/`routing_json`/`phases_json` against whatever typed shapes tasks `04`/`09`/`07` have defined by the time this task is dispatched — if any of those tasks haven't landed yet, validate at minimum that the field is well-formed JSON of the expected top-level shape (an array), and note in your Work Log which sub-structures got full typed validation versus JSON-shape-only validation, so a later task can tighten it without rediscovering the gap.
3. Confirm slot `Resolution`/`ActivationMode` string fields are validated against their real known-good values (`"durable"|"fresh"`; `"singleton"|"fresh-per-wake"|"concurrent"`) at write time, not left to fail silently at launch (task `08`).

## Done means

- Standard REST CRUD test coverage (create, get, list, patch, delete; 404 on a nonexistent ID).
- A write with malformed JSON in any sub-structure column, or an invalid `Resolution`/`ActivationMode` value, is rejected with a clear 400 at write time — covered by an explicit test, not just implied.
- `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
