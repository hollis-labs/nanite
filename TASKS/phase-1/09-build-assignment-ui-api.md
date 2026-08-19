# Build the assignment API — backend only, no frontend work

**Phase:** 1
**Status:** not-started
**Depends on:** `01`-`08` (every new column/table this API needs to expose must exist first — `08`'s Round 2 in particular, since the API surface should reflect the file-discovery cut's end state, not the pre-cut behavior)

## ⚠️ Scope correction, 2026-08-18 (operator decision, standing rule for every phase)

**No frontend work is part of any phase.** This task's original scope (filename kept as `09-build-assignment-ui-api.md` for cross-reference stability — 7 other files already point at it — but read it as API-only) included React component work (`AgentDetailView.tsx`, a new `RoleDetailView.tsx`/`RoleCreateWizard.tsx`, replacing `ToolPermissionsEditor.tsx`'s free-text editing with a picker, etc.). **All of that is cut from this task, and from Phase 1 entirely.** This is not a Phase-1-specific call — the operator's instruction was general: backend/API only, in every phase, going forward. Whoever picks up a later phase with UI-shaped items in its own scope (e.g. Phase 5's Cards work) should get this same correction applied there too — not this task's job to edit those files, just flagging it so it isn't missed.

This task's remaining, real scope is the **REST API surface** the new composition model needs — none of it currently exists end-to-end (see Context for exactly what's missing vs. already built).

**Touches:** `internal/api/agents.go` (`CreateAgentRequest`/`UpdateAgentRequest` — add `role_id`/`consumer_id`/`model_id` fields, currently absent), `internal/api/api.go` (route registration), new `internal/api/consumers.go` (no REST layer exists yet for `consumers` — task `03` deliberately deferred this here), `internal/api/agent_tools.go` or extend `agents.go` (grant/revoke endpoints for `agent_tools` — only a `GET .../tools` list endpoint exists today, per `04`'s own scope)

## Context — what already exists vs. what's actually missing (verified directly, not assumed)

- **`roles` REST CRUD already exists in full** (`internal/api/roles.go`, built by task `01`: `GET/POST /api/roles`, `GET/PUT/DELETE /api/roles/{id}`). Nothing to build here.
- **`agent_profiles.role_id`/`consumer_id`/`model_id` are NOT settable via the API today**, confirmed live during Wave 2's validation checkpoint: `CreateAgentRequest`/`UpdateAgentRequest` (`internal/api/types.go`) have no fields for any of the three. The columns exist (`02`, `03`) and are correctly readable (once `12` lands), but there's no write path via the API yet. This is real, missing work.
- **`consumers` has store-layer CRUD only, no REST** (`internal/store/consumers.go`, task `03`'s own Work Log explicitly deferred the REST layer to this task). Build `GET/POST /api/consumers`, `GET/PUT/DELETE /api/consumers/{id}`, matching the existing route-naming convention `roles.go` already established.
- **`agent_tools` has a list endpoint only** (`GET /api/agents/{id}/tools`, from task `04`). No grant/revoke (`POST`/`DELETE`) endpoints exist yet — an operator (or a future UI, whenever one gets built in some later, explicitly-scoped effort) needs a way to actually assign/remove a tool grant via the API, not just read the current set.
- **`agent_dispatch_tool_allowlist`** (task `04`'s renamed table, resolving the `agent_dispatch_allowlist`/`parent_dispatch_allowlist` naming collision) — check whether anything currently needs a REST surface for it before building one; if nothing consumes it yet, note that in the Work Log and skip rather than building speculative CRUD.

## What to do

1. Add `role_id`, `consumer_id`, `model_id` fields to `CreateAgentRequest`/`UpdateAgentRequest` (`internal/api/types.go`) and wire them through `handleCreateAgent`/`handleUpdateAgent` (`internal/api/agents.go`) — following the same nullable/pointer-field convention already used for `activation_mode` on the update path.
2. Build `internal/api/consumers.go`: full REST CRUD for `consumers`, matching `roles.go`'s existing shape and route-naming convention. Register routes in `internal/api/api.go`.
3. Add grant/revoke endpoints for `agent_tools` — e.g. `POST /api/agents/{id}/tools` (grant), `DELETE /api/agents/{id}/tools/{toolId}` (revoke) — backed by the store-layer functions task `04` already built (`internal/store/agent_tools.go`).
4. Check whether `agent_dispatch_tool_allowlist` needs a REST surface yet (see Context) — build it only if something real consumes it; otherwise note the skip and why.
5. Do not touch anything under `ui/` — no exceptions. If you find yourself about to edit a `.tsx` file, stop; that's not this task's job in this phase.

## Done means

- `role_id`/`consumer_id`/`model_id` are settable via `POST`/`PUT /api/agents` and correctly readable back afterward (verified live against a running instance, not just `go test` — the same live-dogfeed discipline the rest of Phase 1 has used).
- `consumers` has full REST CRUD, exercised by at least one integration test.
- `agent_tools` grant/revoke works via the API, exercised by at least one integration test, and correctly rejects a grant referencing a nonexistent `known_tools` row or a nonexistent agent (same FK-integrity discipline as task `05`).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- No file under `ui/` is touched.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
