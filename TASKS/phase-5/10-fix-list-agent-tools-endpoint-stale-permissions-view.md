# Fix: `GET /api/agents/{id}/tools` still reports `allowed` from the legacy `tool_permissions` path, not `agent_tools`

**Phase:** 5
**Status:** not-started
**Depends on:** `TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md`, `TASKS/phase-5/01-build-assignment-api.md` (both already landed on `main` — this task fixes a real gap found during Phase 5's close-out live dogfeed validation, orthogonal to what either task's own stated scope covered)
**Touches:** `internal/api/tools.go` (`handleListAgentTools`)

## Context

Found by the Orchestrator during Phase 5's real dogfeed validation against the live `nanite-api-service`, 2026-08-19, immediately after merging all 5 actionable Phase 5 tasks. Not a stop-and-escalate finding — confirmed unrelated to any locked decision, fixed as its own task per the same fix-as-new-worker-task discipline `TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md` already established for this batch.

### The gap, confirmed live

Created a real test agent (a genuine `agent_profiles` row, not file-backed), granted it exactly one tool (`dev_read`) via `TASKS/phase-5/01-build-assignment-api.md`'s new `POST /api/agents/{id}/tools` grant endpoint (confirmed working — the grant succeeded, `tool_names: ["dev_read"]`), then called the **pre-existing** `GET /api/agents/{id}/tools` list endpoint (`internal/api/tools.go:94`'s `handleListAgentTools`, built in an earlier phase per this task's own dependency task 01's own Context: *"`agent_tools` has a list endpoint only (`GET /api/agents/{id}/tools`, from Phase 1 task `04`)"*). Every tool in the full catalog — not just `dev_read` — came back `"allowed": true`.

Read the handler directly: it computes `allowed` via `a.Services.ToolClient.GetPermissions(agentID)` → `perms.CheckPermission(t.Name)` (`internal/toolclient/broker.go`'s `GetPermissions`/`CheckPermission`, `internal/toolclient/permissions.go`'s `ToolPermissions.CheckPermission`) — the **legacy** `agent_profiles.tool_permissions` allow/deny-glob mechanism. An agent with no `tool_permissions` configured (the normal case for a freshly-created agent, including the one used in this live test) reads as "everything allowed" under that legacy check — completely independent of `agent_tools` grants.

This is the exact same class of gap `TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md`'s Work Log already fixed at the `SelectForAgent` (turn-time tool selection) surface, and `TASKS/phase-5/01-build-assignment-api.md`'s Work Log already fixed at the `enforceExecutionRules` (execution-time re-check) surface — but this third read surface, the one an operator would naturally reach for to answer "what can this agent actually do" via the very assignment API this phase just built, was never touched by either task (out of both tasks' stated `Touches`) and still reports the pre-`agent_tools` answer.

### Why this matters

An operator granting/revoking tools via `01`'s new endpoints has no live-checked way to confirm what actually happened — the one GET endpoint that looks like it should answer that question gives a stale, misleading "everything is allowed" answer for any DB-backed agent, regardless of its real `agent_tools` grants. This is the same "two systems of record" pattern `04`'s and `01`'s own Work Logs already flagged and closed at the other two surfaces — this is the third and (as far as this task's own investigation should confirm) final one.

## What to do

1. Rewire `handleListAgentTools` to mirror the exact same `dbAgent`-resolution split `internal/service/tool.go`'s `SelectForAgent`/`filterToolsByAgentTools` and `internal/service/tool_execution_rules.go`'s `enforceExecutionRulesViaAgentTools` already use: for an `agentID` that resolves to a real `agent_profiles` row, `allowed` should reflect `agent_tools` grant membership (+ the `known_tools.always_included` escape hatch) — not `tool_permissions`. For an `agentID` that does NOT resolve to a real row (a file-based agent, which cannot have `agent_tools` rows), keep the existing `tool_permissions`/`CheckPermission` behavior unchanged.
2. Confirm no other read surface has the same gap — grep for other callers of `ToolClient.GetPermissions`/`CheckPermission` against an agent ID and check whether any of them face an operator/API consumer the way this one does (internal/execution-only call sites are out of scope; this task's job is the operator-facing read surface specifically). Document what you find either way.
3. Do not change the grant/revoke endpoints (`01`'s work) or `SelectForAgent`/`enforceExecutionRules` (already correct) — this task is the third read-surface fix only.

## Done means

- A real test agent (a genuine `agent_profiles` row) with exactly one `agent_tools` grant, queried via `GET /api/agents/{id}/tools`, shows that one tool as `allowed: true` and every other tool as `allowed: false` — reproducing and closing the exact live-verified gap in this task's Context.
- A file-based agent's `GET /api/agents/{id}/tools` behavior is unchanged (regression check) — still reflects `tool_permissions`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Verified in a real session against a real running instance, not just `go test` — the same live-dogfeed discipline this whole batch has used throughout.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
