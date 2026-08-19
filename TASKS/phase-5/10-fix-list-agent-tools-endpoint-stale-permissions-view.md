# Fix: `GET /api/agents/{id}/tools` still reports `allowed` from the legacy `tool_permissions` path, not `agent_tools`

**Phase:** 5
**Status:** implemented
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

Implemented by a Worker subagent. Read this task file, `docs/engineering/EXECUTION-PROCESS.md`, `internal/service/tool.go`'s `SelectForAgent`/`filterToolsByAgentTools`, and `internal/service/tool_execution_rules.go`'s `enforceExecutionRules`/`enforceExecutionRulesViaAgentTools` in full before writing any code — mirrored their exact `dbAgent`-resolution split, per the task's own instruction (no new pattern invented). Checked `docs/engineering/GLOSSARY.md`: no naming collisions — `dbAgent` is a plain local bool in this file (the other two surfaces use a `*store.AgentProfile`-typed local of the same name; not a new domain term), nothing else introduced is new vocabulary.

### The fix (item 1)

`internal/api/tools.go`'s `handleListAgentTools` now resolves `agentID` against `a.Services.Store.GetAgent(agentID)` first, exactly like `SelectForAgent`'s `dbAgent` check and `enforceExecutionRules`'s `s.store.GetAgent(agentID)` check:

- **Real `agent_profiles` row (`dbAgent == true`)**: `allowed` is computed from `a.Services.Store.ListAgentToolNames(ctx, agentID)` (the `agent_tools` grant set) unioned with the `known_tools.always_included` escape hatch (`a.Services.Store.ListAlwaysIncludedKnownTools(ctx)`, filtered to `status == "available"`) — the same two inputs `filterToolsByAgentTools` + `resolveAlwaysIncludedTools` (service/tool.go) and `enforceExecutionRulesViaAgentTools` (tool_execution_rules.go) already use. `tool_permissions`/`CheckPermission` is NOT consulted for this population, matching both existing surfaces' documented deny-semantics decision.
- **No real row (file-based agent, or an unresolvable ID)**: unchanged — `a.Services.ToolClient.GetPermissions(agentID)` → `perms.CheckPermission(t.Name)`, byte-for-byte the pre-existing legacy path.

Both `ListAgentToolNames`/`ListAlwaysIncludedKnownTools` calls degrade gracefully (empty map) on a store error rather than erroring the whole request — matches `filterToolsByAgentTools`'s own "deny non-escape-hatch tools" precedent in spirit (a DB-backed agent with a lookup failure sees a conservative "nothing granted, only the escape hatch" result rather than 500ing or silently falling back to the legacy default-permit path).

### Item 2 — audit of other `GetPermissions`/`CheckPermission` call sites

Grepped every call site of `ToolClient.GetPermissions`/`.CheckPermission` (and the `ToolPermissions.CheckPermission` method itself) across the whole tree (excluding `_test.go`):

- `internal/toolclient/broker.go` (`SelectToolsAsProvider`, `CallTool`, `HandleRequestToolsForAgent`, `GetPermissions`, `CheckPermission` itself) — internal selection/execution machinery, not an operator-facing read surface. Explicitly out of scope per this task's own item 2 carve-out.
- `internal/service/tool_execution_rules.go:92` — the file-based-agent legacy branch of `enforceExecutionRules`, already correctly gated behind the `dbAgent` split by `TASKS/phase-5/01`. Correct as-is.
- `internal/service/tool.go:910` (`filterToolsByPermissions`) — the non-`dbAgent` legacy branch of `SelectForAgent`, already correctly gated by `TASKS/phase-4/05`. Correct as-is.
- `internal/service/known_tools_backfill.go:144` (`legacyGrantCandidates`) — a one-time migration/backfill helper (`SyncKnownTools`'s legacy-grant backfill pass at startup), not a live request-time read surface an operator queries. Internal/execution-only by the same standard as the broker.go call sites; out of scope.

Also checked every other API handler for an `"allowed"`/`Allowed` JSON field to rule out a *different* operator-facing surface with the same class of gap: `internal/api/shell.go` (unrelated shell-command allowlist), `internal/api/reflexes.go` (`opt_out_allowed`, unrelated reflex config field), `internal/api/agents.go` (`copy_to_managed`, unrelated agent-class capability flag), `internal/api/a2a.go` ("Method not allowed" HTTP error strings). None of these compute a per-tool `allowed` flag from `tool_permissions`/`agent_tools`.

**Finding: no other operator-facing read surface has this gap.** This task's fix closes the third and — per this audit — last one.

### Testing

Added `internal/api/tools_test.go` (new):
- `TestHandleListAgentTools_DBBackedAgentUsesAgentTools` — creates a real `agent_profiles`-backed agent via `POST /api/agents`, grants exactly one known tool (`dev_read`) via the Phase 5 #01 grant endpoint, and confirms `GET /api/agents/{id}/tools` reports `dev_read: true` and every other catalog tool `false`. The literal Done-means scenario.
- `TestHandleListAgentTools_FileBasedAgentUnchanged` — a `file-`-prefixed agent ID (no real `agent_profiles` row) with a `PermissionResolver`-backed restrictive `allow_list` still reads its `allowed` flags from the legacy `tool_permissions` path, unaffected by the fix.

Both tests wire a throwaway `*toolclient.ToolClient` with a small builtin catalog (`newTestAPI`'s lightweight container doesn't wire one by default).

### Live-dogfeed verification (against a real running instance, per Done-means bullet 4)

Followed the exact precedent `TASKS/phase-5/01`'s Work Log established: built a standalone binary from this worktree (`go build -o <scratch>/nanite-verify ./cmd/nanite`), launched it from a fresh scratch CWD outside the repo with `NANITE_WORKSPACE=phase5-10-verify serve --port 8199` (an XDG workspace name never used before, isolated from the shared `default` workspace the project's `cerberus_resource_status`/CLAUDE.md instructions describe — deliberately did NOT touch the Cerberus-managed `nanite-api-service`, since that's the shared production surface, not a worker's scratch verification target), then exercised it via `curl`:

- `POST /api/agents` → created a real, DB-backed agent (`id: 78dda639-7446-47a0-8427-b9ee294f153d`, `tool_permissions: "{}"` — the exact "no tool_permissions configured" scenario the bug's live-dogfeed Context describes).
- `GET /api/agents/{id}/tools` (before any grant) → 159 catalog tools, only `tool_describe`/`tool_list` (the `always_included` escape hatch) reported `allowed:true`; every other tool `false`. This is the fixed behavior — pre-fix this would have read as all 159 `true` (everything permitted under the legacy path's default-permit for an agent with empty `tool_permissions`).
- Looked up the live-synced `known_tools` row for `dev_read` directly via `sqlite3` against the isolated DB, then `POST /api/agents/{id}/tools` with that `tool_id` → `201`, `{"tool_names":["dev_read"]}`.
- `GET /api/agents/{id}/tools` again → `allowed:true` set is now exactly `{dev_read, tool_describe, tool_list}` — the one granted tool plus the escape hatch, nothing else. **This reproduces and closes the exact live-verified gap in the task's Context.**
- Regression check: `GET /api/agents/file-researcher/tools` (a real file-based agent with `tool_permissions.allow_list = ["dev_read","dev_glob","dev_grep","tool_describe","tool_validate","lesson_capture"]`) → `allowed:true` set is exactly those 6 names — the legacy path, byte-for-byte unchanged.
- Regression check: `GET /api/agents/file-worker/tools` (a file-based agent with `allow_list: ["*"]`) → all 159/159 tools `allowed:true` — confirms the legacy wildcard-permissive behavior is untouched for that population too.

Shut the scratch server down, deleted the isolated `~/.local/share/nanite/workspaces/phase5-10-verify/` directory and the scratch build artifacts/CWD, and confirmed `git status --short` in this worktree shows only the intended `internal/api/tools.go` + `internal/api/tools_test.go` changes — no stray writes to any tracked file.

### Build/vet/test

`go build ./cmd/nanite/`: pass. `go vet ./...`: only the pre-existing `stopReaper`/`stopRuntimeReaper` context-leak finding in `internal/service/container.go` (a file this task never touches; confirmed via `git stash`/`git status --short` that it's present on the pre-change tree too — the same finding Phase 1 #04's, Phase 4 #05's, and Phase 5 #01's own Work Logs already documented as pre-existing). `go test ./...`: full repo, every package, pass, including the two new `internal/api/tools_test.go` cases.

No escalations.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
