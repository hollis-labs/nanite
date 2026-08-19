# Build the `agent_reflex_propose` self-tool; test `pending_reflexes` in real sessions

**Phase:** 3
**Status:** not-started
**Depends on:** none functionally (independent of tasks 01/02 — `pending_reflexes` is a separate, already-complete backend), but natural to sequence after 01/02 since all three touch `internal/agent/reflexes`-adjacent code and testing benefits from the dispatch migration being settled first.
**Touches:** `internal/mcp/self_tools*.go` (new tool registration, pattern per existing self-tools like `self_tools_validate.go`/`self_tools_workflow_run.go`), `internal/store/agent_reflexes.go` (`InsertPendingReflex` — read, not modified, unless the propose path needs its own validation variant), `internal/api/reflexes.go` (`validateReflexDefinition` — check if it should also run pre-insert on the propose path).

## Context

TASKS.md Phase 3: *"Build the missing `agent_reflex_propose` self-tool and test `pending_reflexes` in real sessions."* Architecture doc `03-steering.md`: *"`pending_reflexes` (agent proposes its own reflex, operator approves) — complete backend, missing only the self-tool that would let an agent call it. Build the missing piece and test before deciding."*

### The backend is genuinely complete — confirmed directly

- **Table**: `pending_reflexes` (migration `075_agent_reflexes.sql:59-78`) — `id`, `proposed_by`, `proposed_at`, `target_agent_id` (FK → `agent_profiles`), `name`, `trigger_kind`, `trigger_spec`, `action_kind`, `action_spec`, `rationale`, `status` (`pending`/`approved`/`rejected` CHECK), `reviewed_at`, `reviewed_by`.
- **Store methods**, `internal/store/agent_reflexes.go`: `InsertPendingReflex` (:358), `GetPendingReflex` (:400), `ListPendingReflexes` (:416), `ApprovePendingReflex` (:454 — promotes the row into a real `agent_reflexes` row), `RejectPendingReflex` (:505).
- **REST API**, `internal/api/reflexes.go`: `handleListPendingReflexes`/`handleApprovePendingReflex`/`handleRejectPendingReflex`, routed at `internal/api/api.go:138-140` (`GET /api/pending/reflexes`, `POST /api/pending/reflexes/{id}/approve`, `POST /api/pending/reflexes/{id}/reject`).

### What's actually missing: the write path, i.e. the whole point of "agent proposes"

Grepping `internal/mcp/self_tools*.go` finds zero tool registered for this. The only reference anywhere in the codebase is a forward-looking doc-comment on the `PendingReflex` struct itself (`internal/store/agent_reflexes.go:71-73`): *"The `agent_reflex_propose` self-tool inserts these; the operator review API approves to `agent_reflexes` or rejects with a reason."* This is a tool that was documented before it was built.

## What to do

1. Build the `agent_reflex_propose` MCP self-tool, following the registration/handler pattern of existing self-tools in `internal/mcp/self_tools.go` and its siblings. Handler calls `InsertPendingReflex`, populating `proposed_by` (the calling agent's identity — resolve the same way other self-tools identify their caller), `target_agent_id`, `trigger_kind`/`trigger_spec`, `action_kind`/`action_spec` (using whatever action kinds exist post-task-01, including the new `dispatch_to_agent` kind), and a required `rationale` string.
2. Check whether `internal/api/reflexes.go`'s `validateReflexDefinition` (used on the operator-created `agent_reflexes` path, lines ~253-291) should also validate the propose path's input pre-insert — it currently validates `store.AgentReflex` shape, not `store.PendingReflex` shape; decide whether to adapt it or write a narrower propose-specific validator, and document which.
3. Write the tool's description text carefully (per the truncation-history caution elsewhere in this phase — this is a new tool description, not a truncation fix, but keep it accurate and not misleadingly terse) so an agent understands what a reflex proposal actually does (creates a pending row an operator must approve — it does not immediately take effect).
4. Test end to end in a real session: have a real agent call `agent_reflex_propose`, confirm the row lands via `GET /api/pending/reflexes`, approve it via the existing REST endpoint, confirm it's promoted into a real, firing `agent_reflexes` row. Also test the reject path.
5. Based on real usage (not static review), record a keep/re-architect/cut recommendation for `pending_reflexes` as a mechanism in this file's Work Log — per architecture doc 03, this is "kept, actively being evaluated," not a settled keep.

## Done means

- `agent_reflex_propose` is a real, callable self-tool; an agent can propose a reflex and it lands as a real `pending_reflexes` row with all required fields populated (not a stub with placeholder rationale).
- A real approve round-trip (propose → list → approve → confirm the resulting `agent_reflexes` row fires) has been exercised at least once in a real session, not just unit-tested against a fixture.
- A real reject round-trip has also been exercised.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- This file's Work Log records the real-usage keep/re-architect/cut recommendation for `pending_reflexes`, with the observed evidence (not a guess) — this is the actual deliverable the "test before deciding" framing asks for, not just "the tool exists."

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
