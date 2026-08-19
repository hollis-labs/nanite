# Wire `SelectForAgent` to actually read/enforce `agent_tools` (split off from 04)

*(Relocated from `TASKS/phase-1/11` — worktree `phase-1-execution` — as part of the 2026-08-19 Phase 2-9 resequencing.)*

**Phase:** 4
**Status:** not-started
**Depends on:** Phase 1's `04-add-known-tools-and-agent-tools-fk` (landed via the Phase 1→main merge) — schema, live-sync, and one-time legacy-column backfill must exist first
**Touches:** `internal/service/tool.go` (`SelectForAgent`, `filterToolsByAllowlist`, `filterToolsByPermissions`), `internal/toolclient/broker.go` (`CheckPermission`, `ToolClient.SelectToolsAsProvider`'s own permission pass), `internal/toolclient/permissions.go` (`ToolPermissions`/`ParsePermissions` — only if the deny/allow-glob semantics need a real answer against `agent_tools`, see Context)

## Context

Found during `04`'s implementation (2026-08-18), per that task's own explicit instruction: *"Actually wiring `internal/service/tool.go`'s `SelectForAgent` to read `agent_tools` instead of the legacy columns may be substantial enough to warrant its own follow-up task if it grows beyond a straightforward read-path swap — assess during implementation and escalate/split if so, rather than silently under-scoping this task."* It did grow beyond a mechanical swap, for a specific, verified reason below — this task is that follow-up, not a re-litigation of `04`'s decision to defer it.

`agent_profiles.tools` (schema-v2 allowlist) and `agent_profiles.tool_permissions` (`toolclient.ToolPermissions` — `allow_list`/`deny_list`, both **glob-capable** via `toolclient.MatchPattern`, deny-wins-over-allow) are two independently-applied filters `SelectForAgent` runs in sequence today (`filterToolsByAllowlist` then `filterToolsByPermissions`/`CheckPermission`). `agent_tools` (04's new table) is a **plain positive-grant join** — `(agent_id, tool_id)` rows, no glob, no deny concept. Replacing the first two with a read from the third is not a mechanical swap because:

- A `deny_list` glob (e.g. `"dev_*"` denied while everything else is allowed) has no representation in a positive-grant-only join — there is no "everything except X" shape in `agent_tools` as specified by `01-agent-construction.md`. Modeling deny semantics on top of `agent_tools` (a parallel deny-join? a policy-expression column? leaving `tool_permissions` alive as a second, still-enforced layer indefinitely?) is a real design decision the architecture doc does not answer — it only says `agent_tools` "replaces `tools:`/`toolPermissions:`/`roleTools:` entirely," not how deny-glob semantics carry over.
- `04`'s one-time backfill (`internal/service/known_tools_backfill.go`) already resolved this ambiguity **for the data**, by replaying `ToolPermissions.CheckPermission` against the live catalog at backfill time and only granting what survives — i.e., the backfilled `agent_tools` rows already reflect each agent's current *effective* selection (allow ∩ NOT deny), snapshotted once. That resolves "what should today's data look like" but not "what should the live enforcement mechanism be going forward" once an operator starts editing `agent_tools` directly (e.g. via `TASKS/phase-5/01-build-assignment-api.md`'s grant/revoke endpoints) independently of the frozen legacy columns.
- `ToolClient.SelectToolsAsProvider`'s own internal permission pass (`toolclient/broker.go`) also calls `CheckPermission` before tools ever reach `service/tool.go` — a full swap has to decide whether that inner layer also gets rewired, or stays as a defense-in-depth backstop reading the (deprecated but still populated) legacy column indefinitely.

## What to do

1. Decide the deny-semantics question above: either (a) design a real deny/exclusion mechanism that sits alongside `agent_tools` (name it distinctly — check `GLOSSARY.md` first, same discipline `04` applied), or (b) make a deliberate, documented call that `agent_tools` membership is allow-only going forward and `tool_permissions.deny_list` is retired in effect (not just deprecated in name) once an agent has real `agent_tools` rows, with a clear migration story for agents that still rely on deny-glob behavior. Don't silently pick one without recording the reasoning.
2. Rewire `SelectForAgent` (`internal/service/tool.go`) to read granted tool names via `agent_tools` (through `known_tools`) as the primary selection filter, replacing `filterToolsByAllowlist(allTools, agent.Tools)`. Decide whether `filterToolsByPermissions`/`CheckPermission` stays as a second pass per (1)'s answer.
3. Decide whether `ToolClient.SelectToolsAsProvider`'s own internal `CheckPermission` call (`toolclient/broker.go`) needs to change too, or can remain reading the legacy column as a defense-in-depth backstop indefinitely (call out the resulting "two systems of record" tradeoff explicitly if so).
4. Handle the "empty `agent_tools`" case correctly: per `04`'s backfill, an agent with no legacy restriction was backfilled with a grant row for every tool available *at backfill time* — decide whether the live read path should also auto-grant newly-added catalog tools to such an agent going forward (matching today's "no restriction" live behavior) or requires an explicit re-sync/re-grant step (a real behavior change from today, which must be flagged if chosen).
5. Always include `known_tools.always_included=true` rows regardless of `agent_tools` membership (the `request_tools`/`tool_list`/`tool_describe` escape hatch) — `04` created the flag and the column; this task is what actually has to honor it in the live selection path.
6. Update/extend `TASKS/phase-5/01-build-assignment-api.md`'s grant/revoke expectations if this task's answer to (1)/(4) changes what "revoking a tool" or "an agent with zero explicit grants" means for an operator using that API.

## Done means

- `SelectForAgent` reads `agent_tools` (not `agent_profiles.tools`) as the source of truth for which known tools an agent may see, for at least the common case.
- The deny-semantics question (item 1) has a recorded decision, not a silent gap.
- The `always_included` escape-hatch tools are verified to survive selection for an agent with zero explicit `agent_tools` grants.
- Existing `internal/service/tool_test.go`-style coverage for `filterToolsByAllowlist`/`filterToolsByPermissions` is updated or superseded to match the new read path, not left testing dead code.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
