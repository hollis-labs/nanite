# Remove `tool_permissions`/`CheckPermission` entirely; collapse all agent-tool-permission surfaces to `agent_tools`-only

**Status:** not-started
**Depends on:** `TASKS/adhoc/01-eliminate-file-based-agent-runtime.md` (landed and validated 2026-08-19 — see `TASKS/INDEX.md`'s "Ad hoc tasks" section and that task's own Work Log)
**Touches:** `internal/toolclient/permissions.go`, `internal/toolclient/broker.go`, `internal/service/agent_permissions.go` (delete), `internal/service/tool.go` (`SelectForAgent`), `internal/service/tool_execution_rules.go` (`enforceExecutionRulesViaAgentTools`), `internal/api/tools.go` (`handleListAgentTools`), `internal/store` (`agent_profiles.tool_permissions` column — see step on schema below)

**Scope note (2026-08-19, Orchestrator, post-`01` landing):** `01`'s own implementation already removed surface 4 (`internal/api/agent_tools.go`'s `requireRealAgentToolsTarget`) in full — it had no independent logic beyond the now-deleted `agent.IsFileBasedID` check, so it was correctly swept as part of `01`'s own call-site cleanup rather than left for this task. `01` also already left `internal/service/agent_permissions.go`'s `newFileAgentPermissionResolver` stubbed to a permanent no-op (unwired from `container.go`, no longer callable) but did **not** delete it, per its own task file's explicit instruction to leave full deletion to this task. This task's remaining scope is therefore: surfaces 1-3 below, deleting `agent_permissions.go` and the `tool_permissions`/`CheckPermission` machinery itself, and the schema question in step 6. Step 4 below (re-examining `requireRealAgentToolsTarget`) is done — nothing left to do there.

## Context

Discovered and explained (imprecisely, then corrected) during this session's own Phases 2-5 wrap-up: `tool_permissions` is a JSON column on `agent_profiles` (`toolclient.ToolPermissions{AllowList, DenyList, MaxCallsPerTurn, ...}`, checked via `ToolClient.CheckPermission`) — a **deny-only** model where an empty allow-list means everything is allowed. `agent_tools` (migration `116_known_tools_and_agent_tools.sql`, Phase 1 #04) is the newer, **opt-in** model — a tool is allowed only if a grant row exists. The two coexisted because file-based agents (no real `agent_profiles` row reachable the normal way) needed *some* permission mechanism, and `tool_permissions` was it.

Once `TASKS/adhoc/01-eliminate-file-based-agent-runtime.md` lands, there is no longer any population that needs `tool_permissions` — every agent is a real DB row reachable via `store.GetAgent`, so `agent_tools` covers 100% of agents, not "most of them, plus a legacy fallback for the rest."

### The four surfaces that currently branch on `dbAgent := store.GetAgent(agentID)` succeeding-or-failing to decide `agent_tools` vs. legacy `tool_permissions`

1. `internal/service/tool.go`'s `SelectForAgent` (fixed to prefer `agent_tools` in Phase 4 `#05`) — still has a legacy fallback branch for when `GetAgent` fails.
2. `internal/service/tool_execution_rules.go`'s `enforceExecutionRulesViaAgentTools` (Phase 5 `#01`) — same shape.
3. `internal/api/tools.go`'s `handleListAgentTools` (Phase 5 `#10`) — same shape.
4. `internal/api/agent_tools.go`'s `requireRealAgentToolsTarget` (Phase 5 `#01`) — doesn't fall back, it hard-*rejects* grant/revoke calls against an agent `GetAgent` can't find, with a 400. Once `01` lands, `GetAgent` should never fail for a real agent again, so this guard becomes dead weight (though verify — it may still be legitimate as a "does this agent ID exist at all" check, just no longer needing the file-based-specific framing).

Also delete `internal/service/agent_permissions.go`'s `newFileAgentPermissionResolver` in full — it's a `toolclient.PermissionResolver` built exclusively for file-based IDs (gated on `agent.IsFileBasedID`), unreachable dead code once `01` lands. Find and remove wherever it's wired into `ToolClient`'s construction.

## What to do

1. Confirm `01` has actually landed and verify its own "Done means" bar holds (a quick `grep -rn "IsFileBasedID" --include="*.go" internal/agent internal/service internal/api internal/messaging` should return nothing) before starting — if it doesn't, stop, this task isn't safe to run yet.
2. Delete `internal/service/agent_permissions.go` (`newFileAgentPermissionResolver`) and its wiring into `ToolClient`.
3. Remove the legacy-fallback branch from all three of surfaces 1-3 above — each should become unconditionally `agent_tools`-based, no `dbAgent`-succeeds-or-fails split left (since it always succeeds now).
4. Re-examine `requireRealAgentToolsTarget` (`internal/api/agent_tools.go:158`) — decide whether it should be deleted outright (if `GetAgent` failing is now a genuine "agent doesn't exist" 404 case handled elsewhere) or simplified (kept as a not-found check, but with the file-based-specific comment/framing removed). Document your reasoning either way.
5. Remove `toolclient.CheckPermission`/`GetPermissions`/`ToolPermissions`/`ParsePermissions` and the `tool_permissions` JSON parsing path from `internal/toolclient/` if nothing else references them after steps 2-4 — confirm via grep before deleting, don't assume.
6. **Schema**: decide whether to actually `DROP COLUMN tool_permissions` from `agent_profiles` in this task, or leave the column in place (dead, unread) for now and drop it in a later, separate migration once you're confident nothing — including any external tooling, export/import paths, or admin UI — still reads or writes it. Recommendation: leave the column in place for this task (safer, reversible) unless your own investigation turns up a clean, low-risk path to drop it now; note your decision and reasoning in the Work Log either way. Also check for a `toolPermissions:` YAML frontmatter field on any remaining agent-file-adjacent code paths and remove references there too.
7. Live-verify against the real deployed `nanite-api-service`: create a fresh test agent, confirm zero tools allowed before any `agent_tools` grant (not "everything allowed," which was the old deny-only default), grant one tool, confirm exactly `{granted tool} + always_included` is allowed via all three read/enforcement surfaces. Clean up the test agent after.

## Done means

- Zero remaining references to `tool_permissions`/`CheckPermission`/`GetPermissions`/`ToolPermissions`/`newFileAgentPermissionResolver` in non-test Go code (confirm via grep).
- All three permission-surfaces (`SelectForAgent`, `enforceExecutionRulesViaAgentTools`, `handleListAgentTools`) are unconditionally `agent_tools`-based, no legacy branch remains.
- `requireRealAgentToolsTarget` either removed or simplified per step 4, with reasoning documented.
- `go build ./cmd/nanite/`, `go vet ./...` (no new findings beyond the 2 pre-existing `container.go` ones), `go test ./...` all green.
- Live dogfeed per step 7, documented in the Work Log.

## Out of scope

- Anything already covered by `TASKS/adhoc/01-eliminate-file-based-agent-runtime.md` — this task assumes that one is done.
- The broader agent-roster audit/cull — `TASKS/phase-2/07-audit-agent-roster.md`.
